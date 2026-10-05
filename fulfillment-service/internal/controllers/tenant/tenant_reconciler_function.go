/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

//go:generate mockgen -destination=projects_client_mock.go -package=tenant github.com/osac-project/osac/proto/gen/osac/private/v1 ProjectsClient
//go:generate mockgen -destination=network_classes_client_mock.go -package=tenant github.com/osac-project/osac/proto/gen/osac/private/v1 NetworkClassesClient
//go:generate mockgen -destination=virtual_networks_client_mock.go -package=tenant github.com/osac-project/osac/proto/gen/osac/private/v1 VirtualNetworksClient
//go:generate mockgen -destination=subnets_client_mock.go -package=tenant github.com/osac-project/osac/proto/gen/osac/private/v1 SubnetsClient
//go:generate mockgen -destination=security_groups_client_mock.go -package=tenant github.com/osac-project/osac/proto/gen/osac/private/v1 SecurityGroupsClient
//go:generate mockgen -destination=external_ips_client_mock.go -package=tenant github.com/osac-project/osac/proto/gen/osac/private/v1 ExternalIPsClient
//go:generate mockgen -destination=nat_gateways_client_mock.go -package=tenant github.com/osac-project/osac/proto/gen/osac/private/v1 NATGatewaysClient

package tenant

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"strings"

	"google.golang.org/grpc"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/controllers/defaultnetworking"
	"github.com/osac-project/osac/fulfillment-service/internal/controllers/finalizers"
	"github.com/osac-project/osac/fulfillment-service/internal/idp"
	"github.com/osac-project/osac/fulfillment-service/internal/masks"
	"github.com/osac-project/osac/fulfillment-service/internal/vault"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

// FunctionBuilder contains the data needed to build instances of the reconciler function.
type FunctionBuilder struct {
	logger         *slog.Logger
	connection     *grpc.ClientConn
	idpManager     *idp.TenantManager
	vaultLifecycle vault.LifecycleClient
	defaultNetwork defaultnetworking.Manager
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

// SetDefaultNetworking sets the controller-owned manager for tenant default
// networking. This is optional for focused controller tests and reserved
// tenants, but is configured by the production controller process.
func (b *FunctionBuilder) SetDefaultNetworking(value defaultnetworking.Manager) *FunctionBuilder {
	b.defaultNetwork = value
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
	if b.vaultLifecycle == nil {
		err = errors.New("vault lifecycle client is mandatory")
		return
	}

	result = &function{
		logger:                b.logger,
		tenantsClient:         privatev1.NewTenantsClient(b.connection),
		projectsClient:        privatev1.NewProjectsClient(b.connection),
		networkClassesClient:  privatev1.NewNetworkClassesClient(b.connection),
		virtualNetworksClient: privatev1.NewVirtualNetworksClient(b.connection),
		subnetsClient:         privatev1.NewSubnetsClient(b.connection),
		securityGroupsClient:  privatev1.NewSecurityGroupsClient(b.connection),
		externalIPsClient:     privatev1.NewExternalIPsClient(b.connection),
		natGatewaysClient:     privatev1.NewNATGatewaysClient(b.connection),
		secretsClient:         privatev1.NewSecretsClient(b.connection),
		idpManager:            b.idpManager,
		vaultLifecycle:        b.vaultLifecycle,
		defaultNetwork:        b.defaultNetwork,
		maskCalculator:        masks.NewCalculator().Build(),
	}
	return
}

// function is the implementation of the reconciler function.
type function struct {
	logger                *slog.Logger
	tenantsClient         privatev1.TenantsClient
	projectsClient        privatev1.ProjectsClient
	networkClassesClient  privatev1.NetworkClassesClient
	virtualNetworksClient privatev1.VirtualNetworksClient
	subnetsClient         privatev1.SubnetsClient
	securityGroupsClient  privatev1.SecurityGroupsClient
	externalIPsClient     privatev1.ExternalIPsClient
	natGatewaysClient     privatev1.NATGatewaysClient
	secretsClient         privatev1.SecretsClient
	defaultNetwork        defaultnetworking.Manager
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
	r                      *function
	tenant                 *privatev1.Tenant
	defaultNetworkDefaults *privatev1.NetworkDefaults
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
		if err := t.ensureDefaultNetworking(ctx); err != nil {
			return err
		}
		return t.checkDefaultNetworkingReadiness(ctx)
	}

	// Tenant is PENDING or UNSPECIFIED, perform initial sync to IDP
	return t.syncToIDP(ctx)
}

