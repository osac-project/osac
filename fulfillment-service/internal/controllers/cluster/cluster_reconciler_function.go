/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package cluster

//go:generate mockgen -source=../../api/osac/private/v1/clusters_service_grpc.pb.go -destination=clusters_client_mock.go -package=cluster ClustersClient
//go:generate mockgen -source=../../api/osac/private/v1/cluster_versions_service_grpc.pb.go -destination=cluster_versions_client_mock.go -package=cluster ClusterVersionsClient

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"slices"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clnt "sigs.k8s.io/controller-runtime/pkg/client"

	osacv1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"

	privatev1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/private/v1"
	"github.com/osac-project/osac/fulfillment-service/internal/controllers"
	"github.com/osac-project/osac/fulfillment-service/internal/controllers/finalizers"
	"github.com/osac-project/osac/fulfillment-service/internal/kubernetes/annotations"
	"github.com/osac-project/osac/fulfillment-service/internal/kubernetes/labels"
	"github.com/osac-project/osac/fulfillment-service/internal/masks"
	"github.com/osac-project/osac/fulfillment-service/internal/utils"
)

// objectPrefix is the prefix that will be used in the `generateName` field of the resources created in the hub.
const objectPrefix = "order-"

// errClusterVersionNotFound indicates the requested ClusterVersion does not exist.
var errClusterVersionNotFound = errors.New("cluster version not found")

// FunctionBuilder contains the data and logic needed to build a function that reconciles clustes.
type FunctionBuilder struct {
	logger     *slog.Logger
	connection *grpc.ClientConn
	hubCache   controllers.HubCache
}

type function struct {
	logger                *slog.Logger
	hubCache              controllers.HubCache
	clustersClient        privatev1.ClustersClient
	hubsClient            privatev1.HubsClient
	clusterVersionsClient privatev1.ClusterVersionsClient
	maskCalculator        *masks.Calculator
}

type task struct {
	r            *function
	cluster      *privatev1.Cluster
	hubId        string
	hubNamespace string
	hubClient    clnt.Client
}

// NewFunction creates a new builder that can then be used to create a new cluster reconciler function.
func NewFunction() *FunctionBuilder {
	return &FunctionBuilder{}
}

// SetLogger sets the logger. This is mandatory.
func (b *FunctionBuilder) SetLogger(value *slog.Logger) *FunctionBuilder {
	b.logger = value
	return b
}

// SetConnection sets the gRPC client connection. This is mandatory.
func (b *FunctionBuilder) SetConnection(value *grpc.ClientConn) *FunctionBuilder {
	b.connection = value
	return b
}

// SetHubCache sets the cache of hubs. This is mandatory.
func (b *FunctionBuilder) SetHubCache(value controllers.HubCache) *FunctionBuilder {
	b.hubCache = value
	return b
}

// Build uses the information stored in the buidler to create a new cluster reconciler.
func (b *FunctionBuilder) Build() (result controllers.ReconcilerFunction[*privatev1.Cluster], err error) {
	// Check parameters:
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}
	if b.connection == nil {
		err = errors.New("client is mandatory")
		return
	}
	if b.hubCache == nil {
		err = errors.New("hub cache is mandatory")
		return
	}

	// Create and populate the object:
	object := &function{
		logger:                b.logger,
		clustersClient:        privatev1.NewClustersClient(b.connection),
		hubsClient:            privatev1.NewHubsClient(b.connection),
		clusterVersionsClient: privatev1.NewClusterVersionsClient(b.connection),
		hubCache:              b.hubCache,
		maskCalculator:        masks.NewCalculator().Build(),
	}
	result = object.run
	return
}

