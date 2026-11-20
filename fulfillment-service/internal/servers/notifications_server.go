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
	"log/slog"

	"github.com/prometheus/client_golang/prometheus"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

// NotificationsServerBuilder builds NotificationsServer instances.
type NotificationsServerBuilder struct {
	logger            *slog.Logger
	attributionLogic  auth.AttributionLogic
	tenancyLogic      auth.TenancyLogic
	metricsRegisterer prometheus.Registerer
	resourceAccess    NotificationResourceAccess
}

var _ publicv1.NotificationsServer = (*NotificationsServer)(nil)

// NotificationsServer implements the public Notifications gRPC service.
type NotificationsServer struct {
	publicv1.UnimplementedNotificationsServer

	logger         *slog.Logger
	delegate       privatev1.NotificationsServer
	inMapper       *GenericMapper[*publicv1.Notification, *privatev1.Notification]
	outMapper      *GenericMapper[*privatev1.Notification, *publicv1.Notification]
	resourceAccess NotificationResourceAccess
}

// NewNotificationsServer creates a builder for the public notifications server.
func NewNotificationsServer() *NotificationsServerBuilder {
	return &NotificationsServerBuilder{}
}

// SetLogger sets the logger. This is mandatory.
func (b *NotificationsServerBuilder) SetLogger(value *slog.Logger) *NotificationsServerBuilder {
	b.logger = value
	return b
}

// SetAttributionLogic sets attribution logic used on create.
func (b *NotificationsServerBuilder) SetAttributionLogic(value auth.AttributionLogic) *NotificationsServerBuilder {
	b.attributionLogic = value
	return b
}

// SetTenancyLogic sets tenancy logic. This is mandatory.
func (b *NotificationsServerBuilder) SetTenancyLogic(value auth.TenancyLogic) *NotificationsServerBuilder {
	b.tenancyLogic = value
	return b
}

// SetMetricsRegisterer sets the metrics registerer.
func (b *NotificationsServerBuilder) SetMetricsRegisterer(value prometheus.Registerer) *NotificationsServerBuilder {
	b.metricsRegisterer = value
	return b
}

// SetResourceAccess sets the resource visibility checker. This is optional. When
// omitted, Build installs the default DAO checker: Get for a single resource and
// List with this.id in [...] for a batch.
func (b *NotificationsServerBuilder) SetResourceAccess(value NotificationResourceAccess) *NotificationsServerBuilder {
	b.resourceAccess = value
	return b
}

// Build creates the public notifications server.
func (b *NotificationsServerBuilder) Build() (result *NotificationsServer, err error) {
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}
	if b.attributionLogic == nil {
		err = errors.New("attribution logic is mandatory")
		return
	}
	if b.tenancyLogic == nil {
		err = errors.New("tenancy logic is mandatory")
		return
	}

	inMapper, err := NewGenericMapper[*publicv1.Notification, *privatev1.Notification]().
		SetLogger(b.logger).
		SetStrict(true).
		Build()
	if err != nil {
		return
	}
	outMapper, err := NewGenericMapper[*privatev1.Notification, *publicv1.Notification]().
		SetLogger(b.logger).
		SetStrict(false).
		Build()
	if err != nil {
		return
	}

	delegate, err := NewPrivateNotificationsServer().
		SetLogger(b.logger).
		SetAttributionLogic(b.attributionLogic).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		SetFilterDesc((*publicv1.Notification)(nil).ProtoReflect().Descriptor()).
		Build()
	if err != nil {
		return
	}

	resourceAccess := b.resourceAccess
	if resourceAccess == nil {
		resourceAccess, err = newDAONotificationResourceAccess(b.logger, b.tenancyLogic, b.metricsRegisterer)
		if err != nil {
			return
		}
	}

	result = &NotificationsServer{
		logger:         b.logger,
		delegate:       delegate,
		inMapper:       inMapper,
		outMapper:      outMapper,
		resourceAccess: resourceAccess,
	}
	return
}

