/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

//go:generate mockgen -source=../../api/osac/private/v1/projects_service_grpc.pb.go -destination=projects_client_mock.go -package=tenant ProjectsClient
//go:generate mockgen -source=../../api/osac/private/v1/virtual_networks_service_grpc.pb.go -destination=virtual_networks_client_mock.go -package=tenant VirtualNetworksClient
//go:generate mockgen -source=../../api/osac/private/v1/subnets_service_grpc.pb.go -destination=subnets_client_mock.go -package=tenant SubnetsClient
//go:generate mockgen -source=../../api/osac/private/v1/security_groups_service_grpc.pb.go -destination=security_groups_client_mock.go -package=tenant SecurityGroupsClient
//go:generate mockgen -source=../../api/osac/private/v1/nat_gateways_service_grpc.pb.go -destination=nat_gateways_client_mock.go -package=tenant NATGatewaysClient

package tenant

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	privatev1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/private/v1"
	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/controllers/finalizers"
	"github.com/osac-project/osac/fulfillment-service/internal/idp"
	"github.com/osac-project/osac/fulfillment-service/internal/masks"
)

// FunctionBuilder contains the data needed to build instances of the reconciler function.
type FunctionBuilder struct {
	logger     *slog.Logger
	connection *grpc.ClientConn
	idpManager *idp.TenantManager
}

// NewFunction creates a builder that can be used to configure and create reconciler functions.
func NewFunction() *FunctionBuilder {
	return &FunctionBuilder{}
}

// SetLogger sets the logger that the reconciler will use to write log messages.
func (b *FunctionBuilder) SetLogger(value *slog.Logger) *FunctionBuilder {
	b.logger = value
	return b
}

// SetConnection sets the gRPC connection that the reconciler will use to communicate with the API server.
func (b *FunctionBuilder) SetConnection(value *grpc.ClientConn) *FunctionBuilder {
	b.connection = value
	return b
}

// SetIdpManager sets the IDP manager that the reconciler will use to manage tenants in the identity provider.
func (b *FunctionBuilder) SetIdpManager(value *idp.TenantManager) *FunctionBuilder {
	b.idpManager = value
	return b
}

// Build uses the data stored in the builder to create and configure a new reconciler function.
func (b *FunctionBuilder) Build() (result *function, err error) {
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}
	if b.connection == nil {
		err = errors.New("connection is mandatory")
		return
	}
	if b.idpManager == nil {
		err = errors.New("IDP manager is mandatory")
		return
	}

	result = &function{
		logger:                b.logger,
		tenantsClient:         privatev1.NewTenantsClient(b.connection),
		projectsClient:        privatev1.NewProjectsClient(b.connection),
		virtualNetworksClient: privatev1.NewVirtualNetworksClient(b.connection),
		subnetsClient:         privatev1.NewSubnetsClient(b.connection),
		securityGroupsClient:  privatev1.NewSecurityGroupsClient(b.connection),
		natGatewaysClient:     privatev1.NewNATGatewaysClient(b.connection),
		idpManager:            b.idpManager,
		maskCalculator:        masks.NewCalculator().Build(),
	}
	return
}

// function is the implementation of the reconciler function.
type function struct {
	logger                *slog.Logger
	tenantsClient         privatev1.TenantsClient
	projectsClient        privatev1.ProjectsClient
	virtualNetworksClient privatev1.VirtualNetworksClient
	subnetsClient         privatev1.SubnetsClient
	securityGroupsClient  privatev1.SecurityGroupsClient
	natGatewaysClient     privatev1.NATGatewaysClient
	idpManager            *idp.TenantManager
	maskCalculator        *masks.Calculator
}