func (r *function) run(ctx context.Context, cluster *privatev1.Cluster) error {
	oldCluster := proto.Clone(cluster).(*privatev1.Cluster)
	t := task{
		r:       r,
		cluster: cluster,
	}
	var err error
	if cluster.GetMetadata().HasDeletionTimestamp() {
		err = t.delete(ctx)
	} else {
		err = t.update(ctx)
	}
	if err != nil {
		return err
	}
	// Calculate which fields the reconciler actually modified and use a field mask
	// to update only those fields. This prevents overwriting concurrent user changes
	// to fields like spec.node_sets.
	updateMask := r.maskCalculator.Calculate(oldCluster, cluster)

	// Only send an update if there are actual changes
	if len(updateMask.GetPaths()) > 0 {
		_, err = r.clustersClient.Update(ctx, privatev1.ClustersUpdateRequest_builder{
			Object:     cluster,
			UpdateMask: updateMask,
		}.Build())
	}
	return err
}

func (t *task) update(ctx context.Context) error {
	// Add the finalizer and return immediately if it was added. This ensures the finalizer is persisted before any
	// other work is done, reducing the chance of the object being deleted before the finalizer is saved.
	if t.addFinalizer() {
		return nil
	}

	// Set the default values:
	t.setDefaults()

	// Validate that exactly one tenant is assigned:
	if err := t.validateTenant(); err != nil {
		return err
	}

	// Do nothing if the cluster is in a terminal failure state. For both progressing and ready
	// clusters we need to sync the spec to the ClusterOrder, as the user may update node set
	// sizes on a ready cluster.
	state := t.cluster.GetStatus().GetState()
	if state != privatev1.ClusterState_CLUSTER_STATE_PROGRESSING &&
		state != privatev1.ClusterState_CLUSTER_STATE_READY {
		return nil
	}

	// Select the hub and return immediately if it was just selected. This ensures the hub is
	// persisted before any Kubernetes objects are created.
	hubJustSelected := t.cluster.GetStatus().GetHub() == ""
	err := t.selectHub(ctx)
	if err != nil {
		t.r.logger.ErrorContext(
			ctx,
			"Failed to select hub",
			slog.String("error", err.Error()),
		)
		t.updateCondition(
			privatev1.ClusterConditionType_CLUSTER_CONDITION_TYPE_PROGRESSING,
			privatev1.ConditionStatus_CONDITION_STATUS_FALSE,
			"ResourcesUnavailable",
			"The cluster cannot be created because there are no resources available to fulfill the "+
				"request.",
		)
		return nil
	}
	t.cluster.GetStatus().SetHub(t.hubId)
	if hubJustSelected {
		return nil
	}

	// Get the K8S object:
	object, err := t.getKubeObject(ctx)
	if err != nil {
		return err
	}

	// Prepare the changes to the spec:
	spec, err := t.buildSpec(ctx)
	if err != nil {
		if errors.Is(err, errClusterVersionNotFound) {
			t.r.logger.ErrorContext(
				ctx,
				"Cluster version not found",
				slog.String("error", err.Error()),
			)
			t.updateCondition(
				privatev1.ClusterConditionType_CLUSTER_CONDITION_TYPE_PROGRESSING,
				privatev1.ConditionStatus_CONDITION_STATUS_FALSE,
				"ResourcesUnavailable",
				fmt.Sprintf(
					"The cluster cannot be provisioned: %s.",
					err,
				),
			)
			return nil
		}
		return err
	}

	// Create or update the Kubernetes object:
	if object == nil {
		object := &osacv1alpha1.ClusterOrder{
			ObjectMeta: metav1.ObjectMeta{
				Namespace:    t.hubNamespace,
				GenerateName: objectPrefix,
				Labels: map[string]string{
					labels.ClusterOrderUuid: t.cluster.GetId(),
				},
				Annotations: map[string]string{
					annotations.Tenant: t.cluster.GetMetadata().GetTenant(),
				},
			},
			Spec: spec,
		}
		err = t.hubClient.Create(ctx, object)
		if err != nil {
			return controllers.HandleK8sWriteError(ctx, t.r.logger, err, t.setFailed)
		}
		t.r.logger.DebugContext(
			ctx,
			"Created cluster order",
			slog.String("namespace", object.GetNamespace()),
			slog.String("name", object.GetName()),
		)
	} else {
		update := object.DeepCopy()
		update.Spec = spec
		err = t.hubClient.Patch(ctx, update, clnt.MergeFrom(object))
		if err != nil {
			return controllers.HandleK8sWriteError(ctx, t.r.logger, err, t.setFailed)
		}
		t.r.logger.DebugContext(
			ctx,
			"Updated cluster order",
			slog.String("namespace", object.GetNamespace()),
			slog.String("name", object.GetName()),
		)
	}

	return err
}

