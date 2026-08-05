/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package servers

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/genproto/googleapis/api/httpbody"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/clientcmd"
	clnt "sigs.k8s.io/controller-runtime/pkg/client"

	osacv1alpha1 "github.com/osac-project/osac-operator/api/v1alpha1"

	privatev1 "github.com/osac-project/fulfillment-service/internal/api/osac/private/v1"
	publicv1 "github.com/osac-project/fulfillment-service/internal/api/osac/public/v1"
	"github.com/osac-project/fulfillment-service/internal/auth"
	"github.com/osac-project/fulfillment-service/internal/database/dao"
	"github.com/osac-project/fulfillment-service/internal/events"
	"github.com/osac-project/fulfillment-service/internal/jq"
	"github.com/osac-project/fulfillment-service/internal/kubernetes/gvks"
	"github.com/osac-project/fulfillment-service/internal/kubernetes/labels"
)

type ClustersServerBuilder struct {
	logger            *slog.Logger
	notifier          events.Notifier
	attributionLogic  auth.AttributionLogic
	tenancyLogic      auth.TenancyLogic
	metricsRegisterer prometheus.Registerer
	scheme            *runtime.Scheme
}

var _ publicv1.ClustersServer = (*ClustersServer)(nil)

type ClustersServer struct {
	publicv1.UnimplementedClustersServer

	logger          *slog.Logger
	private         privatev1.ClustersServer
	inMapper        *GenericMapper[*publicv1.Cluster, *privatev1.Cluster]
	outMapper       *GenericMapper[*privatev1.Cluster, *publicv1.Cluster]
	jqTool          *jq.Tool
	hubsDao         *dao.GenericDAO[*privatev1.Hub]
	kubeClients     map[string]clnt.Client
	kubeClientsLock *sync.Mutex
	scheme          *runtime.Scheme
}

func NewClustersServer() *ClustersServerBuilder {
	return &ClustersServerBuilder{}
}

// SetLogger sets the logger to use. This is mandatory.
func (b *ClustersServerBuilder) SetLogger(value *slog.Logger) *ClustersServerBuilder {
	b.logger = value
	return b
}

// SetNotifier sets the notifier to use. This is optional.
func (b *ClustersServerBuilder) SetNotifier(value events.Notifier) *ClustersServerBuilder {
	b.notifier = value
	return b
}

// SetAttributionLogic sets the attribution logic to use. This is optional.
func (b *ClustersServerBuilder) SetAttributionLogic(value auth.AttributionLogic) *ClustersServerBuilder {
	b.attributionLogic = value
	return b
}

// SetTenancyLogic sets the tenancy logic to use. This is mandatory.
func (b *ClustersServerBuilder) SetTenancyLogic(value auth.TenancyLogic) *ClustersServerBuilder {
	b.tenancyLogic = value
	return b
}

// SetMetricsRegisterer sets the Prometheus registerer used to register the metrics for the underlying database
// access objects. This is optional. If not set, no metrics will be recorded.
func (b *ClustersServerBuilder) SetMetricsRegisterer(value prometheus.Registerer) *ClustersServerBuilder {
	b.metricsRegisterer = value
	return b
}

// SetScheme sets the Kubernetes runtime scheme used for typed API objects.
// This is mandatory.
func (b *ClustersServerBuilder) SetScheme(value *runtime.Scheme) *ClustersServerBuilder {
	b.scheme = value
	return b
}

