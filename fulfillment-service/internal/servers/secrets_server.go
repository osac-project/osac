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

	privatev1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/private/v1"
	publicv1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/public/v1"
	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/events"
	"github.com/osac-project/osac/fulfillment-service/internal/vault"
)

type SecretsServerBuilder struct {
	logger            *slog.Logger
	notifier          events.Notifier
	attributionLogic  auth.AttributionLogic
	tenancyLogic      auth.TenancyLogic
	metricsRegisterer prometheus.Registerer
	secretStore       vault.SecretStore
}

var _ publicv1.SecretsServer = (*SecretsServer)(nil)

type SecretsServer struct {
	publicv1.UnimplementedSecretsServer

	logger    *slog.Logger
	private   privatev1.SecretsServer
	inMapper  *GenericMapper[*publicv1.Secret, *privatev1.Secret]
	outMapper *GenericMapper[*privatev1.Secret, *publicv1.Secret]
}

func NewSecretsServer() *SecretsServerBuilder {
	return &SecretsServerBuilder{}
}

func (b *SecretsServerBuilder) SetLogger(value *slog.Logger) *SecretsServerBuilder {
	b.logger = value
	return b
}

func (b *SecretsServerBuilder) SetNotifier(value events.Notifier) *SecretsServerBuilder {
	b.notifier = value
	return b
}

func (b *SecretsServerBuilder) SetAttributionLogic(value auth.AttributionLogic) *SecretsServerBuilder {
	b.attributionLogic = value
	return b
}

func (b *SecretsServerBuilder) SetTenancyLogic(value auth.TenancyLogic) *SecretsServerBuilder {
	b.tenancyLogic = value
	return b
}

func (b *SecretsServerBuilder) SetMetricsRegisterer(value prometheus.Registerer) *SecretsServerBuilder {
	b.metricsRegisterer = value
	return b
}

func (b *SecretsServerBuilder) SetSecretStore(value vault.SecretStore) *SecretsServerBuilder {
	b.secretStore = value
	return b
}

func (b *SecretsServerBuilder) Build() (result *SecretsServer, err error) {
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}
	if b.tenancyLogic == nil {
		err = errors.New("tenancy logic is mandatory")
		return
	}

	inMapper, err := NewGenericMapper[*publicv1.Secret, *privatev1.Secret]().
		SetLogger(b.logger).
		SetStrict(true).
		Build()
	if err != nil {
		return
	}
	outMapper, err := NewGenericMapper[*privatev1.Secret, *publicv1.Secret]().
		SetLogger(b.logger).
		SetStrict(false).
		Build()
	if err != nil {
		return
	}

	delegate, err := NewPrivateSecretsServer().
		SetLogger(b.logger).
		SetNotifier(b.notifier).
		SetAttributionLogic(b.attributionLogic).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		SetSecretStore(b.secretStore).
		Build()
	if err != nil {
		return
	}

	result = &SecretsServer{
		logger:    b.logger,
		private:   delegate,
		inMapper:  inMapper,
		outMapper: outMapper,
	}
	return
}

func (s *SecretsServer) redactPublicSecret(secret *publicv1.Secret) {
	secret.SetData(nil)
}

