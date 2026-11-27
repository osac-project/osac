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
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	"github.com/prometheus/client_golang/prometheus"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

// PrivateNotificationSubscriptionsServerBuilder builds PrivateNotificationSubscriptionsServer.
type PrivateNotificationSubscriptionsServerBuilder struct {
	logger            *slog.Logger
	attributionLogic  auth.AttributionLogic
	tenancyLogic      auth.TenancyLogic
	metricsRegisterer prometheus.Registerer
	filterDesc        protoreflect.MessageDescriptor
	secrets           privatev1.SecretsServer
}

var _ privatev1.NotificationSubscriptionsServer = (*PrivateNotificationSubscriptionsServer)(nil)

// PrivateNotificationSubscriptionsServer implements private NotificationSubscriptions.
type PrivateNotificationSubscriptionsServer struct {
	privatev1.UnimplementedNotificationSubscriptionsServer

	logger  *slog.Logger
	generic *GenericServer[*privatev1.NotificationSubscription]
	secrets privatev1.SecretsServer
}

// NewPrivateNotificationSubscriptionsServer creates a builder.
func NewPrivateNotificationSubscriptionsServer() *PrivateNotificationSubscriptionsServerBuilder {
	return &PrivateNotificationSubscriptionsServerBuilder{}
}

// SetLogger sets the logger. Mandatory.
func (b *PrivateNotificationSubscriptionsServerBuilder) SetLogger(
	value *slog.Logger) *PrivateNotificationSubscriptionsServerBuilder {
	b.logger = value
	return b
}

// SetAttributionLogic sets attribution logic used on create.
func (b *PrivateNotificationSubscriptionsServerBuilder) SetAttributionLogic(
	value auth.AttributionLogic) *PrivateNotificationSubscriptionsServerBuilder {
	b.attributionLogic = value
	return b
}

// SetTenancyLogic sets tenancy logic. Mandatory.
func (b *PrivateNotificationSubscriptionsServerBuilder) SetTenancyLogic(
	value auth.TenancyLogic) *PrivateNotificationSubscriptionsServerBuilder {
	b.tenancyLogic = value
	return b
}

// SetMetricsRegisterer sets the metrics registerer.
func (b *PrivateNotificationSubscriptionsServerBuilder) SetMetricsRegisterer(
	value prometheus.Registerer) *PrivateNotificationSubscriptionsServerBuilder {
	b.metricsRegisterer = value
	return b
}

// SetFilterDesc sets the protobuf message descriptor used for CEL list filters.
func (b *PrivateNotificationSubscriptionsServerBuilder) SetFilterDesc(
	value protoreflect.MessageDescriptor) *PrivateNotificationSubscriptionsServerBuilder {
	b.filterDesc = value
	return b
}

// SetSecretsServer sets the private Secrets server used to store signing material.
func (b *PrivateNotificationSubscriptionsServerBuilder) SetSecretsServer(
	value privatev1.SecretsServer) *PrivateNotificationSubscriptionsServerBuilder {
	b.secrets = value
	return b
}

// Build creates the private notification subscriptions server.
func (b *PrivateNotificationSubscriptionsServerBuilder) Build() (
	result *PrivateNotificationSubscriptionsServer, err error) {
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}
	if b.tenancyLogic == nil {
		err = errors.New("tenancy logic is mandatory")
		return
	}
	if b.secrets == nil {
		err = errors.New("secrets server is mandatory")
		return
	}

	generic, err := NewGenericServer[*privatev1.NotificationSubscription]().
		SetLogger(b.logger).
		SetService(privatev1.NotificationSubscriptions_ServiceDesc.ServiceName).
		SetAttributionLogic(b.attributionLogic).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		SetFilterDesc(b.filterDesc).
		Build()
	if err != nil {
		return
	}
	result = &PrivateNotificationSubscriptionsServer{
		logger:  b.logger,
		generic: generic,
		secrets: b.secrets,
	}
	return
}

// List returns subscriptions visible under tenancy rules.
func (s *PrivateNotificationSubscriptionsServer) List(ctx context.Context,
	request *privatev1.NotificationSubscriptionsListRequest) (
	response *privatev1.NotificationSubscriptionsListResponse, err error) {
	err = s.generic.List(ctx, request, &response)
	return
}

// Get returns one subscription by id.
func (s *PrivateNotificationSubscriptionsServer) Get(ctx context.Context,
	request *privatev1.NotificationSubscriptionsGetRequest) (
	response *privatev1.NotificationSubscriptionsGetResponse, err error) {
	err = s.generic.Get(ctx, request, &response)
	return
}

// Create stores a subscription after CEL and SSRF validation, creates a tenant
// Secret for the signing material, and returns the plaintext secret once.
func (s *PrivateNotificationSubscriptionsServer) Create(ctx context.Context,
	request *privatev1.NotificationSubscriptionsCreateRequest) (
	response *privatev1.NotificationSubscriptionsCreateResponse, err error) {
	object := request.GetObject()
	if object == nil {
		return nil, grpcstatus.Error(grpccodes.InvalidArgument, "object is mandatory")
	}
	if err = validateSubscriptionCreate(object); err != nil {
		return nil, err
	}

	var plaintext string
	err = s.generic.CreateWithCandidatePreparation(ctx, request, &response,
		func(ctx context.Context, _, candidate *privatev1.NotificationSubscription) error {
			var createErr error
			plaintext, createErr = s.createSigningSecret(ctx, candidate)
			return createErr
		})
	if err != nil {
		return nil, err
	}
	if response != nil {
		response.SetSigningSecret(plaintext)
	}
	return
}