// syncToIDP synchronizes the tenant to the identity provider.
func (t *task) syncToIDP(ctx context.Context) error {
	if t.tenant.GetStatus().GetIdpTenantName() == "" {
		t.tenant.GetStatus().SetState(privatev1.TenantState_TENANT_STATE_PENDING)

		tenantName := t.tenant.GetMetadata().GetName()
		password, err := t.resolveBreakGlassPassword(ctx)
		if err != nil {
			return err
		}
		config := &idp.TenantConfig{
			Name:               tenantName,
			Enabled:            new(!t.isBuiltin()),
			Domains:            t.tenant.GetSpec().GetDomains(),
			BreakGlassPassword: password,
		}

		credentials, err := t.r.idpManager.CreateTenant(ctx, config)
		if err != nil {
			t.tenant.GetStatus().SetState(privatev1.TenantState_TENANT_STATE_FAILED)
			t.tenant.GetStatus().SetMessage(fmt.Sprintf("Tenant creation in IDP failed: %v", err))
			return nil
		}

		// Record IDP identity before persisting the secret so a later persist
		// failure does not retry CreateTenant (the IDP tenant already exists).
		t.tenant.GetStatus().SetIdpTenantName(config.Name)
		t.tenant.GetStatus().SetBreakGlassUserId(credentials.UserID)
	}

	// Ensure the Vault namespace exists before attempting to persist the break-glass secret.
	// The secret will be stored in Vault under the tenant's namespace, so the namespace
	// must be provisioned first.
	if err := t.ensureVaultNamespace(ctx); err != nil {
		return err
	}

	if err := t.persistBreakGlassSecret(ctx); err != nil {
		t.r.logger.ErrorContext(ctx, "Failed to persist break-glass credentials secret",
			slog.String("tenant_id", t.tenant.GetId()),
			slog.Any("error", err),
		)
		// Stay PENDING with idpTenantName set so the next reconcile retries
		// persist without calling CreateTenant again.
		t.tenant.GetStatus().SetState(privatev1.TenantState_TENANT_STATE_PENDING)
		return nil
	}

	t.tenant.GetStatus().SetState(privatev1.TenantState_TENANT_STATE_SYNCED)
	t.tenant.GetStatus().ClearMessage()
	t.tenant.GetStatus().ClearBreakGlassCredentials()

	t.r.logger.DebugContext(ctx, "Tenant synced to IDP",
		slog.String("tenant_id", t.tenant.GetId()),
		slog.String("tenant_name", t.tenant.GetMetadata().GetName()),
	)

	return nil
}

// resolveBreakGlassPassword returns the break-glass password, preferring inline status credentials
// (legacy / Create fallback) and otherwise fetching data["password"] from the referenced Secret.
func (t *task) resolveBreakGlassPassword(ctx context.Context) (string, error) {
	if password := t.tenant.GetStatus().GetBreakGlassCredentials().GetPassword(); password != "" {
		return password, nil
	}
	ref := t.tenant.GetSpec().GetBreakGlassCredentialsSecret()
	if ref == nil {
		return "", nil
	}
	if t.r.secretsClient == nil {
		return "", fmt.Errorf("secrets client is required to resolve break_glass_credentials_secret")
	}
	id := ref.GetId()
	if id == "" {
		return "", fmt.Errorf("break_glass_credentials_secret must have an id")
	}
	response, err := t.r.secretsClient.Get(ctx, privatev1.SecretsGetRequest_builder{
		Id: id,
	}.Build())
	if err != nil {
		return "", fmt.Errorf("failed to fetch break-glass credentials secret: %w", err)
	}
	value, ok := response.GetObject().GetData()["password"]
	if !ok || len(value) == 0 {
		return "", fmt.Errorf("secret %q is missing data[\"password\"]", id)
	}
	return string(value), nil
}

