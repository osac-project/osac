/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

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
	"strings"
	"time"

	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/billing"
	"github.com/osac-project/osac/fulfillment-service/internal/config"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

const (
	billingInvoiceDefaultLimit = 20
	billingInvoiceMaxLimit     = 100
)

type BillingInvoicesServerBuilder struct {
	logger      *slog.Logger
	providerKey string
	provider    billing.InvoiceProvider
	store       assignedBillingAccountStore
	tenancy     auth.TenancyLogic
}

type BillingInvoicesServer struct {
	publicv1.UnimplementedBillingInvoicesServer
	logger      *slog.Logger
	providerKey string
	provider    billing.InvoiceProvider
	store       assignedBillingAccountStore
	tenancy     auth.TenancyLogic
}

var _ publicv1.BillingInvoicesServer = (*BillingInvoicesServer)(nil)

func NewBillingInvoicesServer() *BillingInvoicesServerBuilder {
	return &BillingInvoicesServerBuilder{}
}

func (b *BillingInvoicesServerBuilder) SetLogger(value *slog.Logger) *BillingInvoicesServerBuilder {
	b.logger = value
	return b
}

func (b *BillingInvoicesServerBuilder) SetProvider(providerKey string, value billing.InvoiceProvider) *BillingInvoicesServerBuilder {
	b.providerKey = providerKey
	b.provider = value
	return b
}

func (b *BillingInvoicesServerBuilder) SetAccountStore(value assignedBillingAccountStore) *BillingInvoicesServerBuilder {
	b.store = value
	return b
}

func (b *BillingInvoicesServerBuilder) SetTenancyLogic(value auth.TenancyLogic) *BillingInvoicesServerBuilder {
	b.tenancy = value
	return b
}

func (b *BillingInvoicesServerBuilder) Build() (*BillingInvoicesServer, error) {
	if b.logger == nil {
		return nil, errors.New("logger is mandatory")
	}
	if b.tenancy == nil {
		return nil, errors.New("tenancy logic is mandatory")
	}
	return &BillingInvoicesServer{
		logger:      b.logger,
		providerKey: b.providerKey,
		provider:    b.provider,
		store:       b.store,
		tenancy:     b.tenancy,
	}, nil
}

func (s *BillingInvoicesServer) List(ctx context.Context,
	request *publicv1.BillingInvoicesListRequest,
) (*publicv1.BillingInvoicesListResponse, error) {
	if request == nil {
		request = &publicv1.BillingInvoicesListRequest{}
	}
	if s.providerKey == "" {
		return nil, grpcstatus.Error(grpccodes.FailedPrecondition, "BILLING_NOT_CONFIGURED")
	}
	if s.provider == nil {
		return nil, grpcstatus.Error(grpccodes.Unimplemented, "PROVIDER_CAPABILITY_UNSUPPORTED")
	}
	offset := request.GetOffset()
	if offset < 0 {
		return nil, grpcstatus.Error(grpccodes.InvalidArgument, "offset must not be negative")
	}
	limit := int32(billingInvoiceDefaultLimit)
	if request.HasLimit() {
		limit = request.GetLimit()
	}
	if limit < 1 || limit > billingInvoiceMaxLimit {
		return nil, grpcstatus.Errorf(grpccodes.InvalidArgument, "limit must be between 1 and %d", billingInvoiceMaxLimit)
	}
	if err := validateInvoiceDateRange(request.GetFromDate(), request.GetToDate()); err != nil {
		return nil, grpcstatus.Error(grpccodes.InvalidArgument, "invoice date range is invalid")
	}

	account, err := s.assignedAccount(ctx, request.GetTenantId())
	if err != nil {
		return nil, err
	}
	page, err := s.provider.ListInvoices(ctx, billing.InvoiceQuery{
		AccountReference: account.ProviderAccountReference,
		FromDate:         request.GetFromDate(),
		ToDate:           request.GetToDate(),
		Offset:           offset,
		Limit:            limit,
	})
	if err != nil {
		s.logger.ErrorContext(ctx, "failed to list provider invoices")
		return nil, grpcstatus.Error(grpccodes.Unavailable, "provider invoices are unavailable")
	}
	items := make([]*publicv1.BillingInvoice, 0, len(page.Items))
	for _, invoice := range page.Items {
		if invoice.ID == "" || invoice.Number == "" {
			s.logger.ErrorContext(ctx, "provider invoice list returned an incomplete invoice")
			return nil, grpcstatus.Error(grpccodes.Unavailable, "provider invoices are unavailable")
		}
		items = append(items, billingInvoiceToProto(invoice))
	}
	size, ok := catalogPricingInputTotal(len(items))
	if !ok {
		return nil, grpcstatus.Error(grpccodes.Internal, "too many provider invoices")
	}
	return publicv1.BillingInvoicesListResponse_builder{
		Size:  size,
		Total: page.Total,
		Items: items,
	}.Build(), nil
}

