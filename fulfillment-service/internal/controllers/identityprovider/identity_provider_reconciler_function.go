/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package identityprovider

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"

	privatev1 "github.com/osac-project/fulfillment-service/internal/api/osac/private/v1"
	"github.com/osac-project/fulfillment-service/internal/apiclient"
	"github.com/osac-project/fulfillment-service/internal/auth"
	"github.com/osac-project/fulfillment-service/internal/controllers/finalizers"
	"github.com/osac-project/fulfillment-service/internal/idp"
	"github.com/osac-project/fulfillment-service/internal/masks"
)

// FunctionBuilder contains the data needed to build instances of the reconciler function.
type FunctionBuilder struct {
	logger     *slog.Logger
	connection *grpc.ClientConn
	idpClient  idp.ClientInterface
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

// SetIdpClient sets the IDP client that the reconciler will use to manage identity providers.
func (b *FunctionBuilder) SetIdpClient(value idp.ClientInterface) *FunctionBuilder {
	b.idpClient = value
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
	if b.idpClient == nil {
		err = errors.New("IDP client is mandatory")
		return
	}

	result = &function{
		logger:                  b.logger,
		identityProvidersClient: privatev1.NewIdentityProvidersClient(b.connection),
		idpClient:               b.idpClient,
		maskCalculator:          masks.NewCalculator().Build(),
	}
	return
}

// function is the implementation of the reconciler function.
type function struct {
	logger                  *slog.Logger
	identityProvidersClient privatev1.IdentityProvidersClient
	idpClient               idp.ClientInterface
	maskCalculator          *masks.Calculator
}

// Run executes the reconciliation logic for the given identity provider.
func (r *function) Run(ctx context.Context, identityProvider *privatev1.IdentityProvider) error {
	oldIdp := proto.Clone(identityProvider).(*privatev1.IdentityProvider)

	task := &task{
		r:                r,
		identityProvider: identityProvider,
	}

	var err error
	if identityProvider.HasMetadata() && identityProvider.GetMetadata().HasDeletionTimestamp() {
		err = task.delete(ctx)
	} else {
		err = task.update(ctx)
	}
	if err != nil {
		return err
	}

	updateMask := r.maskCalculator.Calculate(oldIdp, identityProvider)

	if len(updateMask.GetPaths()) > 0 {
		_, err = r.identityProvidersClient.Update(ctx, privatev1.IdentityProvidersUpdateRequest_builder{
			Object:     identityProvider,
			UpdateMask: updateMask,
		}.Build())
	}

	return err
}

// task contains the data needed to reconcile a single identity provider.
type task struct {
	r                *function
	identityProvider *privatev1.IdentityProvider
}

// update performs the reconciliation logic for creating or updating an identity provider.
func (t *task) update(ctx context.Context) error {
	if t.addFinalizer() {
		return nil
	}

	if err := t.validateTenant(); err != nil {
		return err
	}

	t.setDefaults()

	state := t.identityProvider.GetStatus().GetPhase()

	// Skip reconciliation for terminal error state
	if state == privatev1.IdentityProviderPhase_IDENTITY_PROVIDER_PHASE_ERROR {
		return nil
	}

	// For ready identity providers, no updates are needed
	if state == privatev1.IdentityProviderPhase_IDENTITY_PROVIDER_PHASE_READY {
		return nil
	}

	// Identity provider is UNSPECIFIED or UNKNOWN, perform initial sync to IDP
	return t.syncToIDP(ctx)
}

// syncToIDP synchronizes the identity provider to the IDP backend (Keycloak).
func (t *task) syncToIDP(ctx context.Context) error {
	// Fetch the full identity provider with secrets from the API if available
	// (events have secrets redacted for security, but tests may provide full objects directly)
	fullIdp := t.identityProvider
	if t.r.identityProvidersClient != nil {
		response, err := t.r.identityProvidersClient.Get(ctx, privatev1.IdentityProvidersGetRequest_builder{
			Id: t.identityProvider.GetId(),
		}.Build())
		if err != nil {
			return fmt.Errorf("failed to fetch identity provider: %w", err)
		}
		fullIdp = response.GetObject()
	}

	// Build the IDP provider object from the spec
	// Use tenant-prefixed alias to ensure uniqueness across tenants in Keycloak
	alias := fmt.Sprintf("%s-%s", fullIdp.GetMetadata().GetTenant(), fullIdp.GetMetadata().GetName())
	idpProvider := &idp.IdentityProvider{
		Alias:       alias,
		DisplayName: fullIdp.GetSpec().GetTitle(),
		Type:        t.determineProviderTypeFromIdp(fullIdp),
		Enabled:     fullIdp.GetSpec().GetEnabled(),
		Config:      t.buildConfigFromIdp(fullIdp),
	}

	tenantName := t.identityProvider.GetMetadata().GetTenant()
	createdIdp, err := t.r.idpClient.CreateIdentityProvider(ctx, tenantName, idpProvider)
	if err != nil {
		t.identityProvider.GetStatus().SetPhase(privatev1.IdentityProviderPhase_IDENTITY_PROVIDER_PHASE_ERROR)
		t.identityProvider.GetStatus().SetMessage(fmt.Sprintf("Identity provider creation in IDP failed: %v", err))
		return nil
	}

	t.identityProvider.GetStatus().SetPhase(privatev1.IdentityProviderPhase_IDENTITY_PROVIDER_PHASE_READY)
	t.identityProvider.GetStatus().SetMessage(fmt.Sprintf("Identity provider created successfully with alias: %s", createdIdp.Alias))

	t.r.logger.DebugContext(ctx, "Identity provider synced to IDP",
		slog.String("identity_provider_id", t.identityProvider.GetId()),
		slog.String("alias", createdIdp.Alias),
	)

	return nil
}

// determineProviderTypeFromIdp returns the provider type based on which config is set.
func (t *task) determineProviderTypeFromIdp(idp *privatev1.IdentityProvider) string {
	spec := idp.GetSpec()
	if spec.HasOidc() {
		return "oidc"
	}
	return ""
}

// buildConfigFromIdp builds the provider-specific configuration map from any identity provider object.
func (t *task) buildConfigFromIdp(idp *privatev1.IdentityProvider) map[string]string {
	config := make(map[string]string)
	spec := idp.GetSpec()

	if oidc := spec.GetOidc(); oidc != nil {
		config["authorizationUrl"] = oidc.GetAuthorizationUrl()
		config["tokenUrl"] = oidc.GetTokenUrl()
		config["clientId"] = oidc.GetClientId()
		config["clientSecret"] = oidc.GetClientSecret()
		config["issuer"] = oidc.GetIssuer()
		// Keycloak requires clientAuthMethod to be set for OIDC providers
		config["clientAuthMethod"] = "client_secret_post"
	}

	return config
}

// validateTenant verifies that the identity provider has a valid tenant assigned.
// Identity providers must belong to a specific tenant, not "shared" or "system".
func (t *task) validateTenant() error {
	if !t.identityProvider.HasMetadata() || t.identityProvider.GetMetadata().GetTenant() == "" {
		return errors.New("Identity provider must have a tenant assigned") //nolint:staticcheck // ST1005: Identity provider is an API resource name
	}
	tenant := t.identityProvider.GetMetadata().GetTenant()
	if tenant == auth.SharedTenant || tenant == auth.SystemTenant {
		return fmt.Errorf("Identity provider cannot belong to '%s' tenant - must be scoped to a specific tenant", tenant) //nolint:staticcheck // ST1005: Identity provider is an API resource name
	}
	return nil
}

// setDefaults sets default values for the identity provider.
func (t *task) setDefaults() {
	if !t.identityProvider.HasStatus() {
		t.identityProvider.SetStatus(&privatev1.IdentityProviderStatus{})
	}
	if t.identityProvider.GetStatus().GetPhase() == privatev1.IdentityProviderPhase_IDENTITY_PROVIDER_PHASE_UNSPECIFIED {
		t.identityProvider.GetStatus().SetPhase(privatev1.IdentityProviderPhase_IDENTITY_PROVIDER_PHASE_UNKNOWN)
	}
}

// addFinalizer adds the controller finalizer to the identity provider if not already present.
// Returns true if the finalizer was added (indicating the update should be saved immediately).
func (t *task) addFinalizer() bool {
	if !t.identityProvider.HasMetadata() {
		t.identityProvider.SetMetadata(&privatev1.Metadata{})
	}
	list := t.identityProvider.GetMetadata().GetFinalizers()
	if !slices.Contains(list, finalizers.Controller) {
		list = append(list, finalizers.Controller)
		t.identityProvider.GetMetadata().SetFinalizers(list)
		return true
	}
	return false
}

// removeFinalizer removes the controller finalizer from the identity provider.
func (t *task) removeFinalizer() {
	if !t.identityProvider.HasMetadata() {
		return
	}
	list := t.identityProvider.GetMetadata().GetFinalizers()
	if slices.Contains(list, finalizers.Controller) {
		list = slices.DeleteFunc(list, func(item string) bool {
			return item == finalizers.Controller
		})
		t.identityProvider.GetMetadata().SetFinalizers(list)
	}
}

// delete performs the deletion cleanup for an identity provider.
func (t *task) delete(ctx context.Context) error {
	// Skip if not in ready state (not synced to IDP yet)
	if t.identityProvider.GetStatus().GetPhase() != privatev1.IdentityProviderPhase_IDENTITY_PROVIDER_PHASE_READY {
		t.removeFinalizer()
		return nil
	}

	// Delete the identity provider from Keycloak
	// Use tenant-prefixed alias (same as in syncToIDP)
	tenantName := t.identityProvider.GetMetadata().GetTenant()
	alias := fmt.Sprintf("%s-%s", tenantName, t.identityProvider.GetMetadata().GetName())

	err := t.r.idpClient.DeleteIdentityProvider(ctx, tenantName, alias)
	if err != nil {
		// Check if this is a terminal error (not found / already deleted)
		var apiErr *apiclient.APIError
		if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound {
			// IdP already deleted - this is expected, proceed with finalizer removal
			t.r.logger.InfoContext(ctx, "Identity provider not found in IDP (already deleted)",
				slog.String("identity_provider_id", t.identityProvider.GetId()),
				slog.String("alias", alias),
			)
		} else {
			// Transient error - keep finalizer and retry
			t.r.logger.ErrorContext(ctx, "Failed to delete identity provider from IDP",
				slog.String("identity_provider_id", t.identityProvider.GetId()),
				slog.String("alias", alias),
				slog.Any("error", err),
			)
			return fmt.Errorf("failed to delete identity provider from IDP: %w", err)
		}
	} else {
		t.r.logger.InfoContext(ctx, "Identity provider deleted from IDP",
			slog.String("identity_provider_id", t.identityProvider.GetId()),
			slog.String("alias", alias),
		)
	}

	t.removeFinalizer()
	return nil
}
