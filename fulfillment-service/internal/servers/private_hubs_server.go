/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package servers

import (
	"context"
	"errors"
	"log/slog"

	"github.com/prometheus/client_golang/prometheus"

	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/reflect/protoreflect"

	privatev1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/private/v1"
	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	"github.com/osac-project/osac/fulfillment-service/internal/events"
	"github.com/osac-project/osac/fulfillment-service/internal/references"
)

type PrivateHubsServerBuilder struct {
	logger            *slog.Logger
	notifier          events.Notifier
	attributionLogic  auth.AttributionLogic
	tenancyLogic      auth.TenancyLogic
	metricsRegisterer prometheus.Registerer
	filterDesc        protoreflect.MessageDescriptor
}

var _ privatev1.HubsServer = (*PrivateHubsServer)(nil)

type PrivateHubsServer struct {
	privatev1.UnimplementedHubsServer

	logger     *slog.Logger
	secretsDao *dao.GenericDAO[*privatev1.Secret]
	generic    *GenericServer[*privatev1.Hub]
}

func NewPrivateHubsServer() *PrivateHubsServerBuilder {
	return &PrivateHubsServerBuilder{}
}

func (b *PrivateHubsServerBuilder) SetLogger(value *slog.Logger) *PrivateHubsServerBuilder {
	b.logger = value
	return b
}

func (b *PrivateHubsServerBuilder) SetNotifier(value events.Notifier) *PrivateHubsServerBuilder {
	b.notifier = value
	return b
}

func (b *PrivateHubsServerBuilder) SetAttributionLogic(value auth.AttributionLogic) *PrivateHubsServerBuilder {
	b.attributionLogic = value
	return b
}

func (b *PrivateHubsServerBuilder) SetTenancyLogic(value auth.TenancyLogic) *PrivateHubsServerBuilder {
	b.tenancyLogic = value
	return b
}

// SetMetricsRegisterer sets the Prometheus registerer used to register the metrics for the underlying database
// access objects. This is optional. If not set, no metrics will be recorded.
func (b *PrivateHubsServerBuilder) SetMetricsRegisterer(value prometheus.Registerer) *PrivateHubsServerBuilder {
	b.metricsRegisterer = value
	return b
}

// SetFilterDesc sets the protobuf message descriptor used to validate and translate CEL filter
// expressions. This is optional. When unset, the descriptor of this server's own private message type is used.
func (b *PrivateHubsServerBuilder) SetFilterDesc(value protoreflect.MessageDescriptor) *PrivateHubsServerBuilder {
	b.filterDesc = value
	return b
}

func (b *PrivateHubsServerBuilder) Build() (result *PrivateHubsServer, err error) {
	// Check parameters:
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}
	if b.tenancyLogic == nil {
		err = errors.New("tenancy logic is mandatory")
		return
	}

	// Create the secrets DAO:
	secretsDao, err := dao.NewGenericDAO[*privatev1.Secret]().
		SetLogger(b.logger).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		Build()
	if err != nil {
		return
	}

	// Create the server early so that we can use its functions to set up other objects:
	s := &PrivateHubsServer{
		logger:     b.logger,
		secretsDao: secretsDao,
	}

	// Create the generic server:
	s.generic, err = NewGenericServer[*privatev1.Hub]().
		SetLogger(b.logger).
		SetService(privatev1.Hubs_ServiceDesc.ServiceName).
		SetNotifier(b.notifier).
		SetRedactFunc(s.redact).
		SetAttributionLogic(b.attributionLogic).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		SetFilterDesc(b.filterDesc).
		AddAllowedTenants(auth.SharedTenant).
		Build()
	if err != nil {
		return
	}

	// Return the server:
	result = s
	return
}

// redact clears sensitive fields from the hub before it is included in event notification payloads.
func (s *PrivateHubsServer) redact(object *privatev1.Hub) *privatev1.Hub {
	spec := object.GetSpec()
	if spec != nil {
		spec.SetKubeconfig(nil)
	}
	return object
}

func (s *PrivateHubsServer) List(ctx context.Context,
	request *privatev1.HubsListRequest) (response *privatev1.HubsListResponse, err error) {
	err = s.generic.List(ctx, request, &response)
	return
}

func (s *PrivateHubsServer) Get(ctx context.Context,
	request *privatev1.HubsGetRequest) (response *privatev1.HubsGetResponse, err error) {
	err = s.generic.Get(ctx, request, &response)
	return
}

func (s *PrivateHubsServer) Create(ctx context.Context,
	request *privatev1.HubsCreateRequest) (response *privatev1.HubsCreateResponse, err error) {
	if err = s.validateKubeconfigMutualExclusion(request.GetObject().GetSpec()); err != nil {
		return
	}
	if err = s.validateKubeconfigSecret(ctx, request.GetObject().GetSpec()); err != nil {
		return
	}
	err = s.generic.Create(ctx, request, &response)
	return
}

func (s *PrivateHubsServer) Update(ctx context.Context,
	request *privatev1.HubsUpdateRequest) (response *privatev1.HubsUpdateResponse, err error) {
	if err = s.validateKubeconfigMutualExclusionForUpdate(ctx, request); err != nil {
		return
	}
	if err = s.validateKubeconfigSecret(ctx, request.GetObject().GetSpec()); err != nil {
		return
	}
	err = s.generic.Update(ctx, request, &response)
	return
}