// Run executes the reconciliation logic for the given tenant.
func (r *function) Run(ctx context.Context, tenant *privatev1.Tenant) error {
	oldTenant := proto.Clone(tenant).(*privatev1.Tenant)

	task := &task{
		r:      r,
		tenant: tenant,
	}

	var err error
	if tenant.HasMetadata() && tenant.GetMetadata().HasDeletionTimestamp() {
		err = task.delete(ctx)
	} else {
		err = task.update(ctx)
	}
	if err != nil {
		return err
	}

	updateMask := r.maskCalculator.Calculate(oldTenant, tenant)

	if len(updateMask.GetPaths()) > 0 {
		_, err = r.tenantsClient.Update(ctx, privatev1.TenantsUpdateRequest_builder{
			Object:     tenant,
			UpdateMask: updateMask,
		}.Build())
	}

	return err
}

// task contains the data needed to reconcile a single tenant.
type task struct {
	r      *function
	tenant *privatev1.Tenant
}

// update performs the reconciliation logic for creating or updating a tenant.
func (t *task) update(ctx context.Context) error {
	if t.addFinalizer() {
		return nil
	}

	t.setDefaults()
	t.setConditionDefaults()

	if err := t.validateTenant(); err != nil {
		return err
	}

	state := t.tenant.GetStatus().GetState()

	// Skip reconciliation only for terminal failure state.
	// This prevents infinite retry loops when IDP operations fail.
	if state == privatev1.TenantState_TENANT_STATE_FAILED {
		return nil
	}

	// For synced tenants, update IDP and check default networking readiness.
	if state == privatev1.TenantState_TENANT_STATE_SYNCED {
		if err := t.updateIDP(ctx); err != nil {
			return err
		}
		if t.tenant.GetStatus().GetState() == privatev1.TenantState_TENANT_STATE_FAILED {
			return nil
		}
		return t.checkDefaultNetworkingReadiness(ctx)
	}

	// Tenant is PENDING or UNSPECIFIED, perform initial sync to IDP
	return t.syncToIDP(ctx)
}

// syncToIDP synchronizes the tenant to the identity provider.
func (t *task) syncToIDP(ctx context.Context) error {
	if t.tenant.GetStatus().GetIdpTenantName() != "" {
		t.tenant.GetStatus().SetState(privatev1.TenantState_TENANT_STATE_SYNCED)
		t.tenant.GetStatus().ClearMessage()
		return nil
	}

	t.tenant.GetStatus().SetState(privatev1.TenantState_TENANT_STATE_PENDING)

	tenantName := t.tenant.GetMetadata().GetName()
	config := &idp.TenantConfig{
		Name:               tenantName,
		Enabled:            new(!t.isBuiltin()),
		Domains:            t.tenant.GetSpec().GetDomains(),
		BreakGlassPassword: t.tenant.GetStatus().GetBreakGlassCredentials().GetPassword(),
	}

	credentials, err := t.r.idpManager.CreateTenant(ctx, config)
	if err != nil {
		t.tenant.GetStatus().SetState(privatev1.TenantState_TENANT_STATE_FAILED)
		t.tenant.GetStatus().SetMessage(fmt.Sprintf("Tenant creation in IDP failed: %v", err))
		return nil
	}

	t.tenant.GetStatus().SetState(privatev1.TenantState_TENANT_STATE_SYNCED)
	t.tenant.GetStatus().SetIdpTenantName(config.Name)
	t.tenant.GetStatus().SetBreakGlassUserId(credentials.UserID)
	t.tenant.GetStatus().ClearBreakGlassCredentials()

	t.r.logger.DebugContext(ctx, "Tenant synced to IDP",
		slog.String("tenant_id", t.tenant.GetId()),
		slog.String("tenant_name", tenantName),
	)

	return nil
}