func (b *ClustersServerBuilder) Build() (result *ClustersServer, err error) {
	// Check parameters:
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}
	if b.tenancyLogic == nil {
		err = errors.New("tenancy logic is mandatory")
		return
	}
	if b.scheme == nil {
		err = errors.New("scheme is mandatory")
		return
	}

	// Create the JQ tool:
	jqTool, err := jq.NewTool().
		SetLogger(b.logger).
		Build()
	if err != nil {
		return
	}

	// Create the DAOs:
	hubsDao, err := dao.NewGenericDAO[*privatev1.Hub]().
		SetLogger(b.logger).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		Build()
	if err != nil {
		return
	}

	// Find the full name of the 'status' field so that we can configure the generic server to ignore it. This is
	// because users don't have permission to change the status.
	var object *publicv1.Cluster
	objectReflect := object.ProtoReflect()
	objectDesc := objectReflect.Descriptor()
	statusField := objectDesc.Fields().ByName("status")
	if statusField == nil {
		err = fmt.Errorf("failed to find the status field of type '%s'", objectDesc.FullName())
		return
	}

	// Create the mappers:
	inMapper, err := NewGenericMapper[*publicv1.Cluster, *privatev1.Cluster]().
		SetLogger(b.logger).
		SetStrict(true).
		AddIgnoredFields(statusField.FullName()).
		Build()
	if err != nil {
		return
	}
	outMapper, err := NewGenericMapper[*privatev1.Cluster, *publicv1.Cluster]().
		SetLogger(b.logger).
		SetStrict(false).
		Build()
	if err != nil {
		return
	}

	// Create the private server to delegate to:
	delegate, err := NewPrivateClustersServer().
		SetLogger(b.logger).
		SetNotifier(b.notifier).
		SetAttributionLogic(b.attributionLogic).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		Build()
	if err != nil {
		return
	}

	// Create and populate the object:
	result = &ClustersServer{
		logger:          b.logger,
		jqTool:          jqTool,
		hubsDao:         hubsDao,
		kubeClients:     map[string]clnt.Client{},
		kubeClientsLock: &sync.Mutex{},
		private:         delegate,
		inMapper:        inMapper,
		outMapper:       outMapper,
		scheme:          b.scheme,
	}
	return
}

