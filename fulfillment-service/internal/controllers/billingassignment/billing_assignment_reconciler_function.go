/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package billingassignment

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"

	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/grpc"

	"github.com/osac-project/osac/fulfillment-service/internal/billing"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

type assignmentsClient interface {
	List(context.Context, *privatev1.TenantBillingAssignmentsListRequest, ...grpc.CallOption) (*privatev1.TenantBillingAssignmentsListResponse, error)
	UpdateStatus(context.Context, *privatev1.TenantBillingAssignmentsUpdateStatusRequest, ...grpc.CallOption) (*privatev1.TenantBillingAssignmentsUpdateStatusResponse, error)
}

type accountsClient interface {
	Get(context.Context, *privatev1.BillingAccountsGetRequest, ...grpc.CallOption) (*privatev1.BillingAccountsGetResponse, error)
}

type FunctionBuilder struct {
	logger      *slog.Logger
	connection  *grpc.ClientConn
	provider    billing.Provider
	metrics     prometheus.Registerer
	assignments assignmentsClient
	accounts    accountsClient
}

type Function struct {
	logger      *slog.Logger
	provider    billing.Provider
	metrics     *assignmentSyncMetrics
	assignments assignmentsClient
	accounts    accountsClient
}

func NewFunction() *FunctionBuilder {
	return &FunctionBuilder{}
}

func (b *FunctionBuilder) SetLogger(value *slog.Logger) *FunctionBuilder {
	b.logger = value
	return b
}

func (b *FunctionBuilder) SetConnection(value *grpc.ClientConn) *FunctionBuilder {
	b.connection = value
	return b
}

func (b *FunctionBuilder) SetProvider(value billing.Provider) *FunctionBuilder {
	b.provider = value
	return b
}

func (b *FunctionBuilder) SetMetricsRegisterer(value prometheus.Registerer) *FunctionBuilder {
	b.metrics = value
	return b
}

func (b *FunctionBuilder) SetClients(assignments assignmentsClient, accounts accountsClient) *FunctionBuilder {
	b.assignments = assignments
	b.accounts = accounts
	return b
}

func (b *FunctionBuilder) Build() (*Function, error) {
	if b.logger == nil {
		return nil, errors.New("logger is mandatory")
	}
	if b.assignments == nil || b.accounts == nil {
		if b.connection == nil {
			return nil, errors.New("connection is mandatory")
		}
		b.assignments = privatev1.NewTenantBillingAssignmentsClient(b.connection)
		b.accounts = privatev1.NewBillingAccountsClient(b.connection)
	}
	return &Function{
		logger:      b.logger,
		provider:    b.provider,
		metrics:     newAssignmentSyncMetrics(b.metrics),
		assignments: b.assignments,
		accounts:    b.accounts,
	}, nil
}

// Reconcile synchronizes a pending tenant assignment with the provider. It
// also removes stale mappings from previously assigned accounts, but only
// when the provider still maps that account to this tenant.
func (f *Function) Reconcile(ctx context.Context, assignment *privatev1.TenantBillingAssignment) error {
	if assignment == nil || assignment.GetId() == "" || assignment.GetSpec() == nil {
		return errors.New("invalid tenant billing assignment")
	}
	if f.provider == nil || assignment.GetStatus().GetConnectionState() !=
		privatev1.TenantBillingAssignmentConnectionState_TENANT_BILLING_ASSIGNMENT_CONNECTION_STATE_PENDING {
		return nil
	}
	tenantID := assignment.GetSpec().GetTenantId()
	if assignment.GetStatus().GetEffectiveTo() != nil {
		return f.clearHistoricalLink(ctx, assignment, tenantID)
	}
	targetAccountID := assignment.GetSpec().GetBillingAccountId()
	if tenantID == "" || targetAccountID == "" {
		return f.markPending(ctx, assignment.GetId(), "INVALID_ASSIGNMENT", errors.New("assignment tenant or account is missing"))
	}

	target, err := f.getProviderAccount(ctx, targetAccountID)
	if err != nil {
		return f.markPending(ctx, assignment.GetId(), "PROVIDER_UNAVAILABLE", err)
	}
	if target.ExternalID != "" && target.ExternalID != tenantID {
		return f.markFailed(ctx, assignment.GetId(), "ACCOUNT_ALREADY_BOUND")
	}
	if target.State != billing.AccountStateActive {
		return f.markPending(ctx, assignment.GetId(), "ACCOUNT_NOT_ACTIVE", errors.New("billing account is not active"))
	}
	if target.ExternalID != tenantID {
		if err = f.provider.UpdateAccountExternalID(ctx, target.Reference, target.ExternalID, tenantID); err != nil {
			reason := "PROVIDER_UPDATE_FAILED"
			if errors.Is(err, billing.ErrAccountExternalIDChanged) {
				reason = "ACCOUNT_LINK_CHANGED"
			}
			return f.markPending(ctx, assignment.GetId(), reason, err)
		}
		target, err = f.getProviderAccount(ctx, targetAccountID)
		if err != nil {
			return f.markPending(ctx, assignment.GetId(), "PROVIDER_UNAVAILABLE", err)
		}
	}
	if target.ExternalID != tenantID {
		return f.markPending(ctx, assignment.GetId(), "LINK_NOT_VERIFIED", errors.New("provider did not confirm tenant link"))
	}
	// Keep the previous provider link until the replacement account is verified.
	// If lookup or linking of the new account fails, the old billing association
	// remains usable while this assignment stays pending.
	accounts, err := f.listAssignments(ctx, tenantID)
	if err != nil {
		return f.markPending(ctx, assignment.GetId(), "ASSIGNMENT_LOOKUP_FAILED", err)
	}
	for _, previous := range accounts {
		if previous.GetId() == assignment.GetId() || previous.GetSpec().GetBillingAccountId() == targetAccountID {
			continue
		}
		if previous.GetStatus().GetEffectiveTo() == nil {
			continue
		}
		if err = f.clearHistoricalLink(ctx, previous, tenantID); err != nil {
			return f.markPending(ctx, assignment.GetId(), "PREVIOUS_ACCOUNT_UNLINK_FAILED", err)
		}
	}
	_, err = f.assignments.UpdateStatus(ctx, privatev1.TenantBillingAssignmentsUpdateStatusRequest_builder{
		Id:              assignment.GetId(),
		ConnectionState: privatev1.TenantBillingAssignmentConnectionState_TENANT_BILLING_ASSIGNMENT_CONNECTION_STATE_CONNECTED,
	}.Build())
	if err != nil {
		f.metrics.recordFailure("status_update_failed")
		return fmt.Errorf("failed to save verified tenant billing assignment state: %w", err)
	}
	return nil
}