// Update changes mutable subscription fields (filter, webhook_url, paused).
func (s *PrivateNotificationSubscriptionsServer) Update(ctx context.Context,
	request *privatev1.NotificationSubscriptionsUpdateRequest) (
	response *privatev1.NotificationSubscriptionsUpdateResponse, err error) {
	object := request.GetObject()
	if object == nil {
		return nil, grpcstatus.Error(grpccodes.InvalidArgument, "object is mandatory")
	}
	id := object.GetId()
	if id == "" {
		return nil, grpcstatus.Error(grpccodes.InvalidArgument, "object identifier is mandatory")
	}
	getRequest := &privatev1.NotificationSubscriptionsGetRequest{}
	getRequest.SetId(id)
	var getResponse *privatev1.NotificationSubscriptionsGetResponse
	if err = s.generic.Get(ctx, getRequest, &getResponse); err != nil {
		return nil, err
	}
	existing := getResponse.GetObject()
	mask := request.GetUpdateMask()
	if mask == nil || len(mask.GetPaths()) == 0 {
		response = &privatev1.NotificationSubscriptionsUpdateResponse{}
		response.SetObject(existing)
		return response, nil
	}
	if err = s.validateUpdatePaths(object, mask); err != nil {
		return nil, err
	}
	merged := proto.Clone(existing).(*privatev1.NotificationSubscription)
	if err = applySubscriptionUpdate(merged, object, mask); err != nil {
		return nil, err
	}
	// Tenant Admin unpause (spec.paused=false) must also clear auto_paused. Public
	// Update uses withPublicAPIView, which restores status after the mask merge, so
	// clear in prepareCandidate after that restore — not on the pre-merge object.
	clearAutoPaused := updateIncludesField(mask, "spec.paused") &&
		merged.GetSpec() != nil && !merged.GetSpec().GetPaused()
	clientVersion := object.GetMetadata().GetVersion()
	request.SetObject(merged)
	if request.GetObject().GetMetadata() != nil {
		request.GetObject().GetMetadata().SetVersion(clientVersion)
	}
	err = s.generic.UpdateWithCandidatePreparation(ctx, request, &response,
		func(_ context.Context, _, candidate *privatev1.NotificationSubscription) error {
			if clearAutoPaused {
				if candidate.GetStatus() == nil {
					candidate.SetStatus(&privatev1.NotificationSubscriptionStatus{})
				}
				candidate.GetStatus().SetAutoPaused(false)
			}
			return nil
		})
	return
}

// Delete soft-deletes a subscription and best-effort deletes its signing Secret.
func (s *PrivateNotificationSubscriptionsServer) Delete(ctx context.Context,
	request *privatev1.NotificationSubscriptionsDeleteRequest) (
	response *privatev1.NotificationSubscriptionsDeleteResponse, err error) {
	getRequest := &privatev1.NotificationSubscriptionsGetRequest{}
	getRequest.SetId(request.GetId())
	var getResponse *privatev1.NotificationSubscriptionsGetResponse
	if getErr := s.generic.Get(ctx, getRequest, &getResponse); getErr == nil {
		ref := getResponse.GetObject().GetStatus().GetSigningSecret()
		err = s.generic.Delete(ctx, request, &response)
		if err != nil {
			return
		}
		s.deleteSigningSecret(ctx, ref)
		return
	}
	err = s.generic.Delete(ctx, request, &response)
	return
}

// Signal is unused for subscriptions.
func (s *PrivateNotificationSubscriptionsServer) Signal(ctx context.Context,
	request *privatev1.NotificationSubscriptionsSignalRequest) (
	response *privatev1.NotificationSubscriptionsSignalResponse, err error) {
	err = s.generic.Signal(ctx, request, &response)
	return
}

func validateSubscriptionCreate(object *privatev1.NotificationSubscription) error {
	spec := object.GetSpec()
	if spec == nil {
		return grpcstatus.Error(grpccodes.InvalidArgument, "object spec is mandatory")
	}
	if err := validateWebhookURL(spec.GetWebhookUrl()); err != nil {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "%s", err.Error())
	}
	if err := validateSubscriptionFilter(spec.GetFilter()); err != nil {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "%s", err.Error())
	}
	return nil
}

