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
//go:generate mockgen -source=../../api/osac/private/v1/network_classes_service_grpc.pb.go -destination=network_classes_client_mock.go -package=tenant NetworkClassesClient
//go:generate mockgen -source=../../api/osac/private/v1/external_ip_pools_service_grpc.pb.go -destination=external_ip_pools_client_mock.go -package=tenant ExternalIPPoolsClient
//go:generate mockgen -source=../../api/osac/private/v1/external_ips_service_grpc.pb.go -destination=external_ips_client_mock.go -package=tenant ExternalIPsClient

package tenant

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"google.golang.org/grpc"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	privatev1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/private/v1"
	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/controllers/finalizers"
	"github.com/osac-project/osac/fulfillment-service/internal/idp"
	"github.com/osac-project/osac/fulfillment-service/internal/masks"
	"github.com/osac-project/osac/fulfillment-service/internal/vault"
)

// FunctionBuilder contains the data needed to build instances of the reconciler function.
type FunctionBuilder struct {
	logger         *slog.Logger
	connection     *grpc.ClientConn
	idpManager     *idp.TenantManager
	vaultLifecycle vault.LifecycleClient
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

// SetVaultLifecycle sets the vault lifecycle client that the reconciler will use to manage tenant namespaces
// in the secret store.
func (b *FunctionBuilder) SetVaultLifecycle(value vault.LifecycleClient) *FunctionBuilder {
	b.vaultLifecycle = value
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
		networkClassesClient:  privatev1.NewNetworkClassesClient(b.connection),
		externalIPPoolsClient: privatev1.NewExternalIPPoolsClient(b.connection),
		externalIPsClient:     privatev1.NewExternalIPsClient(b.connection),
		idpManager:            b.idpManager,
		vaultLifecycle:        b.vaultLifecycle,
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
	networkClassesClient  privatev1.NetworkClassesClient
	externalIPPoolsClient privatev1.ExternalIPPoolsClient
	externalIPsClient     privatev1.ExternalIPsClient
	idpManager            *idp.TenantManager
	vaultLifecycle        vault.LifecycleClient
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

	// For synced tenants, update IDP, ensure vault namespace, and check default networking readiness.
	if state == privatev1.TenantState_TENANT_STATE_SYNCED {
		if err := t.updateIDP(ctx); err != nil {
			return err
		}
		if t.tenant.GetStatus().GetState() == privatev1.TenantState_TENANT_STATE_FAILED {
			return nil
		}
		if err := t.ensureVaultNamespace(ctx); err != nil {
			return err
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

	// Skip IDP cleanup if not synced to IDP yet, but still clean up vault.
	if t.tenant.GetStatus().GetState() != privatev1.TenantState_TENANT_STATE_SYNCED {
		if err := t.deleteVaultNamespace(ctx); err != nil {
			return err
		}
		t.removeFinalizer()
		return nil
	}

	// Delete from IDP
	tenantName := t.tenant.GetStatus().GetIdpTenantName()
	if tenantName == "" {
		if err := t.deleteVaultNamespace(ctx); err != nil {
			return err
		}
		t.removeFinalizer()
		return nil
	}

	if err := t.deleteVaultNamespace(ctx); err != nil {
		return err
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
	privatev1.TenantConditionType_TENANT_CONDITION_TYPE_VAULT_READY,
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

func (t *task) isConditionTrue(conditionType privatev1.TenantConditionType) bool {
	for _, c := range t.tenant.GetStatus().GetConditions() {
		if c.GetType() == conditionType {
			return c.GetStatus() == privatev1.ConditionStatus_CONDITION_STATUS_TRUE
		}
	}
	return false
}

func (t *task) ensureVaultNamespace(ctx context.Context) error {
	condType := privatev1.TenantConditionType_TENANT_CONDITION_TYPE_VAULT_READY

	if t.r.vaultLifecycle == nil {
		return nil
	}
	if t.isBuiltin() {
		return nil
	}
	if t.isConditionTrue(condType) {
		return nil
	}

	tenantName := t.tenant.GetMetadata().GetName()
	err := t.r.vaultLifecycle.EnsureTenantNamespace(ctx, tenantName)
	if err != nil {
		t.r.logger.ErrorContext(ctx, "Failed to provision vault namespace for tenant",
			slog.String("tenant_id", t.tenant.GetId()),
			slog.String("tenant_name", tenantName),
			slog.Any("error", err),
		)
		return fmt.Errorf("failed to provision vault namespace: %w", err)
	}

	t.updateCondition(condType, privatev1.ConditionStatus_CONDITION_STATUS_TRUE,
		"NamespaceReady", "Vault namespace provisioned successfully")

	t.r.logger.DebugContext(ctx, "Vault namespace provisioned for tenant",
		slog.String("tenant_id", t.tenant.GetId()),
		slog.String("tenant_name", tenantName),
	)
	return nil
}

func (t *task) deleteVaultNamespace(ctx context.Context) error {
	if t.r.vaultLifecycle == nil {
		return nil
	}
	if t.isBuiltin() {
		return nil
	}

	tenantName := t.tenant.GetMetadata().GetName()
	err := t.r.vaultLifecycle.DeleteTenantNamespace(ctx, tenantName)
	if err != nil {
		t.r.logger.ErrorContext(ctx, "Failed to delete vault namespace for tenant",
			slog.String("tenant_id", t.tenant.GetId()),
			slog.String("tenant_name", tenantName),
			slog.Any("error", err),
		)
		return fmt.Errorf("failed to delete vault namespace: %w", err)
	}

	t.r.logger.DebugContext(ctx, "Vault namespace deleted for tenant",
		slog.String("tenant_id", t.tenant.GetId()),
		slog.String("tenant_name", tenantName),
	)
	return nil
}

const (
	defaultLabelFilter          = "this.metadata.labels['osac.openshift.io/default'] == 'true'"
	defaultLabelKey             = "osac.openshift.io/default"
	ownerReferenceAnnotationKey = "osac.openshift.io/owner-reference"
)

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

	created, err := t.ensureDefaultNetworking(ctx, tenantName, vns.GetItems(), subnets.GetItems(), sgs.GetItems(), ngs.GetItems())
	if err != nil {
		t.updateCondition(condType, privatev1.ConditionStatus_CONDITION_STATUS_FALSE,
			"ProvisioningFailed", fmt.Sprintf("Failed to provision default networking: %v", err))
		return nil
	}
	if created {
		return nil
	}

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

func (t *task) findDefaultNetworkClass(ctx context.Context) (*privatev1.NetworkClass, error) {
	if t.r.networkClassesClient == nil {
		return nil, nil
	}
	filter := "this.is_default == true"
	resp, err := t.r.networkClassesClient.List(ctx, privatev1.NetworkClassesListRequest_builder{
		Filter: &filter,
	}.Build())
	if err != nil {
		return nil, fmt.Errorf("failed to list network classes: %w", err)
	}
	for _, nc := range resp.GetItems() {
		if nc.GetSpec().GetDefaults() != nil {
			return nc, nil
		}
	}
	return nil, nil
}

func (t *task) ensureDefaultNetworking(
	ctx context.Context,
	tenantName string,
	vns []*privatev1.VirtualNetwork,
	subnets []*privatev1.Subnet,
	sgs []*privatev1.SecurityGroup,
	ngs []*privatev1.NATGateway,
) (bool, error) {
	nc, err := t.findDefaultNetworkClass(ctx)
	if err != nil {
		return false, err
	}
	if nc == nil {
		return false, nil
	}

	defaults := nc.GetSpec().GetDefaults()
	created := false

	vnID := findResourceID(vns, "default")
	if vnID == "" {
		vnID, err = t.createDefaultVirtualNetwork(ctx, tenantName, defaults, nc)
		switch {
		case err == nil:
			created = true
		case isAlreadyExists(err):
			vnID, err = t.resolveDefaultVirtualNetworkID(ctx, tenantName)
			if err != nil {
				return false, fmt.Errorf("failed to resolve existing default VirtualNetwork: %w", err)
			}
		default:
			return false, fmt.Errorf("failed to create default VirtualNetwork: %w", err)
		}
	}

	if vnID == "" {
		return created, nil
	}

	if defaults.GetSubnetIpv4Cidr() != "" && !hasResource(subnets, "default-ipv4") {
		err = t.createDefaultSubnet(ctx, tenantName, vnID, defaults.GetSubnetIpv4Cidr(), "", "default-ipv4")
		if err != nil && !isAlreadyExists(err) {
			return created, fmt.Errorf("failed to create default IPv4 Subnet: %w", err)
		}
		created = true
	}

	if defaults.GetSubnetIpv6Cidr() != "" && !hasResource(subnets, "default-ipv6") {
		err = t.createDefaultSubnet(ctx, tenantName, vnID, "", defaults.GetSubnetIpv6Cidr(), "default-ipv6")
		if err != nil && !isAlreadyExists(err) {
			return created, fmt.Errorf("failed to create default IPv6 Subnet: %w", err)
		}
		created = true
	}

	if !hasResource(sgs, "default") {
		err = t.createDefaultSecurityGroup(ctx, tenantName, vnID, defaults)
		if err != nil && !isAlreadyExists(err) {
			return created, fmt.Errorf("failed to create default SecurityGroup: %w", err)
		}
		created = true
	}

	if defaults.GetEnableNatGateway() {
		natCreated, natErr := t.ensureDefaultNATGateway(ctx, tenantName, vnID, ngs)
		if natErr != nil {
			return created, fmt.Errorf("failed to ensure default NAT gateway: %w", natErr)
		}
		created = created || natCreated
	}

	return created, nil
}

func (t *task) ensureDefaultNATGateway(
	ctx context.Context,
	tenantName, vnID string,
	ngs []*privatev1.NATGateway,
) (bool, error) {
	if t.r.externalIPsClient == nil {
		return false, nil
	}

	eipFilter := fmt.Sprintf("%s && this.metadata.tenant == %q", defaultLabelFilter, tenantName)
	eips, err := t.r.externalIPsClient.List(ctx, privatev1.ExternalIPsListRequest_builder{
		Filter: &eipFilter,
	}.Build())
	if err != nil {
		return false, fmt.Errorf("failed to list default ExternalIPs: %w", err)
	}

	allFailed := len(eips.GetItems()) > 0
	var allocatedEIPID string
	for _, eip := range eips.GetItems() {
		switch eip.GetStatus().GetState() {
		case privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED:
			allocatedEIPID = eip.GetId()
			allFailed = false
		case privatev1.ExternalIPState_EXTERNAL_IP_STATE_FAILED:
		default:
			allFailed = false
		}
	}

	if len(eips.GetItems()) == 0 || allFailed {
		err = t.createDefaultExternalIP(ctx, tenantName)
		if err != nil && !isAlreadyExists(err) {
			return false, fmt.Errorf("failed to create default ExternalIP: %w", err)
		}
		return true, nil
	}

	if hasResource(ngs, "default") {
		return false, nil
	}

	if allocatedEIPID == "" {
		return false, nil
	}

	err = t.createDefaultNATGateway(ctx, tenantName, vnID, allocatedEIPID)
	if err != nil && !isAlreadyExists(err) {
		return false, fmt.Errorf("failed to create default NATGateway: %w", err)
	}
	return true, nil
}

func (t *task) createDefaultVirtualNetwork(
	ctx context.Context,
	tenantName string,
	defaults *privatev1.NetworkDefaults,
	nc *privatev1.NetworkClass,
) (string, error) {
	vn := privatev1.VirtualNetwork_builder{
		Metadata: privatev1.Metadata_builder{
			Name:   "default",
			Tenant: tenantName,
			Labels: map[string]string{
				defaultLabelKey: "true",
			},
			Creator: "system",
		}.Build(),
		Spec: privatev1.VirtualNetworkSpec_builder{
			Region:                 "default",
			NetworkClass:           privatev1.NetworkClassReference_builder{Id: nc.GetId()}.Build(),
			ImplementationStrategy: nc.GetImplementationStrategy(),
		}.Build(),
	}.Build()

	if ipv4 := defaults.GetVirtualNetworkIpv4Cidr(); ipv4 != "" {
		vn.GetSpec().SetIpv4Cidr(ipv4)
	}
	if ipv6 := defaults.GetVirtualNetworkIpv6Cidr(); ipv6 != "" {
		vn.GetSpec().SetIpv6Cidr(ipv6)
	}

	resp, err := t.r.virtualNetworksClient.Create(ctx, privatev1.VirtualNetworksCreateRequest_builder{
		Object: vn,
	}.Build())
	if err != nil {
		return "", err
	}
	return resp.GetObject().GetId(), nil
}

func (t *task) resolveDefaultVirtualNetworkID(ctx context.Context, tenantName string) (string, error) {
	filter := fmt.Sprintf("this.metadata.name == %q && this.metadata.tenant == %q", "default", tenantName)
	resp, err := t.r.virtualNetworksClient.List(ctx, privatev1.VirtualNetworksListRequest_builder{
		Filter: &filter,
	}.Build())
	if err != nil {
		return "", fmt.Errorf("failed to list VirtualNetworks: %w", err)
	}
	if len(resp.GetItems()) == 0 {
		return "", nil
	}
	return resp.GetItems()[0].GetId(), nil
}

func (t *task) createDefaultSubnet(
	ctx context.Context,
	tenantName, vnID, ipv4CIDR, ipv6CIDR, name string,
) error {
	subnet := privatev1.Subnet_builder{
		Metadata: privatev1.Metadata_builder{
			Name:   name,
			Tenant: tenantName,
			Labels: map[string]string{
				defaultLabelKey: "true",
			},
			Annotations: map[string]string{
				ownerReferenceAnnotationKey: vnID,
			},
			Creator: "system",
		}.Build(),
		Spec: privatev1.SubnetSpec_builder{
			VirtualNetwork: privatev1.VirtualNetworkLocalReference_builder{Id: vnID}.Build(),
		}.Build(),
	}.Build()

	if ipv4CIDR != "" {
		subnet.GetSpec().SetIpv4Cidr(ipv4CIDR)
	}
	if ipv6CIDR != "" {
		subnet.GetSpec().SetIpv6Cidr(ipv6CIDR)
	}

	_, err := t.r.subnetsClient.Create(ctx, privatev1.SubnetsCreateRequest_builder{
		Object: subnet,
	}.Build())
	return err
}

func (t *task) createDefaultSecurityGroup(
	ctx context.Context,
	tenantName, vnID string,
	defaults *privatev1.NetworkDefaults,
) error {
	sg := privatev1.SecurityGroup_builder{
		Metadata: privatev1.Metadata_builder{
			Name:   "default",
			Tenant: tenantName,
			Labels: map[string]string{
				defaultLabelKey: "true",
			},
			Annotations: map[string]string{
				ownerReferenceAnnotationKey: vnID,
			},
			Creator: "system",
		}.Build(),
		Spec: privatev1.SecurityGroupSpec_builder{
			VirtualNetwork:         privatev1.VirtualNetworkLocalReference_builder{Id: vnID}.Build(),
			Ingress:                defaults.GetIngressRules(),
			Egress:                 defaults.GetEgressRules(),
			ImplementationStrategy: "network_policy",
		}.Build(),
	}.Build()

	_, err := t.r.securityGroupsClient.Create(ctx, privatev1.SecurityGroupsCreateRequest_builder{
		Object: sg,
	}.Build())
	return err
}

func (t *task) createDefaultExternalIP(ctx context.Context, tenantName string) error {
	pool, err := t.selectExternalIPPool(ctx)
	if err != nil {
		return err
	}

	eip := privatev1.ExternalIP_builder{
		Metadata: privatev1.Metadata_builder{
			Name:   "default-nat",
			Tenant: tenantName,
			Labels: map[string]string{
				defaultLabelKey: "true",
			},
			Creator: "system",
		}.Build(),
		Spec: privatev1.ExternalIPSpec_builder{
			Pool: privatev1.ExternalIPPoolReference_builder{Id: pool.GetId()}.Build(),
		}.Build(),
	}.Build()

	_, err = t.r.externalIPsClient.Create(ctx, privatev1.ExternalIPsCreateRequest_builder{
		Object: eip,
	}.Build())
	return err
}

func (t *task) selectExternalIPPool(ctx context.Context) (*privatev1.ExternalIPPool, error) {
	resp, err := t.r.externalIPPoolsClient.List(ctx, privatev1.ExternalIPPoolsListRequest_builder{}.Build())
	if err != nil {
		return nil, fmt.Errorf("failed to list ExternalIP pools: %w", err)
	}

	var best *privatev1.ExternalIPPool
	for _, pool := range resp.GetItems() {
		if pool.GetStatus().GetState() != privatev1.ExternalIPPoolState_EXTERNAL_IP_POOL_STATE_READY {
			continue
		}
		if pool.GetStatus().GetAvailable() <= 0 {
			continue
		}
		if best == nil || pool.GetStatus().GetAvailable() > best.GetStatus().GetAvailable() {
			best = pool
		}
	}
	if best == nil {
		return nil, fmt.Errorf("no READY ExternalIP pool with available capacity found")
	}
	return best, nil
}

func (t *task) createDefaultNATGateway(ctx context.Context, tenantName, vnID, externalIPID string) error {
	ng := privatev1.NATGateway_builder{
		Metadata: privatev1.Metadata_builder{
			Name:   "default",
			Tenant: tenantName,
			Labels: map[string]string{
				defaultLabelKey: "true",
			},
			Annotations: map[string]string{
				ownerReferenceAnnotationKey: vnID,
			},
			Creator: "system",
		}.Build(),
		Spec: privatev1.NATGatewaySpec_builder{
			VirtualNetwork: privatev1.VirtualNetworkLocalReference_builder{Id: vnID}.Build(),
			ExternalIp:     privatev1.ExternalIPLocalReference_builder{Id: externalIPID}.Build(),
		}.Build(),
	}.Build()

	_, err := t.r.natGatewaysClient.Create(ctx, privatev1.NATGatewaysCreateRequest_builder{
		Object: ng,
	}.Build())
	return err
}

type named interface {
	GetMetadata() *privatev1.Metadata
	GetId() string
}

func findResourceID[T named](items []T, name string) string {
	for _, item := range items {
		if item.GetMetadata().GetName() == name {
			return item.GetId()
		}
	}
	return ""
}

func hasResource[T named](items []T, name string) bool {
	for _, item := range items {
		if item.GetMetadata().GetName() == name {
			return true
		}
	}
	return false
}

func isAlreadyExists(err error) bool {
	st, ok := grpcstatus.FromError(err)
	return ok && st.Code() == grpccodes.AlreadyExists
}