func (s *ClustersServer) List(ctx context.Context,
	request *publicv1.ClustersListRequest) (response *publicv1.ClustersListResponse, err error) {
	// Create private request with same parameters:
	privateRequest := &privatev1.ClustersListRequest{}
	privateRequest.SetOffset(request.GetOffset())
	privateRequest.SetLimit(request.GetLimit())
	privateRequest.SetFilter(request.GetFilter())

	// Delegate to private server:
	privateResponse, err := s.private.List(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	// Map private response to public format:
	privateItems := privateResponse.GetItems()
	publicItems := make([]*publicv1.Cluster, len(privateItems))
	for i, privateItem := range privateItems {
		publicItem := &publicv1.Cluster{}
		err = s.outMapper.Copy(ctx, privateItem, publicItem)
		if err != nil {
			s.logger.ErrorContext(
				ctx,
				"Failed to map private cluster to public",
				slog.Any("error", err),
			)
			return nil, grpcstatus.Errorf(grpccodes.Internal, "failed to process clusters")
		}
		redactClusterSecrets(publicItem)
		publicItems[i] = publicItem
	}

	// Create the public response:
	response = &publicv1.ClustersListResponse{}
	response.SetSize(privateResponse.GetSize())
	response.SetTotal(privateResponse.GetTotal())
	response.SetItems(publicItems)
	return
}

func (s *ClustersServer) Get(ctx context.Context,
	request *publicv1.ClustersGetRequest) (response *publicv1.ClustersGetResponse, err error) {
	// Create private request:
	privateRequest := &privatev1.ClustersGetRequest{}
	privateRequest.SetId(request.GetId())

	// Delegate to private server:
	privateResponse, err := s.private.Get(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	// Map private response to public format:
	privateCluster := privateResponse.GetObject()
	publicCluster := &publicv1.Cluster{}
	err = s.outMapper.Copy(ctx, privateCluster, publicCluster)
	if err != nil {
		s.logger.ErrorContext(
			ctx,
			"Failed to map private cluster to public",
			slog.Any("error", err),
		)
		return nil, grpcstatus.Errorf(grpccodes.Internal, "failed to process cluster")
	}

	redactClusterSecrets(publicCluster)

	// Create the public response:
	response = &publicv1.ClustersGetResponse{}
	response.SetObject(publicCluster)
	return
}

func (s *ClustersServer) Create(ctx context.Context,
	request *publicv1.ClustersCreateRequest) (response *publicv1.ClustersCreateResponse, err error) {
	// Map the public cluster to private format:
	publicCluster := request.GetObject()
	if publicCluster == nil {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument, "object is mandatory")
		return
	}
	privateCluster := &privatev1.Cluster{}
	err = s.inMapper.Copy(ctx, publicCluster, privateCluster)
	if err != nil {
		s.logger.ErrorContext(
			ctx,
			"Failed to map public cluster to private",
			slog.Any("error", err),
		)
		err = grpcstatus.Errorf(grpccodes.Internal, "failed to process cluster")
		return
	}

	// Delegate to the private server:
	privateRequest := &privatev1.ClustersCreateRequest{}
	privateRequest.SetObject(privateCluster)
	privateResponse, err := s.private.Create(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	// Map the private response back to public format:
	createdPrivateCluster := privateResponse.GetObject()
	createdPublicCluster := &publicv1.Cluster{}
	err = s.outMapper.Copy(ctx, createdPrivateCluster, createdPublicCluster)
	if err != nil {
		s.logger.ErrorContext(
			ctx,
			"Failed to map private cluster to public",
			slog.Any("error", err),
		)
		err = grpcstatus.Errorf(grpccodes.Internal, "failed to process cluster")
		return
	}

	redactClusterSecrets(createdPublicCluster)

	// Create the public response:
	response = &publicv1.ClustersCreateResponse{}
	response.SetObject(createdPublicCluster)
	return
}

func (s *ClustersServer) Update(ctx context.Context,
	request *publicv1.ClustersUpdateRequest) (response *publicv1.ClustersUpdateResponse, err error) {
	// Validate the request:
	publicCluster := request.GetObject()
	if publicCluster == nil {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument, "object is mandatory")
		return
	}
	id := publicCluster.GetId()
	if id == "" {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument, "object identifier is mandatory")
		return
	}

	// Check if the client sent back the redacted pull_secret sentinel ("***").
	// If so, strip it so it doesn't get stored. We'll preserve the existing value below.
	pullSecretWasRedacted := stripRedactedSecrets(publicCluster)

	// Determine how to prepare the private cluster based on whether there's a field mask. When there's a field mask,
	// we don't want to merge into the existing object because that would prevent proper replacement of map fields
	// (like node sets). Instead, we copy to a new object and let the generic server handle the merge with the
	// database object, which correctly applies field mask semantics.
	var privateCluster *privatev1.Cluster
	var existingPullSecret string
	updateMask := request.GetUpdateMask()
	if len(updateMask.GetPaths()) > 0 {
		privateCluster = &privatev1.Cluster{}
		privateCluster.SetId(id)
	} else {
		getRequest := &privatev1.ClustersGetRequest{}
		getRequest.SetId(id)
		var getResponse *privatev1.ClustersGetResponse
		getResponse, err = s.private.Get(ctx, getRequest)
		if err != nil {
			return nil, err
		}
		privateCluster = getResponse.GetObject()
		// Save the existing pull_secret before the mapper overwrites it
		if pullSecretWasRedacted && privateCluster.GetSpec().HasPullSecret() {
			existingPullSecret = privateCluster.GetSpec().GetPullSecret()
		}
	}
	err = s.inMapper.Copy(ctx, publicCluster, privateCluster)

	// Restore the pull_secret if it was stripped from the redacted sentinel
	if pullSecretWasRedacted && existingPullSecret != "" {
		privateCluster.GetSpec().SetPullSecret(existingPullSecret)
	}
	if err != nil {
		s.logger.ErrorContext(
			ctx,
			"Failed to map public cluster to private",
			slog.Any("error", err),
		)
		err = grpcstatus.Errorf(grpccodes.Internal, "failed to process cluster")
		return
	}

	// Delegate to the private server:
	privateRequest := &privatev1.ClustersUpdateRequest{}
	privateRequest.SetObject(privateCluster)
	privateRequest.SetUpdateMask(updateMask)
	privateRequest.SetLock(request.GetLock())
	privateResponse, err := s.private.Update(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	// Map the private response back to public format:
	updatedPrivateCluster := privateResponse.GetObject()
	updatedPublicCluster := &publicv1.Cluster{}
	err = s.outMapper.Copy(ctx, updatedPrivateCluster, updatedPublicCluster)
	if err != nil {
		s.logger.ErrorContext(
			ctx,
			"Failed to map private cluster to public",
			slog.Any("error", err),
		)
		err = grpcstatus.Errorf(grpccodes.Internal, "failed to process cluster")
		return
	}

	redactClusterSecrets(updatedPublicCluster)

	// Create the public response:
	response = &publicv1.ClustersUpdateResponse{}
	response.SetObject(updatedPublicCluster)
	return
}

func (s *ClustersServer) Delete(ctx context.Context,
	request *publicv1.ClustersDeleteRequest) (response *publicv1.ClustersDeleteResponse, err error) {
	// Create private request:
	privateRequest := &privatev1.ClustersDeleteRequest{}
	privateRequest.SetId(request.GetId())

	// Delegate to private server:
	_, err = s.private.Delete(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	// Create the public response:
	response = &publicv1.ClustersDeleteResponse{}
	return
}

func (s *ClustersServer) GetKubeconfig(ctx context.Context,
	request *publicv1.ClustersGetKubeconfigRequest) (response *publicv1.ClustersGetKubeconfigResponse, err error) {
	kubeconfig, err := s.getKubeconfig(ctx, request.Id)
	if err != nil {
		return
	}
	response = &publicv1.ClustersGetKubeconfigResponse{
		Kubeconfig: string(kubeconfig),
	}
	return
}

func (s *ClustersServer) GetKubeconfigViaHttp(ctx context.Context,
	request *publicv1.ClustersGetKubeconfigViaHttpRequest) (response *httpbody.HttpBody, err error) {
	kubeconfig, err := s.getKubeconfig(ctx, request.Id)
	if err != nil {
		return
	}
	response = &httpbody.HttpBody{
		ContentType: "application/yaml",
		Data:        kubeconfig,
	}
	return
}

func (s *ClustersServer) getKubeconfig(ctx context.Context, clusterId string) (result []byte, err error) {
	// Validate the request:
	if clusterId == "" {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument, "cluster identifier is mandatory")
		return
	}

	// Prepare a logger with additional information about the cluster:
	logger := s.logger.With(
		slog.String("cluster_id", clusterId),
	)

	// Prepare the error to return to the client if some internal error happens:
	internalErr := grpcstatus.Errorf(
		grpccodes.Internal,
		"failed to get kubeconfig for cluster with identifier '%s'",
		clusterId,
	)

	// Try to get the secret that contains the kubeconfig:
	secret, err := s.getHostedClusterSecret(ctx, clusterId, ".status.kubeconfig.name")
	if err != nil {
		logger.ErrorContext(
			ctx,
			"Failed to get hosted cluster secret",
			slog.Any("error", err),
		)
		err = internalErr
		return
	}
	if secret == nil {
		err = grpcstatus.Errorf(
			grpccodes.NotFound,
			"kubeconfig for cluster with identifier '%s' isn't yet available",
			clusterId,
		)
		return
	}

	// Check that the secret has the expected entry, and that it isn't empty:
	kcBytes, ok := secret.Data["kubeconfig"]
	if !ok {
		logger.ErrorContext(ctx, "Kubeconfig secret entry doesn't exist")
		err = internalErr
		return
	}
	if len(kcBytes) == 0 {
		logger.ErrorContext(ctx, "Kubeconfig secret entry is empty")
		err = internalErr
		return
	}

	// Done:
	logger.DebugContext(
		ctx,
		"Returning kubeconfig",
		slog.Int("kc_bytes", len(kcBytes)),
	)
	result = kcBytes
	return
}

func (s *ClustersServer) GetPassword(ctx context.Context,
	request *publicv1.ClustersGetPasswordRequest) (response *publicv1.ClustersGetPasswordResponse, err error) {
	password, err := s.getPassword(ctx, request.Id)
	if err != nil {
		return
	}
	response = &publicv1.ClustersGetPasswordResponse{
		Password: password,
	}
	return
}

func (s *ClustersServer) GetPasswordViaHttp(ctx context.Context,
	request *publicv1.ClustersGetPasswordViaHttpRequest) (response *httpbody.HttpBody, err error) {
	password, err := s.getPassword(ctx, request.Id)
	if err != nil {
		return
	}
	response = &httpbody.HttpBody{
		ContentType: "text/plain",
		Data:        []byte(password),
	}
	return
}

func (s *ClustersServer) getPassword(ctx context.Context, clusterId string) (result string, err error) {
	// Validate the request:
	if clusterId == "" {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument, "cluster identifier is mandatory")
		return
	}

	// Prepare a logger with additional information about the cluster:
	logger := s.logger.With(
		slog.String("cluster_id", clusterId),
	)

	// Prepare the error to return to the client if some internal error happens:
	internalErr := grpcstatus.Errorf(
		grpccodes.Internal,
		"failed to get password for cluster with identifier '%s'",
		clusterId,
	)

	// Try to get the secret that contains the password:
	secret, err := s.getHostedClusterSecret(ctx, clusterId, ".status.kubeadminPassword.name")
	if err != nil {
		s.logger.ErrorContext(
			ctx,
			"Failed to get hosted cluster",
			slog.Any("error", err),
		)
		err = internalErr
		return
	}
	if secret == nil {
		err = grpcstatus.Errorf(
			grpccodes.NotFound,
			"password for cluster with identifier '%s' isn't available yet",
			clusterId,
		)
		return
	}

	// Check that the secret has the expected entry, and that it isn't empty:
	passwordBytes, ok := secret.Data["password"]
	if !ok {
		logger.ErrorContext(ctx, "Password secret entry doesn't exist")
		err = internalErr
		return
	}
	if len(passwordBytes) == 0 {
		logger.ErrorContext(ctx, "Password secret entry is empty")
		err = internalErr
		return
	}

	// Done:
	logger.DebugContext(
		ctx,
		"Returning password",
		slog.Int("password_bytes", len(passwordBytes)),
	)
	result = string(passwordBytes)
	return
}