func (s *PrivateNotificationSubscriptionsServer) createSigningSecret(ctx context.Context,
	subscription *privatev1.NotificationSubscription) (plaintext string, err error) {
	plaintext, err = generateWebhookSigningSecret()
	if err != nil {
		return "", grpcstatus.Error(grpccodes.Internal, "failed to generate signing secret")
	}
	tenant := subscription.GetMetadata().GetTenant()
	name := signingSecretName(subscription.GetMetadata().GetName())
	secretResp, err := s.secrets.Create(ctx, privatev1.SecretsCreateRequest_builder{
		Object: privatev1.Secret_builder{
			Metadata: privatev1.Metadata_builder{
				Name:   name,
				Tenant: tenant,
			}.Build(),
			Type: privatev1.SecretType_SECRET_TYPE_VALUE,
			Data: map[string][]byte{
				"value": []byte(plaintext),
			},
		}.Build(),
	}.Build())
	if err != nil {
		return "", fmt.Errorf("create signing secret: %w", err)
	}
	created := secretResp.GetObject()
	status := subscription.GetStatus()
	if status == nil {
		status = &privatev1.NotificationSubscriptionStatus{}
		subscription.SetStatus(status)
	}
	status.SetSigningSecret(privatev1.SecretLocalReference_builder{
		Id:   created.GetId(),
		Name: created.GetMetadata().GetName(),
	}.Build())
	status.SetAutoPaused(false)
	return plaintext, nil
}

func (s *PrivateNotificationSubscriptionsServer) deleteSigningSecret(ctx context.Context,
	ref *privatev1.SecretLocalReference) {
	if ref == nil || (ref.GetId() == "" && ref.GetName() == "") {
		return
	}
	id := ref.GetId()
	if id == "" {
		req := &privatev1.SecretsListRequest{}
		req.SetFilter(fmt.Sprintf("this.metadata.name == %s", strconv.Quote(ref.GetName())))
		list, err := s.secrets.List(ctx, req)
		if err != nil || list.GetSize() == 0 {
			return
		}
		id = list.GetItems()[0].GetId()
	}
	_, err := s.secrets.Delete(ctx, privatev1.SecretsDeleteRequest_builder{Id: id}.Build())
	if err != nil {
		s.logger.ErrorContext(ctx, "Failed to delete subscription signing secret",
			slog.String("secret_id", id),
			slog.Any("error", err),
		)
	}
}

func (s *PrivateNotificationSubscriptionsServer) validateUpdatePaths(
	object *privatev1.NotificationSubscription, mask *fieldmaskpb.FieldMask) error {
	spec := object.GetSpec()
	for _, path := range mask.GetPaths() {
		switch normalizeFieldMaskPath(path) {
		case "spec.webhook_url":
			if spec == nil || spec.GetWebhookUrl() == "" {
				return grpcstatus.Error(grpccodes.InvalidArgument, "field 'spec.webhook_url' is required")
			}
			if err := validateWebhookURL(spec.GetWebhookUrl()); err != nil {
				return grpcstatus.Errorf(grpccodes.InvalidArgument, "%s", err.Error())
			}
		case "spec.filter":
			if spec == nil || spec.GetFilter() == "" {
				return grpcstatus.Error(grpccodes.InvalidArgument, "field 'spec.filter' is required")
			}
			if err := validateSubscriptionFilter(spec.GetFilter()); err != nil {
				return grpcstatus.Errorf(grpccodes.InvalidArgument, "%s", err.Error())
			}
		case "spec.paused", "status.last_delivery_status", "status.auto_paused":
			// Allowed.
		case "status.signing_secret":
			return grpcstatus.Error(grpccodes.InvalidArgument, "field 'status.signing_secret' is immutable")
		default:
			return grpcstatus.Errorf(grpccodes.InvalidArgument, "field '%s' is immutable", path)
		}
	}
	return nil
}

func applySubscriptionUpdate(merged, incoming *privatev1.NotificationSubscription,
	mask *fieldmaskpb.FieldMask) error {
	for _, path := range mask.GetPaths() {
		switch normalizeFieldMaskPath(path) {
		case "spec.webhook_url":
			merged.GetSpec().SetWebhookUrl(incoming.GetSpec().GetWebhookUrl())
		case "spec.filter":
			merged.GetSpec().SetFilter(incoming.GetSpec().GetFilter())
		case "spec.paused":
			merged.GetSpec().SetPaused(incoming.GetSpec().GetPaused())
		case "status.last_delivery_status":
			if merged.GetStatus() == nil {
				merged.SetStatus(&privatev1.NotificationSubscriptionStatus{})
			}
			merged.GetStatus().SetLastDeliveryStatus(incoming.GetStatus().GetLastDeliveryStatus())
		case "status.auto_paused":
			if merged.GetStatus() == nil {
				merged.SetStatus(&privatev1.NotificationSubscriptionStatus{})
			}
			merged.GetStatus().SetAutoPaused(incoming.GetStatus().GetAutoPaused())
		}
	}
	return nil
}

func generateWebhookSigningSecret() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return "whsec_" + base64.RawURLEncoding.EncodeToString(buf), nil
}

func signingSecretName(subscriptionName string) string {
	base := strings.ToLower(strings.TrimSpace(subscriptionName))
	if base == "" {
		base = "subscription"
	}
	base = strings.ReplaceAll(base, "_", "-")
	name := "whsec-" + base
	if len(name) > 63 {
		name = name[:63]
		name = strings.TrimRight(name, "-")
	}
	return name
}