func (s *SecretsServer) Create(ctx context.Context,
	request *publicv1.SecretsCreateRequest) (response *publicv1.SecretsCreateResponse, err error) {
	privateObject := &privatev1.Secret{}
	err = s.inMapper.Copy(ctx, request.GetObject(), privateObject)
	if err != nil {
		s.logger.ErrorContext(ctx, "Failed to map public secret to private", slog.Any("error", err))
		return nil, err
	}

	// Public API always uses the Vault backend.
	privateObject.SetBackend(privatev1.SecretBackend_SECRET_BACKEND_VAULT)

	privateRequest := &privatev1.SecretsCreateRequest{}
	privateRequest.SetObject(privateObject)

	privateResponse, err := s.private.Create(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	publicObject := &publicv1.Secret{}
	err = s.outMapper.Copy(ctx, privateResponse.GetObject(), publicObject)
	if err != nil {
		s.logger.ErrorContext(ctx, "Failed to map private secret to public", slog.Any("error", err))
		return nil, err
	}
	s.redactPublicSecret(publicObject)

	response = &publicv1.SecretsCreateResponse{}
	response.SetObject(publicObject)
	return
}

func (s *SecretsServer) List(ctx context.Context,
	request *publicv1.SecretsListRequest) (response *publicv1.SecretsListResponse, err error) {
	privateRequest := &privatev1.SecretsListRequest{}
	privateRequest.SetOffset(request.GetOffset())
	privateRequest.SetLimit(request.GetLimit())
	privateRequest.SetFilter(request.GetFilter())
	privateRequest.SetOrder(request.GetOrder())

	privateResponse, err := s.private.List(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	privateItems := privateResponse.GetItems()
	publicItems := make([]*publicv1.Secret, len(privateItems))
	for i, privateItem := range privateItems {
		publicItem := &publicv1.Secret{}
		err = s.outMapper.Copy(ctx, privateItem, publicItem)
		if err != nil {
			s.logger.ErrorContext(ctx, "Failed to map private secret to public", slog.Any("error", err))
			return nil, err
		}
		s.redactPublicSecret(publicItem)
		publicItems[i] = publicItem
	}

	response = &publicv1.SecretsListResponse{}
	response.SetSize(privateResponse.GetSize())
	response.SetTotal(privateResponse.GetTotal())
	response.SetItems(publicItems)
	return
}

func (s *SecretsServer) Get(ctx context.Context,
	request *publicv1.SecretsGetRequest) (response *publicv1.SecretsGetResponse, err error) {
	privateRequest := &privatev1.SecretsGetRequest{}
	privateRequest.SetId(request.GetId())

	privateResponse, err := s.private.Get(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	publicObject := &publicv1.Secret{}
	err = s.outMapper.Copy(ctx, privateResponse.GetObject(), publicObject)
	if err != nil {
		s.logger.ErrorContext(ctx, "Failed to map private secret to public", slog.Any("error", err))
		return nil, err
	}

	response = &publicv1.SecretsGetResponse{}
	response.SetObject(publicObject)
	return
}

func (s *SecretsServer) Update(ctx context.Context,
	request *publicv1.SecretsUpdateRequest) (response *publicv1.SecretsUpdateResponse, err error) {
	id := request.GetObject().GetId()

	getRequest := &privatev1.SecretsGetRequest{}
	getRequest.SetId(id)
	getResponse, err := s.private.Get(ctx, getRequest)
	if err != nil {
		return nil, err
	}

	existing := getResponse.GetObject()
	if existing.GetBackend() == privatev1.SecretBackend_SECRET_BACKEND_HUB &&
		len(request.GetObject().GetData()) > 0 {
		return nil, grpcstatus.Errorf(grpccodes.FailedPrecondition,
			"cannot update data for hub-backed secrets via public API")
	}

	privateObject := &privatev1.Secret{}
	err = s.inMapper.Copy(ctx, request.GetObject(), privateObject)
	if err != nil {
		s.logger.ErrorContext(ctx, "Failed to map public secret to private", slog.Any("error", err))
		return nil, err
	}

	// Preserve the existing backend (public API has no backend field).
	privateObject.SetBackend(existing.GetBackend())

	privateRequest := &privatev1.SecretsUpdateRequest{}
	privateRequest.SetObject(privateObject)
	privateRequest.SetUpdateMask(request.GetUpdateMask())
	privateRequest.SetLock(request.GetLock())

	privateResponse, err := s.private.Update(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	publicObject := &publicv1.Secret{}
	err = s.outMapper.Copy(ctx, privateResponse.GetObject(), publicObject)
	if err != nil {
		s.logger.ErrorContext(ctx, "Failed to map private secret to public", slog.Any("error", err))
		return nil, err
	}
	s.redactPublicSecret(publicObject)

	response = &publicv1.SecretsUpdateResponse{}
	response.SetObject(publicObject)
	return
}

func (s *SecretsServer) Delete(ctx context.Context,
	request *publicv1.SecretsDeleteRequest) (response *publicv1.SecretsDeleteResponse, err error) {
	getRequest := &privatev1.SecretsGetRequest{}
	getRequest.SetId(request.GetId())
	getResponse, err := s.private.Get(ctx, getRequest)
	if err != nil {
		return nil, err
	}

	existing := getResponse.GetObject()
	if existing.GetBackend() == privatev1.SecretBackend_SECRET_BACKEND_HUB {
		return nil, grpcstatus.Errorf(grpccodes.FailedPrecondition,
			"cannot delete hub-backed secrets via public API")
	}

	privateRequest := &privatev1.SecretsDeleteRequest{}
	privateRequest.SetId(request.GetId())

	_, err = s.private.Delete(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	response = &publicv1.SecretsDeleteResponse{}
	return
}