func (s *ClustersServer) getHostedClusterSecret(ctx context.Context, clusterId string,
	secretField string) (result *corev1.Secret, err error) {
	// Get the data of the cluster from the private server:
	getRequest := &privatev1.ClustersGetRequest{}
	getRequest.SetId(clusterId)
	getResponse, err := s.private.Get(ctx, getRequest)
	if err != nil {
		return
	}
	cluster := getResponse.GetObject()
	if cluster == nil || cluster.GetStatus().GetHub() == "" {
		return
	}

	// Get the data of the hub:
	getHubResponse, err := s.hubsDao.Get().
		SetId(cluster.GetStatus().GetHub()).
		Do(ctx)
	if err != nil {
		return
	}
	hub := getHubResponse.GetObject()
	if hub == nil {
		return
	}

	// Create a client for the hub:
	hubClient, err := s.getKubeClient(ctx, hub)
	if err != nil {
		return
	}

	// Get the cluster order from the hub:
	order, err := s.getKubeClusterOrder(ctx, hubClient, hub.GetSpec().GetNamespace(), cluster.GetId())
	if err != nil {
		return
	}

	// Extract the location of the hosted cluster:
	clusterRef := order.Status.ClusterReference
	if clusterRef == nil {
		err = fmt.Errorf("cluster order for '%s' has no cluster reference", cluster.GetId())
		return
	}
	hcKey := clnt.ObjectKey{
		Namespace: clusterRef.Namespace,
		Name:      clusterRef.HostedClusterName,
	}

	// Get the hosted cluster from the hub:
	hc, err := s.getKubeHostedCluster(ctx, hubClient, hcKey)
	if err != nil || hc == nil {
		return
	}

	// Extract the name of the secret from the hosted cluster:
	secretKey := clnt.ObjectKey{
		Namespace: hc.GetNamespace(),
	}
	err = s.jqTool.Evaluate(secretField, hc.Object, &secretKey.Name)
	if err != nil {
		return
	}

	// Get the secret from the hub:
	result, err = s.getKubeSecret(ctx, hubClient, secretKey)
	return
}

