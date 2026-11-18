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

// BillingProviderStatusesServerBuilder contains the data and logic needed to create a new billing provider statuses
// server.
type BillingProviderStatusesServerBuilder struct {
	logger   *slog.Logger
	delegate *PrivateBillingProviderStatusesServer
}

var _ publicv1.BillingProviderStatusesServer = (*BillingProviderStatusesServer)(nil)

// BillingProviderStatusesServer is the public server; it delegates to the private one.
type BillingProviderStatusesServer struct {
	publicv1.UnimplementedBillingProviderStatusesServer
	private   *PrivateBillingProviderStatusesServer
	outMapper *GenericMapper[*privatev1.BillingProviderStatus, *publicv1.BillingProviderStatus]
}

// NewBillingProviderStatusesServer starts building the public statuses server.
func NewBillingProviderStatusesServer() *BillingProviderStatusesServerBuilder {
	return &BillingProviderStatusesServerBuilder{}
}

// SetLogger sets the logger to use. This is mandatory.
func (b *BillingProviderStatusesServerBuilder) SetLogger(value *slog.Logger) *BillingProviderStatusesServerBuilder {
	b.logger = value
	return b
}

// SetDelegate sets the private server to delegate to, so that public and private reads share one probe state. This
// is mandatory.
func (b *BillingProviderStatusesServerBuilder) SetDelegate(
	value *PrivateBillingProviderStatusesServer) *BillingProviderStatusesServerBuilder {
	b.delegate = value
	return b
}

// Build fails when the logger or delegate is missing.
func (b *BillingProviderStatusesServerBuilder) Build() (result *BillingProviderStatusesServer, err error) {
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}
	if b.delegate == nil {
		err = errors.New("delegate is mandatory")
		return
	}
	outMapper, err := NewGenericMapper[*privatev1.BillingProviderStatus, *publicv1.BillingProviderStatus]().
		SetLogger(b.logger).
		SetStrict(false).
		Build()
	if err != nil {
		return nil, err
	}
	result = &BillingProviderStatusesServer{private: b.delegate, outMapper: outMapper}
	return
}

// Get delegates to the private server and maps the status to the public type.
func (s *BillingProviderStatusesServer) Get(ctx context.Context,
	request *publicv1.BillingProviderStatusesGetRequest) (*publicv1.BillingProviderStatusesGetResponse, error) {
	privateResponse, err := s.private.Get(ctx, &privatev1.BillingProviderStatusesGetRequest{})
	if err != nil {
		return nil, err
	}
	object := &publicv1.BillingProviderStatus{}
	if err := s.outMapper.Copy(ctx, privateResponse.GetObject(), object); err != nil {
		return nil, err
	}
	return publicv1.BillingProviderStatusesGetResponse_builder{Object: object}.Build(), nil
}