func (t *task) setDefaults() {
	if !t.cluster.HasStatus() {
		t.cluster.SetStatus(&privatev1.ClusterStatus{})
	}
	if t.cluster.GetStatus().GetState() == privatev1.ClusterState_CLUSTER_STATE_UNSPECIFIED {
		t.cluster.GetStatus().SetState(privatev1.ClusterState_CLUSTER_STATE_PROGRESSING)
		t.cluster.GetStatus().SetStateTransitionTime(timestamppb.Now())
	}
	for value := range privatev1.ClusterConditionType_name {
		if value != 0 {
			t.setConditionDefaults(privatev1.ClusterConditionType(value))
		}
	}
}

func (t *task) setConditionDefaults(value privatev1.ClusterConditionType) {
	exists := false
	for _, current := range t.cluster.GetStatus().GetConditions() {
		if current.GetType() == value {
			exists = true
			break
		}
	}
	if !exists {
		conditions := t.cluster.GetStatus().GetConditions()
		conditions = append(conditions, privatev1.ClusterCondition_builder{
			Type:   value,
			Status: privatev1.ConditionStatus_CONDITION_STATUS_FALSE,
		}.Build())
		t.cluster.GetStatus().SetConditions(conditions)
	}
}

func (t *task) validateTenant() error {
	if !t.cluster.HasMetadata() || t.cluster.GetMetadata().GetTenant() == "" {
		return errors.New("Cluster must have a tenant assigned") //nolint:staticcheck // ST1005: Cluster is an API resource name
	}
	return nil
}

// buildSpec constructs the spec for the Kubernetes ClusterOrder object based on the
// cluster from the database.
func (t *task) buildSpec(ctx context.Context) (osacv1alpha1.ClusterOrderSpec, error) {
	templateParameters, err := utils.ConvertTemplateParametersToJSON(t.cluster.GetSpec().GetTemplateParameters())
	if err != nil {
		return osacv1alpha1.ClusterOrderSpec{}, err
	}
	spec := osacv1alpha1.ClusterOrderSpec{
		TemplateID:         controllers.RefKeyStr(t.cluster.GetSpec().GetTemplate()),
		TemplateParameters: templateParameters,
		NodeRequests:       t.prepareNodeRequests(),
	}

	// Add explicit spec fields if present:
	if err = t.addExplicitFields(ctx, &spec); err != nil {
		return osacv1alpha1.ClusterOrderSpec{}, err
	}

	return spec, nil
}

func (t *task) addExplicitFields(ctx context.Context, spec *osacv1alpha1.ClusterOrderSpec) error {
	clusterSpec := t.cluster.GetSpec()
	if clusterSpec.HasPullSecret() {
		spec.PullSecret = clusterSpec.GetPullSecret()
	}
	if clusterSpec.HasSshPublicKey() {
		spec.SSHPublicKey = clusterSpec.GetSshPublicKey()
	}
	// Resolve version reference to a release image via the ClusterVersion resource.
	if versionRef := clusterSpec.GetVersion(); versionRef != nil && versionRef.GetName() != "" {
		image, err := t.resolveVersionImage(ctx, versionRef.GetName())
		if err != nil {
			return err
		}
		spec.ReleaseImage = image
	}
	if clusterSpec.HasNetwork() {
		network := &osacv1alpha1.ClusterNetworkSpec{}
		hasFields := false
		if clusterSpec.GetNetwork().HasPodCidr() {
			network.PodCIDR = clusterSpec.GetNetwork().GetPodCidr()
			hasFields = true
		}
		if clusterSpec.GetNetwork().HasServiceCidr() {
			network.ServiceCIDR = clusterSpec.GetNetwork().GetServiceCidr()
			hasFields = true
		}
		if hasFields {
			spec.Network = network
		}
	}
	return nil
}

