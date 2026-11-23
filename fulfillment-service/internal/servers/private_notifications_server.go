/*
Copyright (c) 2026 Red Hat Inc.

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
	"regexp"

	"github.com/prometheus/client_golang/prometheus"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

// notificationKindSegments maps NotificationKind to the <kind> segment of
// osac.notification.<kind>.<name>.
var notificationKindSegments = map[privatev1.NotificationKind]string{
	privatev1.NotificationKind_NOTIFICATION_KIND_EVENT:        "event",
	privatev1.NotificationKind_NOTIFICATION_KIND_ANNOUNCEMENT: "announcement",
}

// notificationTypePattern is osac.notification.<kind>.<name> with a nonempty
// lowercase name of letters, digits, and underscores.
var notificationTypePattern = regexp.MustCompile(`^osac\.notification\.([a-z]+)\.([a-z0-9_]+)$`)

// PrivateNotificationsServerBuilder builds PrivateNotificationsServer instances.
type PrivateNotificationsServerBuilder struct {
	logger            *slog.Logger
	attributionLogic  auth.AttributionLogic
	tenancyLogic      auth.TenancyLogic
	metricsRegisterer prometheus.Registerer
	filterDesc        protoreflect.MessageDescriptor
}

var _ privatev1.NotificationsServer = (*PrivateNotificationsServer)(nil)

// PrivateNotificationsServer implements the private Notifications gRPC service.
type PrivateNotificationsServer struct {
	privatev1.UnimplementedNotificationsServer

	generic *GenericServer[*privatev1.Notification]
}

// NewPrivateNotificationsServer creates a builder for the private notifications server.
func NewPrivateNotificationsServer() *PrivateNotificationsServerBuilder {
	return &PrivateNotificationsServerBuilder{}
}

// SetLogger sets the logger. This is mandatory.
func (b *PrivateNotificationsServerBuilder) SetLogger(value *slog.Logger) *PrivateNotificationsServerBuilder {
	b.logger = value
	return b
}

// SetAttributionLogic sets attribution logic used on create.
func (b *PrivateNotificationsServerBuilder) SetAttributionLogic(value auth.AttributionLogic) *PrivateNotificationsServerBuilder {
	b.attributionLogic = value
	return b
}

// SetTenancyLogic sets tenancy logic. This is mandatory.
func (b *PrivateNotificationsServerBuilder) SetTenancyLogic(value auth.TenancyLogic) *PrivateNotificationsServerBuilder {
	b.tenancyLogic = value
	return b
}

// SetMetricsRegisterer sets the metrics registerer.
func (b *PrivateNotificationsServerBuilder) SetMetricsRegisterer(value prometheus.Registerer) *PrivateNotificationsServerBuilder {
	b.metricsRegisterer = value
	return b
}

// SetFilterDesc sets the protobuf message descriptor used for CEL filters.
func (b *PrivateNotificationsServerBuilder) SetFilterDesc(value protoreflect.MessageDescriptor) *PrivateNotificationsServerBuilder {
	b.filterDesc = value
	return b
}

// Build creates the private notifications server.
func (b *PrivateNotificationsServerBuilder) Build() (result *PrivateNotificationsServer, err error) {
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}
	if b.tenancyLogic == nil {
		err = errors.New("tenancy logic is mandatory")
		return
	}

	generic, err := NewGenericServer[*privatev1.Notification]().
		SetLogger(b.logger).
		SetService(privatev1.Notifications_ServiceDesc.ServiceName).
		SetAttributionLogic(b.attributionLogic).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		SetFilterDesc(b.filterDesc).
		AddAllowedTenants(auth.SystemTenant).
		Build()
	if err != nil {
		return
	}
	result = &PrivateNotificationsServer{generic: generic}
	return
}

// List returns notifications visible under tenancy rules.
func (s *PrivateNotificationsServer) List(ctx context.Context,
	request *privatev1.NotificationsListRequest) (response *privatev1.NotificationsListResponse, err error) {
	err = s.generic.List(ctx, request, &response)
	return
}

// Get returns one notification by id.
func (s *PrivateNotificationsServer) Get(ctx context.Context,
	request *privatev1.NotificationsGetRequest) (response *privatev1.NotificationsGetResponse, err error) {
	err = s.generic.Get(ctx, request, &response)
	return
}

// Create stores a notification after validating kind/severity and inbox or announcement rules.
func (s *PrivateNotificationsServer) Create(ctx context.Context,
	request *privatev1.NotificationsCreateRequest) (response *privatev1.NotificationsCreateResponse, err error) {
	if request.GetObject() == nil {
		return nil, grpcstatus.Error(grpccodes.InvalidArgument, "object is mandatory")
	}
	if err = s.validateCreate(request.GetObject()); err != nil {
		return nil, err
	}
	// Apply ownership and state defaults on the request before GenericServer assigns
	// creator/tenant. Candidate preparation cannot change tenant afterward.
	s.applyCreateDefaults(request.GetObject())
	err = s.generic.Create(ctx, request, &response)
	return
}

func (s *PrivateNotificationsServer) validateCreate(object *privatev1.Notification) error {
	spec := object.GetSpec()
	if spec == nil {
		return grpcstatus.Error(grpccodes.InvalidArgument, "object spec is mandatory")
	}
	if spec.GetKind() == privatev1.NotificationKind_NOTIFICATION_KIND_UNSPECIFIED {
		return grpcstatus.Error(grpccodes.InvalidArgument, "field 'spec.kind' is required")
	}
	if spec.GetSeverity() == privatev1.NotificationSeverity_NOTIFICATION_SEVERITY_UNSPECIFIED {
		return grpcstatus.Error(grpccodes.InvalidArgument, "field 'spec.severity' is required")
	}
	if spec.GetTitle() == "" {
		return grpcstatus.Error(grpccodes.InvalidArgument, "field 'spec.title' is required")
	}
	if err := validateNotificationType(spec.GetKind(), spec.GetType()); err != nil {
		return err
	}

	if spec.GetKind() == privatev1.NotificationKind_NOTIFICATION_KIND_ANNOUNCEMENT {
		if spec.GetScope() == privatev1.NotificationScope_NOTIFICATION_SCOPE_UNSPECIFIED {
			return grpcstatus.Error(grpccodes.InvalidArgument, "field 'spec.scope' is required for announcements")
		}
		if spec.GetRecipientId() != "" {
			return grpcstatus.Error(grpccodes.InvalidArgument, "announcements must not set spec.recipient_id")
		}
		if spec.GetEvent() != nil && (spec.GetEvent().GetId() != "" || spec.GetEvent().GetSource() != "") {
			return grpcstatus.Error(grpccodes.InvalidArgument, "announcements must not set spec.event")
		}
		return nil
	}

	if spec.GetRecipientId() == "" {
		return grpcstatus.Error(grpccodes.InvalidArgument, "field 'spec.recipient_id' is required for inbox notifications")
	}
	if spec.GetEvent() == nil || spec.GetEvent().GetId() == "" || spec.GetEvent().GetSource() == "" {
		return grpcstatus.Error(grpccodes.InvalidArgument, "field 'spec.event' with id and source is required for inbox notifications")
	}
	return nil
}

func notificationKindSegment(kind privatev1.NotificationKind) (string, error) {
	segment, ok := notificationKindSegments[kind]
	if !ok {
		return "", fmt.Errorf("unsupported notification kind %v", kind)
	}
	return segment, nil
}

// validateNotificationType requires osac.notification.<kind>.<name> with <kind>
// matching spec.kind and <name> nonempty [a-z0-9_]+.
func validateNotificationType(kind privatev1.NotificationKind, typeValue string) error {
	if typeValue == "" {
		return grpcstatus.Error(grpccodes.InvalidArgument, "field 'spec.type' is required")
	}
	kindSegment, err := notificationKindSegment(kind)
	if err != nil {
		return grpcstatus.Error(grpccodes.InvalidArgument, "field 'spec.kind' is required")
	}
	matches := notificationTypePattern.FindStringSubmatch(typeValue)
	if matches == nil {
		return grpcstatus.Error(grpccodes.InvalidArgument,
			"field 'spec.type' must match osac.notification.<kind>.<name>")
	}
	if matches[1] != kindSegment {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"field 'spec.type' kind segment must be %q", kindSegment)
	}
	return nil
}

// applyCreateDefaults sets platform announcement tenant and inbox unread state on the
// request object before GenericServer create preparation.
func (s *PrivateNotificationsServer) applyCreateDefaults(object *privatev1.Notification) {
	spec := object.GetSpec()
	if spec.GetKind() == privatev1.NotificationKind_NOTIFICATION_KIND_ANNOUNCEMENT {
		if spec.GetScope() == privatev1.NotificationScope_NOTIFICATION_SCOPE_PLATFORM {
			metadata := object.GetMetadata()
			if metadata == nil {
				metadata = &privatev1.Metadata{}
				object.SetMetadata(metadata)
			}
			metadata.SetTenant(auth.SystemTenant)
		}
		return
	}
	if spec.GetState() == privatev1.NotificationState_NOTIFICATION_STATE_UNSPECIFIED {
		spec.SetState(privatev1.NotificationState_NOTIFICATION_STATE_UNREAD)
	}
}

// Update changes only allowed fields.
func (s *PrivateNotificationsServer) Update(ctx context.Context,
	request *privatev1.NotificationsUpdateRequest) (response *privatev1.NotificationsUpdateResponse, err error) {
	id := request.GetObject().GetId()
	if id == "" {
		return nil, grpcstatus.Error(grpccodes.InvalidArgument, "object identifier is mandatory")
	}

	getRequest := &privatev1.NotificationsGetRequest{}
	getRequest.SetId(id)
	var getResponse *privatev1.NotificationsGetResponse
	if err = s.generic.Get(ctx, getRequest, &getResponse); err != nil {
		return nil, err
	}
	existing := getResponse.GetObject()
	if existing.GetSpec().GetKind() == privatev1.NotificationKind_NOTIFICATION_KIND_ANNOUNCEMENT {
		return nil, grpcstatus.Error(grpccodes.InvalidArgument, "announcements do not support update")
	}

	mask := request.GetUpdateMask()
	if mask == nil || len(mask.GetPaths()) == 0 {
		return nil, grpcstatus.Error(grpccodes.InvalidArgument, "update_mask is mandatory")
	}
	for _, path := range mask.GetPaths() {
		if normalizeFieldMaskPath(path) != "spec.state" {
			return nil, grpcstatus.Errorf(grpccodes.InvalidArgument, "field '%s' is immutable", path)
		}
	}

	merged := proto.Clone(existing).(*privatev1.Notification)
	if request.GetObject().GetSpec() != nil {
		merged.GetSpec().SetState(request.GetObject().GetSpec().GetState())
	}
	if merged.GetSpec().GetState() == privatev1.NotificationState_NOTIFICATION_STATE_UNSPECIFIED {
		return nil, grpcstatus.Error(grpccodes.InvalidArgument, "field 'spec.state' is required")
	}

	clientVersion := request.GetObject().GetMetadata().GetVersion()
	request.SetObject(merged)
	request.SetUpdateMask(&fieldmaskpb.FieldMask{Paths: []string{"spec.state"}})
	if request.GetObject().GetMetadata() != nil {
		request.GetObject().GetMetadata().SetVersion(clientVersion)
	}

	err = s.generic.Update(ctx, request, &response)
	return
}

// Delete deletes a notification by id.
func (s *PrivateNotificationsServer) Delete(ctx context.Context,
	request *privatev1.NotificationsDeleteRequest) (response *privatev1.NotificationsDeleteResponse, err error) {
	err = s.generic.Delete(ctx, request, &response)
	return
}

// Signal indicates that reconciliation may be required.
func (s *PrivateNotificationsServer) Signal(ctx context.Context,
	request *privatev1.NotificationsSignalRequest) (response *privatev1.NotificationsSignalResponse, err error) {
	err = s.generic.Signal(ctx, request, &response)
	return
}