// updateIDP updates the tenant in the identity provider with the current spec values.
func (t *task) updateIDP(ctx context.Context) error {
	tenantName := t.tenant.GetStatus().GetIdpTenantName()
	if tenantName == "" {
		t.tenant.GetStatus().SetState(privatev1.TenantState_TENANT_STATE_FAILED)
		t.tenant.GetStatus().SetMessage("Tenant name is empty")
		t.r.logger.ErrorContext(
			ctx,
			"Tenant name is empty",
			slog.String("tenant", t.tenant.GetMetadata().GetName()),
		)
		return nil
	}
	domains := t.tenant.GetSpec().GetDomains()
	err := t.r.idpManager.UpdateTenant(ctx, tenantName, domains)
	if err != nil {
		t.r.logger.ErrorContext(ctx, "Failed to update tenant domains in IDP",
			slog.String("tenant_id", t.tenant.GetId()),
			slog.Any("error", err),
		)
		return err
	}
	return nil
}

// setDefaults sets default values for the tenant.
func (t *task) setDefaults() {
	if !t.tenant.HasStatus() {
		t.tenant.SetStatus(&privatev1.TenantStatus{})
	}
	if t.tenant.GetStatus().GetState() == privatev1.TenantState_TENANT_STATE_UNSPECIFIED {
		t.tenant.GetStatus().SetState(privatev1.TenantState_TENANT_STATE_PENDING)
	}
}

// validateTenant verifies that the tenant has a tenant assigned.
func (t *task) validateTenant() error {
	if !t.tenant.HasMetadata() || t.tenant.GetMetadata().GetTenant() == "" {
		return errors.New("Tenant must have a metadata.tenant assigned") //nolint:staticcheck // ST1005: Tenant is an API resource name
	}
	return nil
}

// addFinalizer adds the controller finalizer to the tenant if not already present.
// Returns true if the finalizer was added (indicating the update should be saved immediately).
func (t *task) addFinalizer() bool {
	if !t.tenant.HasMetadata() {
		t.tenant.SetMetadata(&privatev1.Metadata{})
	}
	list := t.tenant.GetMetadata().GetFinalizers()
	if !slices.Contains(list, finalizers.Controller) {
		list = append(list, finalizers.Controller)
		t.tenant.GetMetadata().SetFinalizers(list)
		return true
	}
	return false
}

// removeFinalizer removes the controller finalizer from the tenant.
func (t *task) removeFinalizer() {
	if !t.tenant.HasMetadata() {
		return
	}
	list := t.tenant.GetMetadata().GetFinalizers()
	if slices.Contains(list, finalizers.Controller) {
		list = slices.DeleteFunc(list, func(item string) bool {
			return item == finalizers.Controller
		})
		t.tenant.GetMetadata().SetFinalizers(list)
	}
}

// isBuiltin returns true if the tenant is a builtin tenant that should not be user-accessible in the
// identity provider. Builtin tenants like "shared" and "system" are created disabled.
func (t *task) isBuiltin() bool {
	name := t.tenant.GetMetadata().GetName()
	return name == auth.SharedTenant || name == auth.SystemTenant
}

// delete performs the deletion cleanup for a tenant.
func (t *task) delete(ctx context.Context) error {
	// Block until all projects are deleted by the administrator.
	remaining, err := t.countRemainingProjects(ctx)
	if err != nil {
		return fmt.Errorf("failed to query remaining projects: %w", err)
	}
	if remaining > 0 {
		t.r.logger.InfoContext(ctx, "Waiting for projects to be deleted before tenant can be removed",
			slog.String("tenant_id", t.tenant.GetId()),
			slog.Int("remaining_projects", int(remaining)),
		)
		return fmt.Errorf("tenant still has %d project(s) pending deletion", remaining)
	}

	// Skip if not synced to IDP yet
	if t.tenant.GetStatus().GetState() != privatev1.TenantState_TENANT_STATE_SYNCED {
		t.removeFinalizer()
		return nil
	}

	// Delete from IDP
	tenantName := t.tenant.GetStatus().GetIdpTenantName()
	if tenantName == "" {
		t.removeFinalizer()
		return nil
	}

	err = t.r.idpManager.DeleteTenant(ctx, tenantName)
	if err != nil {
		return fmt.Errorf("failed to delete IDP tenant: %w", err)
	}

	t.r.logger.DebugContext(ctx, "Deleted tenant from IDP",
		slog.String("tenant_id", t.tenant.GetId()),
		slog.String("idp_name", tenantName),
	)

	t.removeFinalizer()
	return nil
}

