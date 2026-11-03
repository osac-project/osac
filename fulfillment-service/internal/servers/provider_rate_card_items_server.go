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

	"google.golang.org/protobuf/proto"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

// ProviderRateCardItemsServerBuilder builds the public server.
type ProviderRateCardItemsServerBuilder struct {
	logger   *slog.Logger
	delegate *PrivateProviderRateCardItemsServer
}

var _ publicv1.ProviderRateCardItemsServer = (*ProviderRateCardItemsServer)(nil)

// ProviderRateCardItemsServer is the public server; it delegates to the private one and maps the result to the
// public wire type.
type ProviderRateCardItemsServer struct {
	publicv1.UnimplementedProviderRateCardItemsServer
	private   *PrivateProviderRateCardItemsServer
	outMapper *GenericMapper[*privatev1.ProviderRateCardItem, *publicv1.ProviderRateCardItem]
}

// NewProviderRateCardItemsServer starts building the public server.
func NewProviderRateCardItemsServer() *ProviderRateCardItemsServerBuilder {
	return &ProviderRateCardItemsServerBuilder{}
}

// SetLogger sets the logger to use. This is mandatory.
func (b *ProviderRateCardItemsServerBuilder) SetLogger(value *slog.Logger) *ProviderRateCardItemsServerBuilder {
	b.logger = value
	return b
}

// SetDelegate sets the private server to delegate to. This is mandatory.
func (b *ProviderRateCardItemsServerBuilder) SetDelegate(
	value *PrivateProviderRateCardItemsServer) *ProviderRateCardItemsServerBuilder {
	b.delegate = value
	return b
}

// Build fails when the logger or delegate is missing.
func (b *ProviderRateCardItemsServerBuilder) Build() (result *ProviderRateCardItemsServer, err error) {
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}
	if b.delegate == nil {
		err = errors.New("delegate is mandatory")
		return
	}
	outMapper, err := NewGenericMapper[*privatev1.ProviderRateCardItem, *publicv1.ProviderRateCardItem]().
		SetLogger(b.logger).
		SetStrict(false).
		Build()
	if err != nil {
		return nil, err
	}
	result = &ProviderRateCardItemsServer{private: b.delegate, outMapper: outMapper}
	return
}

// List delegates to the private server and maps every item to the public type.
func (s *ProviderRateCardItemsServer) List(ctx context.Context,
	request *publicv1.ProviderRateCardItemsListRequest) (*publicv1.ProviderRateCardItemsListResponse, error) {
	privateRequest := privatev1.ProviderRateCardItemsListRequest_builder{
		Offset:       proto.Int32(request.GetOffset()),
		Limit:        proto.Int32(request.GetLimit()),
		Filter:       proto.String(request.GetFilter()),
		Order:        proto.String(request.GetOrder()),
		TenantId:     proto.String(request.GetTenantId()),
		Service:      proto.String(request.GetService()),
		CurrencyCode: proto.String(request.GetCurrencyCode()),
	}.Build()
	privateResponse, err := s.private.List(ctx, privateRequest)
	if err != nil {
		return nil, err
	}
	items := make([]*publicv1.ProviderRateCardItem, 0, len(privateResponse.GetItems()))
	for _, privateItem := range privateResponse.GetItems() {
		object := &publicv1.ProviderRateCardItem{}
		if err := s.outMapper.Copy(ctx, privateItem, object); err != nil {
			return nil, err
		}
		items = append(items, object)
	}
	return publicv1.ProviderRateCardItemsListResponse_builder{
		Size:  privateResponse.GetSize(),
		Total: privateResponse.GetTotal(),
		Items: items,
	}.Build(), nil
}