const breakGlassSecretName = "break-glass-credentials"

// persistBreakGlassSecret stores generated break-glass credentials as a Secret and records the
// reference on the tenant spec. No-op when a reference is already set or no secrets client is
// configured (unit tests). If the Secret already exists (retry after a failed status save),
// the existing object is adopted instead of failing.
func (t *task) persistBreakGlassSecret(ctx context.Context) error {
	if t.tenant.GetSpec().GetBreakGlassCredentialsSecret() != nil {
		return nil
	}
	if t.r.secretsClient == nil {
		return nil
	}
	creds := t.tenant.GetStatus().GetBreakGlassCredentials()
	if creds.GetPassword() == "" {
		return nil
	}
	tenantName := t.tenant.GetMetadata().GetName()
	response, err := t.r.secretsClient.Create(ctx, privatev1.SecretsCreateRequest_builder{
		Object: privatev1.Secret_builder{
			Metadata: privatev1.Metadata_builder{
				Name:   breakGlassSecretName,
				Tenant: tenantName,
			}.Build(),
			Type: privatev1.SecretType_SECRET_TYPE_OPAQUE,
			Data: map[string][]byte{
				"username": []byte(creds.GetUsername()),
				"password": []byte(creds.GetPassword()),
			},
		}.Build(),
	}.Build())
	if err != nil {
		if grpcstatus.Code(err) == grpccodes.AlreadyExists {
			return t.adoptExistingBreakGlassSecret(ctx, tenantName)
		}
		return fmt.Errorf("failed to store break-glass credentials secret: %w", err)
	}
	created := response.GetObject()
	t.setBreakGlassSecretRef(created.GetId(), created.GetMetadata().GetName())
	return nil
}

func (t *task) adoptExistingBreakGlassSecret(ctx context.Context, tenantName string) error {
	filter := fmt.Sprintf(
		"this.metadata.name == %s && this.metadata.tenant == %s",
		strconv.Quote(breakGlassSecretName), strconv.Quote(tenantName),
	)
	listResp, err := t.r.secretsClient.List(ctx, privatev1.SecretsListRequest_builder{
		Filter: new(filter),
		Limit:  new(int32(1)),
	}.Build())
	if err != nil {
		return fmt.Errorf("failed to look up existing break-glass credentials secret: %w", err)
	}
	items := listResp.GetItems()
	if len(items) == 0 {
		return fmt.Errorf("break-glass credentials secret already exists but could not be found")
	}
	existing := items[0]
	t.setBreakGlassSecretRef(existing.GetId(), existing.GetMetadata().GetName())
	return nil
}

func (t *task) setBreakGlassSecretRef(id, name string) {
	ref := &privatev1.SecretLocalReference{}
	ref.SetId(id)
	ref.SetName(name)
	if !t.tenant.HasSpec() {
		t.tenant.SetSpec(&privatev1.TenantSpec{})
	}
	t.tenant.GetSpec().SetBreakGlassCredentialsSecret(ref)
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
	// Remove the break-glass secret first so its project FK does not block
	// administrators from deleting the default project. Clear the spec ref
	// afterwards so the follow-up Tenants/Update (finalizer removal) is not
	// rejected by SecretLocalReference lookup of a secret that no longer exists.
	if err := t.deleteBreakGlassSecret(ctx); err != nil {
		return err
	}
	t.clearBreakGlassSecretRef()

	// Delete the auto-created unnamed root project so it does not block tenant deletion.
	if err := t.deleteRootProject(ctx); err != nil {
		return err
	}

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

	if err := t.deleteVaultNamespace(ctx); err != nil {
		return err
	}

	// Always attempt IdP cleanup. CreateTenant may have succeeded even when
	// status never reached SYNCED (for example persist failed, or the tenant
	// was deleted mid-sync before idp_tenant_name was saved).
	tenantName := t.tenant.GetStatus().GetIdpTenantName()
	if tenantName == "" {
		tenantName = t.tenant.GetMetadata().GetName()
	}
	if tenantName != "" {
		if err := t.r.idpManager.DeleteTenant(ctx, tenantName); err != nil {
			return fmt.Errorf("failed to delete IDP tenant: %w", err)
		}
		t.r.logger.DebugContext(ctx, "Deleted tenant from IDP",
			slog.String("tenant_id", t.tenant.GetId()),
			slog.String("idp_name", tenantName),
		)
	}

	t.removeFinalizer()
	return nil
}

