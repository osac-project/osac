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

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

// CatalogPricingInputsServerBuilder contains the data and logic needed to create a new public catalog pricing
// inputs server.
type CatalogPricingInputsServerBuilder struct {
	logger   *slog.Logger
	delegate *PrivateCatalogPricingInputsServer
}

var _ publicv1.CatalogPricingInputsServer = (*CatalogPricingInputsServer)(nil)

// CatalogPricingInputsServer is the public server; it delegates to the private one and maps the result to the
// public type.
type CatalogPricingInputsServer struct {
	publicv1.UnimplementedCatalogPricingInputsServer
	private   *PrivateCatalogPricingInputsServer
	outMapper *GenericMapper[*privatev1.CatalogPricingInput, *publicv1.CatalogPricingInput]
}

// NewCatalogPricingInputsServer starts building the public catalog pricing inputs server.
func NewCatalogPricingInputsServer() *CatalogPricingInputsServerBuilder {
	return &CatalogPricingInputsServerBuilder{}
}

// SetLogger sets the logger to use. This is mandatory.
func (b *CatalogPricingInputsServerBuilder) SetLogger(value *slog.Logger) *CatalogPricingInputsServerBuilder {
	b.logger = value
	return b
}

// SetDelegate sets the private server to delegate to. This is mandatory.
func (b *CatalogPricingInputsServerBuilder) SetDelegate(value *PrivateCatalogPricingInputsServer) *CatalogPricingInputsServerBuilder {
	b.delegate = value
	return b
}

// Build fails when the logger or delegate is missing.
func (b *CatalogPricingInputsServerBuilder) Build() (result *CatalogPricingInputsServer, err error) {
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}
	if b.delegate == nil {
		err = errors.New("delegate is mandatory")
		return
	}
	outMapper, err := NewGenericMapper[*privatev1.CatalogPricingInput, *publicv1.CatalogPricingInput]().
		SetLogger(b.logger).
		SetStrict(false).
		Build()
	if err != nil {
		return nil, err
	}
	result = &CatalogPricingInputsServer{private: b.delegate, outMapper: outMapper}
	return
}

// List delegates to the private server and maps each item to the public type.
func (s *CatalogPricingInputsServer) List(ctx context.Context,
	request *publicv1.CatalogPricingInputsListRequest) (*publicv1.CatalogPricingInputsListResponse, error) {
	privateRequest := &privatev1.CatalogPricingInputsListRequest{}
	privateRequest.SetOffset(request.GetOffset())
	if request.HasLimit() {
		privateRequest.SetLimit(request.GetLimit())
	}
	privateRequest.SetFilter(request.GetFilter())
	privateRequest.SetOrder(request.GetOrder())
	privateResponse, err := s.private.List(ctx, privateRequest)
	if err != nil {
		return nil, err
	}
	items := make([]*publicv1.CatalogPricingInput, len(privateResponse.GetItems()))
	for i, privateItem := range privateResponse.GetItems() {
		item := &publicv1.CatalogPricingInput{}
		if err := s.outMapper.Copy(ctx, privateItem, item); err != nil {
			return nil, err
		}
		items[i] = item
	}
	return publicv1.CatalogPricingInputsListResponse_builder{
		Size:  privateResponse.GetSize(),
		Total: privateResponse.GetTotal(),
		Items: items,
	}.Build(), nil
}