// resolveVersionImage looks up a ClusterVersion by name and returns its release image.
func (t *task) resolveVersionImage(ctx context.Context, versionName string) (string, error) {
	// metadata.name is unique per tenant, so at most one result is expected.
	response, err := t.r.clusterVersionsClient.List(ctx, privatev1.ClusterVersionsListRequest_builder{
		Filter: new(fmt.Sprintf(
			"this.metadata.name == %q", versionName,
		)),
		Limit: new(int32(1)),
	}.Build())
	if err != nil {
		return "", fmt.Errorf(
			"failed to look up cluster version '%s': %w",
			versionName, err,
		)
	}
	if len(response.GetItems()) == 0 {
		return "", fmt.Errorf(
			"%w: '%s'",
			errClusterVersionNotFound, versionName,
		)
	}
	if response.GetTotal() > 1 {
		t.r.logger.WarnContext(ctx, "Multiple cluster versions found for name, using first",
			"version_name", versionName,
			"count", response.GetTotal(),
		)
	}
	return response.GetItems()[0].GetSpec().GetImage(), nil
}

func (t *task) prepareNodeRequests() []osacv1alpha1.NodeRequest {
	nodeSets := t.cluster.GetSpec().GetNodeSets()
	keys := make([]string, 0, len(nodeSets))
	for key := range nodeSets {
		keys = append(keys, key)
	}
	slices.Sort(keys)

	nodeRequests := make([]osacv1alpha1.NodeRequest, 0, len(keys))
	for _, key := range keys {
		nodeRequests = append(nodeRequests, t.prepareNodeRequest(nodeSets[key]))
	}
	return nodeRequests
}

func (t *task) prepareNodeRequest(nodeSet *privatev1.ClusterNodeSet) osacv1alpha1.NodeRequest {
	return osacv1alpha1.NodeRequest{
		ResourceClass: controllers.RefKeyStr(nodeSet.GetHostType()),
		NumberOfNodes: int(nodeSet.GetSize()),
	}
}

func (t *task) delete(ctx context.Context) (err error) {
	// Do nothing if we don't know the hub yet:
	t.hubId = t.cluster.GetStatus().GetHub()
	if t.hubId == "" {
		return
	}
	err = t.getHub(ctx)
	if err != nil {
		// Check if the hub has been decommissioned (deleted from database)
		if errors.Is(err, controllers.ErrHubNotFound) {
			controllers.RemoveFinalizerOnDecommissionedHub(ctx, t.r.logger, t.hubId, "cluster_id", t.cluster.GetId(), t.removeFinalizer)
			return nil
		}
		// For transient errors (network, timeout, etc.), continue retrying
		return
	}

	// Delete the K8S object:
	object, err := t.getKubeObject(ctx)
	if err != nil {
		return
	}
	if object == nil {
		t.r.logger.DebugContext(
			ctx,
			"Cluster order doesn't exist",
			slog.String("id", t.cluster.GetId()),
		)
		t.removeFinalizer()
		return
	}
	err = t.hubClient.Delete(ctx, object)
	if err != nil {
		return
	}
	t.r.logger.DebugContext(
		ctx,
		"Deleted cluster order",
		slog.String("namespace", object.GetNamespace()),
		slog.String("name", object.GetName()),
	)

	t.removeFinalizer()
	return
}