// deleteBreakGlassSecret removes the persisted break-glass credentials secret so
// it cannot block project or tenant deletion via foreign keys.
func (t *task) deleteBreakGlassSecret(ctx context.Context) error {
	if t.r.secretsClient == nil {
		return nil
	}
	ids, err := t.breakGlassSecretIDs(ctx)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if _, err := t.r.secretsClient.Delete(ctx, privatev1.SecretsDeleteRequest_builder{
			Id: id,
		}.Build()); err != nil {
			if grpcstatus.Code(err) == grpccodes.NotFound {
				continue
			}
			return fmt.Errorf("failed to delete break-glass credentials secret: %w", err)
		}
	}
	return nil
}

func (t *task) clearBreakGlassSecretRef() {
	if t.tenant.HasSpec() {
		t.tenant.GetSpec().ClearBreakGlassCredentialsSecret()
	}
}

func (t *task) breakGlassSecretIDs(ctx context.Context) ([]string, error) {
	if id := t.tenant.GetSpec().GetBreakGlassCredentialsSecret().GetId(); id != "" {
		return []string{id}, nil
	}
	tenantName := t.tenant.GetMetadata().GetName()
	if tenantName == "" {
		return nil, nil
	}
	filter := fmt.Sprintf(
		"this.metadata.name == %s && this.metadata.tenant == %s",
		strconv.Quote(breakGlassSecretName), strconv.Quote(tenantName),
	)
	listResp, err := t.r.secretsClient.List(ctx, privatev1.SecretsListRequest_builder{
		Filter: new(filter),
	}.Build())
	if err != nil {
		return nil, fmt.Errorf("failed to look up break-glass credentials secret: %w", err)
	}
	items := listResp.GetItems()
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.GetId())
	}
	return ids, nil
}

// countRemainingProjects returns the number of project rows that still belong
// to this tenant, including projects whose deletion is still in progress. The
// tenant reconciler must retain the IdP organization until those projects are
// archived because their controllers may still need it to remove project groups.
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

// deleteRootProject deletes the auto-created unnamed root project (empty name) that is
// automatically created for each tenant. Without this, the root project blocks tenant
// deletion because countRemainingProjects never reaches zero.
func (t *task) deleteRootProject(ctx context.Context) error {
	tenantName := t.tenant.GetMetadata().GetName()
	listFilter := fmt.Sprintf(
		"this.metadata.name == '' && this.metadata.tenant == %q",
		tenantName,
	)
	listResp, err := t.r.projectsClient.List(ctx, privatev1.ProjectsListRequest_builder{
		Filter: new(listFilter),
	}.Build())
	if err != nil {
		return fmt.Errorf("failed to list root project: %w", err)
	}
	for _, project := range listResp.GetItems() {
		if project.GetMetadata().HasDeletionTimestamp() {
			continue
		}
		_, err = t.r.projectsClient.Delete(ctx, privatev1.ProjectsDeleteRequest_builder{
			Id: project.GetId(),
		}.Build())
		if err != nil {
			if grpcstatus.Code(err) == grpccodes.NotFound {
				continue
			}
			return fmt.Errorf("failed to delete root project: %w", err)
		}
		t.r.logger.DebugContext(ctx, "Deleted root project for tenant",
			slog.String("tenant_id", t.tenant.GetId()),
			slog.String("project_id", project.GetId()),
		)
	}
	return nil
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
	defaultLabelFilter = "this.metadata.labels['osac.openshift.io/default'] == 'true'"
)