func (s *BillingInvoicesServer) Get(ctx context.Context,
	request *publicv1.BillingInvoicesGetRequest,
) (*publicv1.BillingInvoicesGetResponse, error) {
	if request == nil || strings.TrimSpace(request.GetId()) == "" {
		return nil, grpcstatus.Error(grpccodes.InvalidArgument, "invoice id is required")
	}
	if s.providerKey == "" {
		return nil, grpcstatus.Error(grpccodes.FailedPrecondition, "BILLING_NOT_CONFIGURED")
	}
	if s.provider == nil {
		return nil, grpcstatus.Error(grpccodes.Unimplemented, "PROVIDER_CAPABILITY_UNSUPPORTED")
	}
	account, err := s.assignedAccount(ctx, request.GetTenantId())
	if err != nil {
		return nil, err
	}
	invoice, err := s.provider.GetInvoice(ctx, account.ProviderAccountReference, request.GetId())
	if errors.Is(err, billing.ErrInvoiceNotFound) {
		return nil, grpcstatus.Error(grpccodes.NotFound, "invoice was not found")
	}
	if err != nil {
		s.logger.ErrorContext(ctx, "failed to retrieve provider invoice")
		return nil, grpcstatus.Error(grpccodes.Unavailable, "provider invoice is unavailable")
	}
	if invoice.ID != request.GetId() {
		s.logger.ErrorContext(ctx, "provider invoice lookup returned a mismatched invoice")
		return nil, grpcstatus.Error(grpccodes.Unavailable, "provider invoice is unavailable")
	}
	return publicv1.BillingInvoicesGetResponse_builder{Object: billingInvoiceToProto(invoice)}.Build(), nil
}

func (s *BillingInvoicesServer) assignedAccount(ctx context.Context, requestedTenant string) (billing.AssignedAccount, error) {
	tenantID := strings.TrimSpace(requestedTenant)
	if tenantID == "" {
		tenantID = config.TenantFromContext(ctx)
	}
	if tenantID == "" {
		return billing.AssignedAccount{}, grpcstatus.Error(grpccodes.InvalidArgument, "tenant_id is required")
	}
	if err := auth.ValidateTenantAssignment(ctx, s.tenancy, tenantID); err != nil {
		return billing.AssignedAccount{}, convertTenantErrorToGRPC(ctx, err, s.logger, tenantID)
	}
	if s.store == nil {
		return billing.AssignedAccount{}, grpcstatus.Error(grpccodes.Internal, "tenant billing account lookup is unavailable")
	}
	account, err := s.store.GetAssignedAccount(ctx, tenantID)
	if errors.Is(err, billing.ErrTenantBillingAssignmentNotFound) {
		return billing.AssignedAccount{}, grpcstatus.Error(grpccodes.FailedPrecondition, "tenant has no active billing account")
	}
	if err != nil {
		s.logger.ErrorContext(ctx, "failed to resolve tenant billing account", slog.Any("error", err))
		return billing.AssignedAccount{}, grpcstatus.Error(grpccodes.Internal, "failed to resolve tenant billing account")
	}
	if account.ProviderKey != s.providerKey {
		return billing.AssignedAccount{}, grpcstatus.Error(grpccodes.FailedPrecondition, "tenant billing account uses a different provider")
	}
	if !assignmentIsConnected(account.ConnectionState) {
		return billing.AssignedAccount{}, grpcstatus.Error(grpccodes.FailedPrecondition, "tenant billing account is not connected")
	}
	if account.ProviderAccountReference == "" {
		return billing.AssignedAccount{}, grpcstatus.Error(grpccodes.FailedPrecondition, "tenant billing account is unavailable")
	}
	return account, nil
}

func validateInvoiceDateRange(fromDate, toDate string) error {
	const invoiceDateFormat = "2006-01-02"
	var from, to time.Time
	var err error
	if fromDate != "" {
		from, err = time.Parse(invoiceDateFormat, fromDate)
		if err != nil {
			return err
		}
	}
	if toDate != "" {
		to, err = time.Parse(invoiceDateFormat, toDate)
		if err != nil {
			return err
		}
	}
	if fromDate != "" && toDate != "" && from.After(to) {
		return errors.New("from_date must be on or before to_date")
	}
	return nil
}

func billingInvoiceToProto(invoice billing.Invoice) *publicv1.BillingInvoice {
	items := make([]*publicv1.BillingInvoiceLineItem, 0, len(invoice.Items))
	for _, item := range invoice.Items {
		items = append(items, publicv1.BillingInvoiceLineItem_builder{
			Description:  item.Description,
			ServiceName:  item.ServiceName,
			ServiceCode:  item.ServiceCode,
			Volume:       item.Volume,
			RatePerUnit:  item.RatePerUnit,
			Amount:       item.Amount,
			TaxAmount:    item.TaxAmount,
			Balance:      item.Balance,
			ChargeType:   item.ChargeType,
			PriceMethod:  item.PriceMethod,
			PeriodStart:  item.PeriodStart,
			PeriodEnd:    item.PeriodEnd,
			CurrencyCode: item.CurrencyCode,
		}.Build())
	}
	return publicv1.BillingInvoice_builder{
		Id:             invoice.ID,
		InvoiceNumber:  invoice.Number,
		InvoiceDate:    invoice.Date,
		Status:         invoice.Status,
		Type:           invoice.Type,
		Amount:         invoice.Amount,
		TaxAmount:      invoice.TaxAmount,
		Balance:        invoice.Balance,
		CurrencyCode:   invoice.CurrencyCode,
		DueDate:        invoice.DueDate,
		PeriodStart:    invoice.PeriodStart,
		PeriodEnd:      invoice.PeriodEnd,
		PaymentStatus:  invoice.PaymentStatus,
		ApprovalStatus: invoice.ApprovalStatus,
		Items:          items,
	}.Build()
}