func (f *Function) listAssignments(ctx context.Context, tenantID string) ([]*privatev1.TenantBillingAssignment, error) {
	filter := fmt.Sprintf("this.spec.tenant_id == %s", strconv.Quote(tenantID))
	items := make([]*privatev1.TenantBillingAssignment, 0)
	for offset := int32(0); ; offset += 1000 {
		limit := int32(1000)
		response, err := f.assignments.List(ctx, privatev1.TenantBillingAssignmentsListRequest_builder{
			Filter: &filter,
			Offset: &offset,
			Limit:  &limit,
		}.Build())
		if err != nil {
			return nil, errors.New("failed to list tenant billing assignments")
		}
		items = append(items, response.GetItems()...)
		if int64(len(items)) >= int64(response.GetTotal()) || len(response.GetItems()) == 0 {
			return items, nil
		}
	}
}

func (f *Function) clearHistoricalLink(ctx context.Context, assignment *privatev1.TenantBillingAssignment, tenantID string) error {
	account, err := f.getProviderAccount(ctx, assignment.GetSpec().GetBillingAccountId())
	if err != nil {
		return err
	}
	if account.ExternalID != tenantID {
		return nil
	}
	if err = f.provider.UpdateAccountExternalID(ctx, account.Reference, tenantID, ""); err != nil {
		return fmt.Errorf("failed to clear previous tenant link: %w", err)
	}
	account, err = f.getProviderAccount(ctx, assignment.GetSpec().GetBillingAccountId())
	if err != nil {
		return err
	}
	if account.ExternalID == tenantID {
		return errors.New("provider did not confirm previous tenant link removal")
	}
	return nil
}

func (f *Function) getProviderAccount(ctx context.Context, accountID string) (billing.Account, error) {
	response, err := f.accounts.Get(ctx, privatev1.BillingAccountsGetRequest_builder{Id: accountID}.Build())
	if err != nil {
		return billing.Account{}, errors.New("failed to load OSAC billing account")
	}
	account := response.GetObject()
	if account == nil || account.GetProviderAccountReference() == "" {
		return billing.Account{}, errors.New("OSAC billing account has no provider reference")
	}
	result, err := billing.RetryProviderRead(ctx, func(ctx context.Context) (billing.Account, error) {
		return f.provider.GetAccount(ctx, account.GetProviderAccountReference())
	})
	if err != nil {
		return billing.Account{}, errors.New("provider account lookup failed")
	}
	return result, nil
}

func (f *Function) markPending(ctx context.Context, id, reason string, cause error) error {
	f.metrics.recordFailure(reason)
	_, err := f.assignments.UpdateStatus(ctx, privatev1.TenantBillingAssignmentsUpdateStatusRequest_builder{
		Id:              id,
		ConnectionState: privatev1.TenantBillingAssignmentConnectionState_TENANT_BILLING_ASSIGNMENT_CONNECTION_STATE_PENDING,
		Message:         &reason,
	}.Build())
	if err != nil {
		return fmt.Errorf("failed to retain pending billing assignment status: %w", err)
	}
	f.logger.WarnContext(ctx, "billing assignment provider synchronization is pending", slog.String("reason", reason))
	return cause
}

func (f *Function) markFailed(ctx context.Context, id, reason string) error {
	f.metrics.recordFailure(reason)
	_, err := f.assignments.UpdateStatus(ctx, privatev1.TenantBillingAssignmentsUpdateStatusRequest_builder{
		Id:              id,
		ConnectionState: privatev1.TenantBillingAssignmentConnectionState_TENANT_BILLING_ASSIGNMENT_CONNECTION_STATE_FAILED,
		Message:         &reason,
	}.Build())
	if err != nil {
		return fmt.Errorf("failed to save failed billing assignment status: %w", err)
	}
	f.logger.WarnContext(ctx, "billing assignment provider synchronization failed", slog.String("reason", reason))
	return nil
}