func (t *task) ensureDefaultNetworking(ctx context.Context) error {
	if t.r.defaultNetwork == nil {
		return nil
	}
	defaults, err := t.r.defaultNetwork.Ensure(ctx, t.tenant.GetMetadata().GetName())
	if err != nil {
		return err
	}
	t.defaultNetworkDefaults = defaults
	return nil
}

func (t *task) checkDefaultNetworkingReadiness(ctx context.Context) error {
	tenantName := t.tenant.GetMetadata().GetName()
	condType := privatev1.TenantConditionType_TENANT_CONDITION_TYPE_DEFAULT_NETWORKING_READY

	if tenantName == auth.SystemTenant || tenantName == auth.SharedTenant {
		t.updateCondition(condType, privatev1.ConditionStatus_CONDITION_STATUS_TRUE,
			"ReservedTenant", "Reserved tenants do not require default networking")
		return nil
	}

	if t.r.virtualNetworksClient == nil {
		return nil
	}
	defaults := t.defaultNetworkDefaults
	if t.r.defaultNetwork == nil {
		var err error
		defaults, err = t.activeNetworkDefaults(ctx)
		if err != nil {
			return err
		}
	}

	filter := fmt.Sprintf("%s && this.metadata.tenant == %q", defaultLabelFilter, tenantName)

	var pending, failed []string
	present := map[string]bool{}

	vns, err := t.r.virtualNetworksClient.List(ctx, privatev1.VirtualNetworksListRequest_builder{
		Filter: new(filter),
	}.Build())
	if err != nil {
		return fmt.Errorf("failed to list default virtual networks: %w", err)
	}
	for _, vn := range vns.GetItems() {
		name := fmt.Sprintf("VirtualNetwork/%s", vn.GetMetadata().GetName())
		present[name] = true
		switch vn.GetStatus().GetState() {
		case privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_READY:
		case privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_FAILED:
			failed = append(failed, name)
		default:
			pending = append(pending, name)
		}
	}

	subnets, err := t.r.subnetsClient.List(ctx, privatev1.SubnetsListRequest_builder{
		Filter: new(filter),
	}.Build())
	if err != nil {
		return fmt.Errorf("failed to list default subnets: %w", err)
	}
	for _, s := range subnets.GetItems() {
		name := fmt.Sprintf("Subnet/%s", s.GetMetadata().GetName())
		present[name] = true
		switch s.GetStatus().GetState() {
		case privatev1.SubnetState_SUBNET_STATE_READY:
		case privatev1.SubnetState_SUBNET_STATE_FAILED, privatev1.SubnetState_SUBNET_STATE_DELETE_FAILED:
			failed = append(failed, name)
		default:
			pending = append(pending, name)
		}
	}

	sgs, err := t.r.securityGroupsClient.List(ctx, privatev1.SecurityGroupsListRequest_builder{
		Filter: new(filter),
	}.Build())
	if err != nil {
		return fmt.Errorf("failed to list default security groups: %w", err)
	}
	for _, sg := range sgs.GetItems() {
		name := fmt.Sprintf("SecurityGroup/%s", sg.GetMetadata().GetName())
		present[name] = true
		switch sg.GetStatus().GetState() {
		case privatev1.SecurityGroupState_SECURITY_GROUP_STATE_READY:
		case privatev1.SecurityGroupState_SECURITY_GROUP_STATE_FAILED, privatev1.SecurityGroupState_SECURITY_GROUP_STATE_DELETE_FAILED:
			failed = append(failed, name)
		default:
			pending = append(pending, name)
		}
	}

	eips, err := t.r.externalIPsClient.List(ctx, privatev1.ExternalIPsListRequest_builder{
		Filter: new(filter),
	}.Build())
	if err != nil {
		return fmt.Errorf("failed to list default external IPs: %w", err)
	}
	for _, eip := range eips.GetItems() {
		name := fmt.Sprintf("ExternalIP/%s", eip.GetMetadata().GetName())
		present[name] = true
		switch eip.GetStatus().GetState() {
		case privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED:
		case privatev1.ExternalIPState_EXTERNAL_IP_STATE_FAILED:
			failed = append(failed, name)
		default:
			pending = append(pending, name)
		}
	}

	ngs, err := t.r.natGatewaysClient.List(ctx, privatev1.NATGatewaysListRequest_builder{
		Filter: new(filter),
	}.Build())
	if err != nil {
		return fmt.Errorf("failed to list default NAT gateways: %w", err)
	}
	for _, ng := range ngs.GetItems() {
		name := fmt.Sprintf("NATGateway/%s", ng.GetMetadata().GetName())
		present[name] = true
		switch ng.GetStatus().GetState() {
		case privatev1.NATGatewayState_NAT_GATEWAY_STATE_READY:
		case privatev1.NATGatewayState_NAT_GATEWAY_STATE_FAILED:
			failed = append(failed, name)
		default:
			pending = append(pending, name)
		}
	}

	defaultCount := len(vns.GetItems()) + len(subnets.GetItems()) + len(sgs.GetItems()) +
		len(eips.GetItems()) + len(ngs.GetItems())
	if defaults == nil && defaultCount == 0 {
		t.updateCondition(condType, privatev1.ConditionStatus_CONDITION_STATUS_TRUE,
			"NoDefaultNetworking", "No default networking resources configured")
		return nil
	}

	if len(failed) > 0 {
		t.updateCondition(condType, privatev1.ConditionStatus_CONDITION_STATUS_FALSE,
			"ResourceFailed", fmt.Sprintf("Default networking resources failed: %s", strings.Join(failed, ", ")))
		return nil
	}
	pending = append(pending, missingDefaultResources(defaults, present)...)
	if len(pending) > 0 {
		t.updateCondition(condType, privatev1.ConditionStatus_CONDITION_STATUS_FALSE,
			"ResourcesPending", fmt.Sprintf("Default networking resources pending: %s", strings.Join(pending, ", ")))
		return nil
	}
	t.updateCondition(condType, privatev1.ConditionStatus_CONDITION_STATUS_TRUE,
		"AllResourcesReady", "All default networking resources are ready")
	return nil
}