func (s *PrivateHubsServer) Delete(ctx context.Context,
	request *privatev1.HubsDeleteRequest) (response *privatev1.HubsDeleteResponse, err error) {
	err = s.generic.Delete(ctx, request, &response)
	return
}

func (s *PrivateHubsServer) Signal(ctx context.Context,
	request *privatev1.HubsSignalRequest) (response *privatev1.HubsSignalResponse, err error) {
	err = s.generic.Signal(ctx, request, &response)
	return
}

func hasInlineKubeconfig(spec *privatev1.HubSpec) bool {
	return spec != nil && len(spec.GetKubeconfig()) > 0
}

// validateKubeconfigMutualExclusion rejects specs that set both kubeconfig and kubeconfig_secret.
func (s *PrivateHubsServer) validateKubeconfigMutualExclusion(spec *privatev1.HubSpec) error {
	if spec == nil {
		return nil
	}
	if hasInlineKubeconfig(spec) && spec.GetKubeconfigSecret() != nil {
		return grpcstatus.Errorf(
			grpccodes.InvalidArgument,
			"kubeconfig and kubeconfig_secret are mutually exclusive",
		)
	}
	return nil
}

// validateKubeconfigMutualExclusionForUpdate checks for kubeconfig / kubeconfig_secret conflicts
// on Update, accounting for the update mask. When only one of the two fields is in the mask, the
// other retains its DB value, so a conflict can occur even if the request itself looks clean.
func (s *PrivateHubsServer) validateKubeconfigMutualExclusionForUpdate(
	ctx context.Context, request *privatev1.HubsUpdateRequest) error {
	spec := request.GetObject().GetSpec()
	if err := s.validateKubeconfigMutualExclusion(spec); err != nil {
		return err
	}

	mask := request.GetUpdateMask()
	// With a nil/empty mask the entire object is replaced, so no DB state to consider.
	if mask == nil || len(mask.GetPaths()) == 0 {
		return nil
	}

	settingKubeconfigSecret := spec.GetKubeconfigSecret() != nil &&
		updateIncludesField(mask, "spec.kubeconfig_secret")
	settingKubeconfig := hasInlineKubeconfig(spec) &&
		updateIncludesField(mask, "spec.kubeconfig")

	if !settingKubeconfigSecret && !settingKubeconfig {
		return nil
	}

	existing, found, err := s.getExistingHub(ctx, request)
	if err != nil {
		return err
	}
	if !found {
		return nil
	}
	existingSpec := existing.GetSpec()

	if settingKubeconfigSecret && hasInlineKubeconfig(existingSpec) &&
		!updateIncludesField(mask, "spec.kubeconfig") {
		return grpcstatus.Errorf(
			grpccodes.InvalidArgument,
			"kubeconfig and kubeconfig_secret are mutually exclusive",
		)
	}
	if settingKubeconfig && existingSpec.GetKubeconfigSecret() != nil &&
		!updateIncludesField(mask, "spec.kubeconfig_secret") {
		return grpcstatus.Errorf(
			grpccodes.InvalidArgument,
			"kubeconfig and kubeconfig_secret are mutually exclusive",
		)
	}
	return nil
}

func (s *PrivateHubsServer) validateKubeconfigSecret(ctx context.Context, spec *privatev1.HubSpec) error {
	if spec == nil {
		return nil
	}
	ref := spec.GetKubeconfigSecret()
	if ref == nil {
		return nil
	}
	if ref.GetId() == "" && ref.GetName() == "" {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "kubeconfig_secret must specify id or name")
	}
	resolved, err := references.NewDAOLookupFunc(s.secretsDao)(ctx, "", "", ref.GetId(), ref.GetName())
	if err != nil {
		var deniedErr *dao.ErrDenied
		if errors.As(err, &deniedErr) {
			return grpcstatus.Errorf(grpccodes.PermissionDenied, "%s", deniedErr.Reason)
		}
		var nf interface{ IsNotFound() bool }
		if errors.As(err, &nf) && nf.IsNotFound() {
			return grpcstatus.Errorf(grpccodes.InvalidArgument,
				"there is no secret with identifier or name '%s'", refKey(ref))
		}
		s.logger.ErrorContext(ctx, "Failed to resolve kubeconfig_secret reference", "error", err)
		return grpcstatus.Errorf(grpccodes.Internal, "failed to resolve kubeconfig_secret reference")
	}
	resolvedRef := &privatev1.SecretLocalReference{}
	resolvedRef.SetId(resolved.ID)
	resolvedRef.SetName(resolved.Name)
	spec.SetKubeconfigSecret(resolvedRef)
	return nil
}

func (s *PrivateHubsServer) getExistingHub(ctx context.Context,
	request *privatev1.HubsUpdateRequest) (*privatev1.Hub, bool, error) {
	hub := request.GetObject()
	if hub == nil {
		return nil, false, nil
	}
	id := hub.GetId()
	if id == "" {
		return nil, false, nil
	}
	getResponse, err := s.generic.dao.Get().
		SetId(id).
		Do(ctx)
	if err != nil {
		return nil, false, err
	}
	return getResponse.GetObject(), true, nil
}