func (s *ClustersServer) getKubeClient(ctx context.Context, hub *privatev1.Hub) (result clnt.Client, err error) {
	s.kubeClientsLock.Lock()
	defer s.kubeClientsLock.Unlock()
	result, ok := s.kubeClients[hub.Id]
	if ok {
		return
	}
	result, err = s.createKubeClient(ctx, hub)
	if err != nil {
		return
	}
	s.kubeClients[hub.Id] = result
	return
}

func (s *ClustersServer) createKubeClient(ctx context.Context, hub *privatev1.Hub) (result clnt.Client, err error) {
	config, err := clientcmd.RESTConfigFromKubeConfig(hub.GetSpec().GetKubeconfig())
	if err != nil {
		return
	}
	result, err = clnt.New(config, clnt.Options{Scheme: s.scheme})
	return
}

func (s *ClustersServer) getKubeClusterOrder(ctx context.Context, client clnt.Client,
	namespace string, id string) (result *osacv1alpha1.ClusterOrder, err error) {
	list := &osacv1alpha1.ClusterOrderList{}
	err = client.List(
		ctx, list,
		clnt.InNamespace(namespace),
		clnt.MatchingLabels{
			labels.ClusterOrderUuid: id,
		},
	)
	if err != nil {
		return
	}
	items := list.Items
	if len(items) != 1 {
		err = fmt.Errorf(
			"expected exactly one cluster order with identifier '%s' but found %d",
			id, len(items),
		)
		return
	}
	result = &items[0]
	return
}