func (t *task) activeNetworkDefaults(ctx context.Context) (*privatev1.NetworkDefaults, error) {
	filter := "!has(this.metadata.deletion_timestamp)"
	response, err := t.r.networkClassesClient.List(ctx, privatev1.NetworkClassesListRequest_builder{
		Filter: new(filter), Limit: new(int32(2)),
	}.Build())
	if err != nil {
		return nil, fmt.Errorf("failed to list active NetworkClasses: %w", err)
	}
	if response.GetTotal() > 1 || len(response.GetItems()) > 1 {
		return nil, errors.New("multiple active NetworkClasses are configured")
	}
	if len(response.GetItems()) == 0 {
		return nil, nil
	}
	return response.GetItems()[0].GetSpec().GetDefaults(), nil
}

func missingDefaultResources(defaults *privatev1.NetworkDefaults, present map[string]bool) []string {
	required := []string{"VirtualNetwork/default", "SecurityGroup/default"}
	if defaults != nil {
		if defaults.GetSubnetIpv4Cidr() != "" {
			required = append(required, "Subnet/default-ipv4")
		}
		if defaults.GetSubnetIpv6Cidr() != "" {
			required = append(required, "Subnet/default-ipv6")
		}
		if defaults.GetEnableNatGateway() {
			required = append(required, "ExternalIP/default-nat", "NATGateway/default")
		}
	}
	var missing []string
	for _, name := range required {
		if !present[name] {
			missing = append(missing, name)
		}
	}
	return missing
}
