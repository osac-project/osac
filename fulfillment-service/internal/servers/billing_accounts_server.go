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

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

// BillingAccountsServerBuilder contains the data and logic needed to create a new billing accounts server.
type BillingAccountsServerBuilder struct {
	logger   *slog.Logger
	delegate *PrivateBillingAccountsServer
}

var _ publicv1.BillingAccountsServer = (*BillingAccountsServer)(nil)

// BillingAccountsServer is the public server; it delegates to the private one.
type BillingAccountsServer struct {
	publicv1.UnimplementedBillingAccountsServer
	logger    *slog.Logger
	private   *PrivateBillingAccountsServer
	outMapper *GenericMapper[*privatev1.BillingAccount, *publicv1.BillingAccount]
}

func NewBillingAccountsServer() *BillingAccountsServerBuilder {
	return &BillingAccountsServerBuilder{}
}

// SetLogger sets the logger to use. This is mandatory.
func (b *BillingAccountsServerBuilder) SetLogger(value *slog.Logger) *BillingAccountsServerBuilder {
	b.logger = value
	return b
}

// SetDelegate sets the private server to delegate to. This is mandatory.
func (b *BillingAccountsServerBuilder) SetDelegate(value *PrivateBillingAccountsServer) *BillingAccountsServerBuilder {
	b.delegate = value
	return b
}

func (b *BillingAccountsServerBuilder) Build() (result *BillingAccountsServer, err error) {
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}
	if b.delegate == nil {
		err = errors.New("delegate is mandatory")
		return
	}
	outMapper, err := NewGenericMapper[*privatev1.BillingAccount, *publicv1.BillingAccount]().
		SetLogger(b.logger).
		SetStrict(false).
		Build()
	if err != nil {
		return nil, err
	}
	result = &BillingAccountsServer{logger: b.logger, private: b.delegate, outMapper: outMapper}
	return
}

// List maps the public shared list request to the private one and the private response back to public BillingAccount
// items. provider_account_reference never appears here: the outMapper copies only fields the public message
// declares, and the public BillingAccount message has no such field (CleanAPI strips it at proto generation).
func (s *BillingAccountsServer) List(ctx context.Context,
	request *publicv1.BillingAccountsListRequest) (*publicv1.BillingAccountsListResponse, error) {
	privateRequest := &privatev1.BillingAccountsListRequest{}
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

	privateItems := privateResponse.GetItems()
	publicItems := make([]*publicv1.BillingAccount, 0, len(privateItems))
	for _, privateItem := range privateItems {
		publicItem := &publicv1.BillingAccount{}
		if err := s.outMapper.Copy(ctx, privateItem, publicItem); err != nil {
			s.logger.ErrorContext(ctx, "Failed to map private billing account to public", slog.Any("error", err))
			return nil, status.Errorf(codes.Internal, "failed to process billing accounts")
		}
		publicItems = append(publicItems, publicItem)
	}

	response := &publicv1.BillingAccountsListResponse{}
	response.SetSize(int32(len(publicItems))) // #nosec G115 -- bounded by page size
	response.SetTotal(privateResponse.GetTotal())
	response.SetItems(publicItems)
	return response, nil
}