func (s *ClustersServer) getKubeHostedCluster(ctx context.Context, client clnt.Client,
	key clnt.ObjectKey) (result *unstructured.Unstructured, err error) {
	object := &unstructured.Unstructured{}
	object.SetGroupVersionKind(gvks.HostedCluster)
	err = client.Get(ctx, key, object)
	if apierrors.IsNotFound(err) {
		err = nil
		return
	}
	if err != nil {
		return
	}
	result = object
	return
}

func (s *ClustersServer) getKubeSecret(ctx context.Context, client clnt.Client,
	key clnt.ObjectKey) (result *corev1.Secret, err error) {
	object := &corev1.Secret{}
	err = client.Get(ctx, key, object)
	if apierrors.IsNotFound(err) {
		err = nil
		return
	}
	if err != nil {
		return
	}
	result = object
	return
}

// redactClusterSecrets clears sensitive fields from a public cluster before returning it to the client.
func redactClusterSecrets(cluster *publicv1.Cluster) {
	if cluster == nil || cluster.Spec == nil {
		return
	}
	if cluster.Spec.HasPullSecret() {
		cluster.Spec.SetPullSecret("***")
	}
}

// stripRedactedSecrets removes the redacted sentinel value from write-only fields
// so that an Update echoing back a GET response doesn't overwrite the stored secret.
// Returns true if a redacted value was stripped.
func stripRedactedSecrets(cluster *publicv1.Cluster) bool {
	if cluster == nil || cluster.Spec == nil {
		return false
	}
	if cluster.Spec.HasPullSecret() && cluster.Spec.GetPullSecret() == "***" {
		cluster.Spec.ClearPullSecret()
		return true
	}
	return false
}