// List returns notifications visible under tenancy, recipient, and resource rules.
func (s *NotificationsServer) List(ctx context.Context,
	request *publicv1.NotificationsListRequest) (response *publicv1.NotificationsListResponse, err error) {
	privateRequest := &privatev1.NotificationsListRequest{}
	privateRequest.SetOffset(request.GetOffset())
	if request.HasLimit() {
		privateRequest.SetLimit(request.GetLimit())
	}
	privateRequest.SetFilter(request.GetFilter())
	privateRequest.SetOrder(request.GetOrder())

	privateResponse, err := s.delegate.List(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	privateItems := privateResponse.GetItems()
	candidates := make([]*privatev1.Notification, 0, len(privateItems))
	for _, privateItem := range privateItems {
		if !s.isRecipientVisible(ctx, privateItem) {
			continue
		}
		candidates = append(candidates, privateItem)
	}

	resources := make([]*privatev1.NotificationResourceReference, len(candidates))
	for i, candidate := range candidates {
		resources[i] = candidate.GetSpec().GetResource()
	}
	visibleFlags, visErr := s.resourceAccess.MayAccessMany(ctx, resources)
	if visErr != nil {
		s.logger.ErrorContext(ctx, "Notification resource visibility batch check failed",
			slog.Any("error", visErr),
		)
		visibleFlags = failClosedResourceFlags(resources)
	}
	if len(visibleFlags) != len(candidates) {
		s.logger.ErrorContext(ctx, "Notification resource visibility batch size mismatch",
			slog.Int("candidates", len(candidates)),
			slog.Int("flags", len(visibleFlags)),
		)
		visibleFlags = failClosedResourceFlags(resources)
	}

	publicItems := make([]*publicv1.Notification, 0, len(candidates))
	for i, privateItem := range candidates {
		if !visibleFlags[i] {
			continue
		}
		publicItem := &publicv1.Notification{}
		err = s.outMapper.Copy(ctx, privateItem, publicItem)
		if err != nil {
			s.logger.ErrorContext(
				ctx,
				"Failed to map private notification to public",
				slog.Any("error", err),
			)
			return nil, grpcstatus.Errorf(grpccodes.Internal, "failed to process notifications")
		}
		publicItems = append(publicItems, publicItem)
	}

	// Adjust total for dropped rows only when this page is the full result set.
	total := privateResponse.GetTotal()
	if request.GetOffset() <= 0 && len(privateItems) == int(total) {
		dropped := len(privateItems) - len(publicItems)
		total -= int32(dropped) // #nosec G115 -- dropped <= len(privateItems) == total
	}

	response = &publicv1.NotificationsListResponse{}
	response.SetSize(int32(len(publicItems))) // #nosec G115 -- bounded by page size
	response.SetTotal(total)
	response.SetItems(publicItems)
	return
}

// Get returns one notification by id.
func (s *NotificationsServer) Get(ctx context.Context,
	request *publicv1.NotificationsGetRequest) (response *publicv1.NotificationsGetResponse, err error) {
	privateRequest := &privatev1.NotificationsGetRequest{}
	privateRequest.SetId(request.GetId())

	privateResponse, err := s.delegate.Get(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	if err = s.authorizePublicRead(ctx, privateResponse.GetObject()); err != nil {
		return nil, err
	}

	publicNotification := &publicv1.Notification{}
	err = s.outMapper.Copy(ctx, privateResponse.GetObject(), publicNotification)
	if err != nil {
		s.logger.ErrorContext(
			ctx,
			"Failed to map private notification to public",
			slog.Any("error", err),
		)
		return nil, grpcstatus.Errorf(grpccodes.Internal, "failed to process notification")
	}

	response = &publicv1.NotificationsGetResponse{}
	response.SetObject(publicNotification)
	return
}

// Create stores an announcement. Event-driven inbox rows are private-API only.
func (s *NotificationsServer) Create(ctx context.Context,
	request *publicv1.NotificationsCreateRequest) (response *publicv1.NotificationsCreateResponse, err error) {
	publicNotification := request.GetObject()
	if publicNotification == nil {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument, "object is mandatory")
		return
	}
	if err = s.validatePublicCreate(ctx, publicNotification); err != nil {
		return
	}

	privateNotification := &privatev1.Notification{}
	err = s.inMapper.Copy(ctx, publicNotification, privateNotification)
	if err != nil {
		s.logger.ErrorContext(
			ctx,
			"Failed to map public notification to private",
			slog.Any("error", err),
		)
		err = publicInputMappingError(err, "failed to process notification")
		return
	}

	privateRequest := &privatev1.NotificationsCreateRequest{}
	privateRequest.SetObject(privateNotification)
	privateResponse, err := s.delegate.Create(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	createdPublic := &publicv1.Notification{}
	err = s.outMapper.Copy(ctx, privateResponse.GetObject(), createdPublic)
	if err != nil {
		s.logger.ErrorContext(
			ctx,
			"Failed to map private notification to public",
			slog.Any("error", err),
		)
		err = grpcstatus.Errorf(grpccodes.Internal, "failed to process notification")
		return
	}

	response = &publicv1.NotificationsCreateResponse{}
	response.SetObject(createdPublic)
	return
}

func (s *NotificationsServer) validatePublicCreate(ctx context.Context, object *publicv1.Notification) error {
	spec := object.GetSpec()
	if spec == nil {
		return grpcstatus.Error(grpccodes.InvalidArgument, "object spec is mandatory")
	}
	if spec.GetKind() != publicv1.NotificationKind_NOTIFICATION_KIND_ANNOUNCEMENT {
		return grpcstatus.Error(grpccodes.InvalidArgument, "public Create only accepts announcements")
	}
	if spec.GetScope() == publicv1.NotificationScope_NOTIFICATION_SCOPE_UNSPECIFIED {
		return grpcstatus.Error(grpccodes.InvalidArgument, "field 'spec.scope' is required for announcements")
	}
	if spec.GetScope() == publicv1.NotificationScope_NOTIFICATION_SCOPE_PLATFORM && auth.IsTenantAdmin(ctx) {
		return grpcstatus.Error(grpccodes.PermissionDenied, "tenant admins cannot create platform announcements")
	}
	return nil
}

// Update changes only allowed fields on inbox items (delegates mask enforcement to private).
func (s *NotificationsServer) Update(ctx context.Context,
	request *publicv1.NotificationsUpdateRequest) (response *publicv1.NotificationsUpdateResponse, err error) {
	ctx = withPublicAPIView(ctx)
	if err := validatePublicObjectMetadata(request.GetObject()); err != nil {
		return nil, err
	}

	publicNotification := request.GetObject()
	if publicNotification == nil {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument, "object is mandatory")
		return
	}
	id := publicNotification.GetId()
	if id == "" {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument, "object identifier is mandatory")
		return
	}

	getRequest := &privatev1.NotificationsGetRequest{}
	getRequest.SetId(id)
	getResponse, getErr := s.delegate.Get(ctx, getRequest)
	if getErr != nil {
		return nil, getErr
	}
	if err = s.authorizePublicRead(ctx, getResponse.GetObject()); err != nil {
		return nil, err
	}

	var privateNotification *privatev1.Notification
	updateMask := request.GetUpdateMask()
	if len(updateMask.GetPaths()) > 0 {
		privateNotification = &privatev1.Notification{}
		privateNotification.SetId(id)
	} else {
		privateNotification = getResponse.GetObject()
	}
	err = s.inMapper.Copy(ctx, publicNotification, privateNotification)
	if err != nil {
		s.logger.ErrorContext(
			ctx,
			"Failed to map public notification to private",
			slog.Any("error", err),
		)
		err = publicInputMappingError(err, "failed to process notification")
		return
	}

	privateRequest := &privatev1.NotificationsUpdateRequest{}
	privateRequest.SetObject(privateNotification)
	privateRequest.SetUpdateMask(updateMask)
	privateRequest.SetLock(request.GetLock())
	privateResponse, err := s.delegate.Update(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	updatedPublic := &publicv1.Notification{}
	err = s.outMapper.Copy(ctx, privateResponse.GetObject(), updatedPublic)
	if err != nil {
		s.logger.ErrorContext(
			ctx,
			"Failed to map private notification to public",
			slog.Any("error", err),
		)
		err = grpcstatus.Errorf(grpccodes.Internal, "failed to process notification")
		return
	}

	response = &publicv1.NotificationsUpdateResponse{}
	response.SetObject(updatedPublic)
	return
}

// Delete deletes a notification by id.
func (s *NotificationsServer) Delete(ctx context.Context,
	request *publicv1.NotificationsDeleteRequest) (response *publicv1.NotificationsDeleteResponse, err error) {
	id := request.GetId()
	if id == "" {
		return nil, grpcstatus.Error(grpccodes.InvalidArgument, "object identifier is mandatory")
	}

	getRequest := &privatev1.NotificationsGetRequest{}
	getRequest.SetId(id)
	getResponse, getErr := s.delegate.Get(ctx, getRequest)
	if getErr != nil {
		return nil, getErr
	}
	if err = s.authorizePublicDelete(ctx, getResponse.GetObject()); err != nil {
		return nil, err
	}

	privateRequest := &privatev1.NotificationsDeleteRequest{}
	privateRequest.SetId(id)
	_, err = s.delegate.Delete(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	response = &publicv1.NotificationsDeleteResponse{}
	return
}

// failClosedResourceFlags allows nil resources and denies everything else.
func failClosedResourceFlags(resources []*privatev1.NotificationResourceReference) []bool {
	flags := make([]bool, len(resources))
	for i, resource := range resources {
		flags[i] = resource == nil
	}
	return flags
}

// authorizePublicRead returns NotFound unless the caller may see the notification.
func (s *NotificationsServer) authorizePublicRead(ctx context.Context,
	notification *privatev1.Notification) error {
	if !s.isRecipientVisible(ctx, notification) {
		return grpcstatus.Error(grpccodes.NotFound, "object not found")
	}
	visible, visErr := s.isResourceVisible(ctx, notification)
	if visErr != nil {
		s.logger.ErrorContext(ctx, "Notification resource visibility check failed",
			slog.Any("error", visErr),
			slog.String("notification_id", notification.GetId()),
		)
		return grpcstatus.Error(grpccodes.NotFound, "object not found")
	}
	if !visible {
		return grpcstatus.Error(grpccodes.NotFound, "object not found")
	}
	return nil
}

// authorizePublicDelete allows announcement delete for admins. Inbox delete uses read rules.
func (s *NotificationsServer) authorizePublicDelete(ctx context.Context,
	notification *privatev1.Notification) error {
	spec := notification.GetSpec()
	if spec.GetKind() == privatev1.NotificationKind_NOTIFICATION_KIND_ANNOUNCEMENT {
		if !s.canManageAnnouncement(ctx, spec.GetScope()) {
			return grpcstatus.Error(grpccodes.PermissionDenied, "announcement delete is not allowed")
		}
	}
	return s.authorizePublicRead(ctx, notification)
}

// canManageAnnouncement reports whether the caller may delete an announcement of the given scope.
func (s *NotificationsServer) canManageAnnouncement(ctx context.Context,
	scope privatev1.NotificationScope) bool {
	subject := auth.SubjectFromContext(ctx)
	isPlatformAdmin := !subject.Tenants.Finite()
	if scope == privatev1.NotificationScope_NOTIFICATION_SCOPE_PLATFORM {
		return isPlatformAdmin
	}
	return isPlatformAdmin || auth.IsTenantAdmin(ctx)
}

// isRecipientVisible reports whether the caller may see this inbox row or announcement.
func (s *NotificationsServer) isRecipientVisible(ctx context.Context,
	notification *privatev1.Notification) bool {
	spec := notification.GetSpec()
	if spec.GetKind() == privatev1.NotificationKind_NOTIFICATION_KIND_ANNOUNCEMENT {
		return true
	}
	if s.callerSeesAllInbox(ctx) {
		return true
	}
	return spec.GetRecipientId() == auth.SubjectFromContext(ctx).User
}

// callerSeesAllInbox reports whether the caller may see every inbox row in their tenancy.
func (s *NotificationsServer) callerSeesAllInbox(ctx context.Context) bool {
	subject := auth.SubjectFromContext(ctx)
	if !subject.Tenants.Finite() {
		return true
	}
	return auth.IsTenantAdmin(ctx)
}

// isResourceVisible reports whether the caller may access the notification's linked resource.
func (s *NotificationsServer) isResourceVisible(ctx context.Context,
	notification *privatev1.Notification) (bool, error) {
	resource := notification.GetSpec().GetResource()
	if resource == nil {
		return true, nil
	}
	return s.resourceAccess.MayAccess(ctx, resource)
}