// countRemainingProjects returns the number of projects that still belong to
// this tenant. The tenant reconciler blocks deletion until this returns 0 —
// it is the administrator's responsibility to delete all projects first.
func (t *task) countRemainingProjects(ctx context.Context) (int32, error) {
	listFilter := fmt.Sprintf("this.metadata.tenant == %q", t.tenant.GetMetadata().GetName())
	listResp, err := t.r.projectsClient.List(ctx, privatev1.ProjectsListRequest_builder{
		Filter: new(listFilter),
		Limit:  new(int32(0)),
	}.Build())
	if err != nil {
		return 0, err
	}
	return listResp.GetTotal(), nil
}

var tenantConditionTypes = []privatev1.TenantConditionType{
	privatev1.TenantConditionType_TENANT_CONDITION_TYPE_DEFAULT_NETWORKING_READY,
}

// setConditionDefaults ensures every known condition type has an entry in the tenant's conditions list.
func (t *task) setConditionDefaults() {
	for _, conditionType := range tenantConditionTypes {
		t.setConditionDefault(conditionType)
	}
}

func (t *task) setConditionDefault(conditionType privatev1.TenantConditionType) {
	for _, c := range t.tenant.GetStatus().GetConditions() {
		if c.GetType() == conditionType {
			return
		}
	}
	conditions := t.tenant.GetStatus().GetConditions()
	conditions = append(conditions, privatev1.TenantCondition_builder{
		Type:   conditionType,
		Status: privatev1.ConditionStatus_CONDITION_STATUS_FALSE,
	}.Build())
	t.tenant.GetStatus().SetConditions(conditions)
}

func (t *task) updateCondition(conditionType privatev1.TenantConditionType, status privatev1.ConditionStatus,
	reason string, message string) {
	conditions := t.tenant.GetStatus().GetConditions()
	for i, c := range conditions {
		if c.GetType() == conditionType {
			transitionTime := c.GetLastTransitionTime()
			if c.GetStatus() != status {
				transitionTime = timestamppb.Now()
			}
			conditions[i] = privatev1.TenantCondition_builder{
				Type:               conditionType,
				Status:             status,
				Reason:             new(reason),
				Message:            new(message),
				LastTransitionTime: transitionTime,
			}.Build()
			t.tenant.GetStatus().SetConditions(conditions)
			return
		}
	}
	conditions = append(conditions, privatev1.TenantCondition_builder{
		Type:               conditionType,
		Status:             status,
		Reason:             new(reason),
		Message:            new(message),
		LastTransitionTime: timestamppb.Now(),
	}.Build())
	t.tenant.GetStatus().SetConditions(conditions)
}

const defaultLabelFilter = "this.metadata.labels['osac.openshift.io/default'] == 'true'"