func (t *task) selectHub(ctx context.Context) error {
	t.hubId = t.cluster.GetStatus().GetHub()
	if t.hubId == "" {
		response, err := t.r.hubsClient.List(ctx, privatev1.HubsListRequest_builder{}.Build())
		if err != nil {
			return err
		}
		if len(response.Items) == 0 {
			return errors.New("there are no hubs")
		}
		t.hubId = response.Items[rand.IntN(len(response.Items))].GetId()
	}
	t.r.logger.DebugContext(
		ctx,
		"Selected hub",
		slog.String("id", t.hubId),
	)
	hubEntry, err := t.r.hubCache.Get(ctx, t.hubId)
	if err != nil {
		return err
	}
	t.hubNamespace = hubEntry.Namespace
	t.hubClient = hubEntry.Client
	return nil
}

func (t *task) getHub(ctx context.Context) error {
	t.hubId = t.cluster.GetStatus().GetHub()
	hubEntry, err := t.r.hubCache.Get(ctx, t.hubId)
	if err != nil {
		return err
	}
	t.hubNamespace = hubEntry.Namespace
	t.hubClient = hubEntry.Client
	return nil
}

func (t *task) getKubeObject(ctx context.Context) (result *osacv1alpha1.ClusterOrder, err error) {
	list := &osacv1alpha1.ClusterOrderList{}
	err = t.hubClient.List(
		ctx, list,
		clnt.InNamespace(t.hubNamespace),
		clnt.MatchingLabels{
			labels.ClusterOrderUuid: t.cluster.GetId(),
		},
	)
	if err != nil {
		return
	}
	items := list.Items
	count := len(items)
	if count > 1 {
		err = fmt.Errorf(
			"expected at most one cluster order with identifier '%s' but found %d",
			t.cluster.GetId(), count,
		)
		return
	}
	if count > 0 {
		result = &items[0]
	}
	return
}

func (t *task) setFailed(err error) {
	if !t.cluster.HasStatus() {
		t.cluster.SetStatus(&privatev1.ClusterStatus{})
	}
	t.cluster.GetStatus().SetState(privatev1.ClusterState_CLUSTER_STATE_FAILED)
	t.updateCondition(
		privatev1.ClusterConditionType_CLUSTER_CONDITION_TYPE_PROGRESSING,
		privatev1.ConditionStatus_CONDITION_STATUS_FALSE,
		"ValidationFailed",
		err.Error(),
	)
}

// updateCondition updates or creates a condition with the specified type, status, reason, and message.
func (t *task) updateCondition(conditionType privatev1.ClusterConditionType, status privatev1.ConditionStatus,
	reason string, message string) {
	conditions := t.cluster.GetStatus().GetConditions()
	updated := false
	for i, condition := range conditions {
		if condition.GetType() == conditionType {
			conditions[i] = privatev1.ClusterCondition_builder{
				Type:    conditionType,
				Status:  status,
				Reason:  &reason,
				Message: &message,
			}.Build()
			updated = true
			break
		}
	}
	if !updated {
		conditions = append(conditions, privatev1.ClusterCondition_builder{
			Type:    conditionType,
			Status:  status,
			Reason:  &reason,
			Message: &message,
		}.Build())
	}
	t.cluster.GetStatus().SetConditions(conditions)
}

// addFinalizer adds the controller finalizer if it is not already present. Returns true if the finalizer was added,
// false if it was already present.
func (t *task) addFinalizer() bool {
	list := t.cluster.GetMetadata().GetFinalizers()
	if !slices.Contains(list, finalizers.Controller) {
		list = append(list, finalizers.Controller)
		t.cluster.GetMetadata().SetFinalizers(list)
		return true
	}
	return false
}

func (t *task) removeFinalizer() {
	list := t.cluster.GetMetadata().GetFinalizers()
	if slices.Contains(list, finalizers.Controller) {
		list = slices.DeleteFunc(list, func(item string) bool {
			return item == finalizers.Controller
		})
		t.cluster.GetMetadata().SetFinalizers(list)
	}
}