func (t *task) checkDefaultNetworkingReadiness(ctx context.Context) error {
	if t.r.virtualNetworksClient == nil {
		return nil
	}
	tenantName := t.tenant.GetMetadata().GetName()
	filter := fmt.Sprintf("%s && this.metadata.tenant == %q", defaultLabelFilter, tenantName)

	var pending, failed []string

	vns, err := t.r.virtualNetworksClient.List(ctx, privatev1.VirtualNetworksListRequest_builder{
		Filter: new(filter),
	}.Build())
	if err != nil {
		return fmt.Errorf("failed to list default virtual networks: %w", err)
	}
	for _, vn := range vns.GetItems() {
		switch vn.GetStatus().GetState() {
		case privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_READY:
		case privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_FAILED:
			failed = append(failed, fmt.Sprintf("VirtualNetwork/%s", vn.GetMetadata().GetName()))
		default:
			pending = append(pending, fmt.Sprintf("VirtualNetwork/%s", vn.GetMetadata().GetName()))
		}
	}

	subnets, err := t.r.subnetsClient.List(ctx, privatev1.SubnetsListRequest_builder{
		Filter: new(filter),
	}.Build())
	if err != nil {
		return fmt.Errorf("failed to list default subnets: %w", err)
	}
	for _, s := range subnets.GetItems() {
		switch s.GetStatus().GetState() {
		case privatev1.SubnetState_SUBNET_STATE_READY:
		case privatev1.SubnetState_SUBNET_STATE_FAILED, privatev1.SubnetState_SUBNET_STATE_DELETE_FAILED:
			failed = append(failed, fmt.Sprintf("Subnet/%s", s.GetMetadata().GetName()))
		default:
			pending = append(pending, fmt.Sprintf("Subnet/%s", s.GetMetadata().GetName()))
		}
	}

	sgs, err := t.r.securityGroupsClient.List(ctx, privatev1.SecurityGroupsListRequest_builder{
		Filter: new(filter),
	}.Build())
	if err != nil {
		return fmt.Errorf("failed to list default security groups: %w", err)
	}
	for _, sg := range sgs.GetItems() {
		switch sg.GetStatus().GetState() {
		case privatev1.SecurityGroupState_SECURITY_GROUP_STATE_READY:
		case privatev1.SecurityGroupState_SECURITY_GROUP_STATE_FAILED, privatev1.SecurityGroupState_SECURITY_GROUP_STATE_DELETE_FAILED:
			failed = append(failed, fmt.Sprintf("SecurityGroup/%s", sg.GetMetadata().GetName()))
		default:
			pending = append(pending, fmt.Sprintf("SecurityGroup/%s", sg.GetMetadata().GetName()))
		}
	}

	ngs, err := t.r.natGatewaysClient.List(ctx, privatev1.NATGatewaysListRequest_builder{
		Filter: new(filter),
	}.Build())
	if err != nil {
		return fmt.Errorf("failed to list default NAT gateways: %w", err)
	}
	for _, ng := range ngs.GetItems() {
		switch ng.GetStatus().GetState() {
		case privatev1.NATGatewayState_NAT_GATEWAY_STATE_READY:
		case privatev1.NATGatewayState_NAT_GATEWAY_STATE_FAILED:
			failed = append(failed, fmt.Sprintf("NATGateway/%s", ng.GetMetadata().GetName()))
		default:
			pending = append(pending, fmt.Sprintf("NATGateway/%s", ng.GetMetadata().GetName()))
		}
	}

	totalResources := len(vns.GetItems()) + len(subnets.GetItems()) + len(sgs.GetItems()) + len(ngs.GetItems())
	condType := privatev1.TenantConditionType_TENANT_CONDITION_TYPE_DEFAULT_NETWORKING_READY

	if totalResources == 0 {
		t.updateCondition(condType, privatev1.ConditionStatus_CONDITION_STATUS_TRUE,
			"NoDefaultNetworking", "No default networking resources configured")
		return nil
	}

	if len(failed) > 0 {
		t.updateCondition(condType, privatev1.ConditionStatus_CONDITION_STATUS_FALSE,
			"ResourceFailed", fmt.Sprintf("Default networking resources failed: %s", strings.Join(failed, ", ")))
		return nil
	}

	if len(pending) > 0 {
		t.updateCondition(condType, privatev1.ConditionStatus_CONDITION_STATUS_FALSE,
			"ResourcesPending", fmt.Sprintf("Default networking resources pending: %s", strings.Join(pending, ", ")))
		return nil
	}

	t.updateCondition(condType, privatev1.ConditionStatus_CONDITION_STATUS_TRUE,
		"AllResourcesReady", "All default networking resources are ready")
	return nil
}
