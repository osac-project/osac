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
	"net/url"
	"strings"

	"github.com/prometheus/client_golang/prometheus"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/collections"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	"github.com/osac-project/osac/fulfillment-service/internal/vault"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

type PrivateIdentityProvidersServerBuilder struct {
	logger            *slog.Logger
	attributionLogic  auth.AttributionLogic
	tenancyLogic      auth.TenancyLogic
	metricsRegisterer prometheus.Registerer
	filterDesc        protoreflect.MessageDescriptor
	secretStore       vault.SecretStore
}

// Query parameter names allowed in the OIDC authorization URL. Extend this list deliberately:
// every other parameter is rejected, so the URL never carries values the OIDC broker or the
// upstream provider could treat as security sensitive.
var oidcAllowedAuthorizationUrlParameters = collections.NewSet[string](
	"acr_values",
)

var _ privatev1.IdentityProvidersServer = (*PrivateIdentityProvidersServer)(nil)

type PrivateIdentityProvidersServer struct {
	privatev1.UnimplementedIdentityProvidersServer
	logger      *slog.Logger
	generic     *GenericServer[*privatev1.IdentityProvider]
	dao         *dao.GenericDAO[*privatev1.IdentityProvider]
	secretsDao  *dao.GenericDAO[*privatev1.Secret]
	secretStore vault.SecretStore
}

func NewPrivateIdentityProvidersServer() *PrivateIdentityProvidersServerBuilder {
	return &PrivateIdentityProvidersServerBuilder{}
}

func (b *PrivateIdentityProvidersServerBuilder) SetLogger(value *slog.Logger) *PrivateIdentityProvidersServerBuilder {
	b.logger = value
	return b
}

func (b *PrivateIdentityProvidersServerBuilder) SetAttributionLogic(value auth.AttributionLogic) *PrivateIdentityProvidersServerBuilder {
	b.attributionLogic = value
	return b
}

func (b *PrivateIdentityProvidersServerBuilder) SetTenancyLogic(value auth.TenancyLogic) *PrivateIdentityProvidersServerBuilder {
	b.tenancyLogic = value
	return b
}

func (b *PrivateIdentityProvidersServerBuilder) SetMetricsRegisterer(value prometheus.Registerer) *PrivateIdentityProvidersServerBuilder {
	b.metricsRegisterer = value
	return b
}

// SetFilterDesc sets the protobuf message descriptor used to validate and translate CEL filter
// expressions. This is optional. When unset, the descriptor of this server's own private message type is used.
func (b *PrivateIdentityProvidersServerBuilder) SetFilterDesc(value protoreflect.MessageDescriptor) *PrivateIdentityProvidersServerBuilder {
	b.filterDesc = value
	return b
}

// SetSecretStore sets the Vault secret store used to read the value of a Vault-backed secret
// referenced by client_secret_secret. It is optional: when unset, the create/update validation
// only sees data carried in the database column (which covers non-Vault backends and tests).
func (b *PrivateIdentityProvidersServerBuilder) SetSecretStore(value vault.SecretStore) *PrivateIdentityProvidersServerBuilder {
	b.secretStore = value
	return b
}

func (b *PrivateIdentityProvidersServerBuilder) Build() (result *PrivateIdentityProvidersServer, err error) {
	// Check parameters:
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}
	if b.tenancyLogic == nil {
		err = errors.New("tenancy logic is mandatory")
		return
	}

	// Create the server early so that we can use its functions to set up other objects:
	s := &PrivateIdentityProvidersServer{
		logger:      b.logger,
		secretStore: b.secretStore,
	}

	// Create the generic server:
	s.generic, err = NewGenericServer[*privatev1.IdentityProvider]().
		SetLogger(b.logger).
		SetService(privatev1.IdentityProviders_ServiceDesc.ServiceName).
		SetAttributionLogic(b.attributionLogic).
		SetTenancyLogic(b.tenancyLogic).
		AddAllowedTenants(auth.SystemTenant).
		SetMetricsRegisterer(b.metricsRegisterer).
		SetFilterDesc(b.filterDesc).
		Build()
	if err != nil {
		return
	}

	// Create the DAO:
	s.dao, err = dao.NewGenericDAO[*privatev1.IdentityProvider]().
		SetLogger(b.logger).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		Build()
	if err != nil {
		return
	}

	s.secretsDao, err = dao.NewGenericDAO[*privatev1.Secret]().
		SetLogger(b.logger).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		Build()
	if err != nil {
		return
	}

	// Return the server:
	result = s
	return
}

func (s *PrivateIdentityProvidersServer) Create(ctx context.Context,
	request *privatev1.IdentityProvidersCreateRequest) (response *privatev1.IdentityProvidersCreateResponse, err error) {
	if err = validateOidcUrls(oidcFrom(request.GetObject())); err != nil {
		return
	}
	if err = s.validateClientSecretSecret(ctx, request.GetObject()); err != nil {
		return
	}
	if err = validateDefaultProjectForCreate(request.GetObject().GetMetadata()); err != nil {
		return
	}
	err = s.generic.Create(ctx, request, &response)
	return
}

func (s *PrivateIdentityProvidersServer) List(ctx context.Context,
	request *privatev1.IdentityProvidersListRequest) (response *privatev1.IdentityProvidersListResponse, err error) {
	err = s.generic.List(ctx, request, &response)
	return
}

func (s *PrivateIdentityProvidersServer) Get(ctx context.Context,
	request *privatev1.IdentityProvidersGetRequest) (response *privatev1.IdentityProvidersGetResponse, err error) {
	err = s.generic.Get(ctx, request, &response)
	return
}

func (s *PrivateIdentityProvidersServer) Update(ctx context.Context,
	request *privatev1.IdentityProvidersUpdateRequest) (response *privatev1.IdentityProvidersUpdateResponse, err error) {
	mask := request.GetUpdateMask()

	// updateIncludesField treats a nil or empty mask as a full-object replacement,
	// matching the generic server, so the checks below see the whole spec exactly
	// like a create instead of skipping validation for mask-less updates.
	hasClientSecretUpdate := updateIncludesField(mask, "spec.oidc.client_secret_secret")
	hasSpecUpdate := updateIncludesField(mask, "spec")

	oidcUrlFields := []string{
		"spec.oidc.authorization_url",
		"spec.oidc.token_url",
		"spec.oidc.issuer",
		"spec.oidc.user_info_url",
		"spec.oidc.jwks_url",
		"spec.oidc.logout_url",
	}
	var urlFieldsToValidate []string
	for _, field := range oidcUrlFields {
		if updateIncludesField(mask, field) {
			urlFieldsToValidate = append(urlFieldsToValidate, field)
		}
	}

	// Validate OIDC URLs if any URL fields are being updated
	if len(urlFieldsToValidate) > 0 {
		if err = validateOidcUrlsPartial(oidcFrom(request.GetObject()), urlFieldsToValidate); err != nil {
			return
		}
	}

	// Validate client secret if needed
	if hasClientSecretUpdate {
		if err = s.validateClientSecretSecret(ctx, request.GetObject()); err != nil {
			return
		}
	}
	// Only reset phase when the client is changing spec fields (user-initiated intent change).
	// Do NOT reset when the reconciler is writing status back (would cause infinite reconcile loop).
	// Apply the reset to the merged candidate so it is persisted with the spec update without
	// adding a controller-managed status field to the caller's update mask.
	err = s.generic.UpdateWithCandidatePreparation(
		ctx,
		request,
		&response,
		func(_ context.Context, _, candidate *privatev1.IdentityProvider) error {
			if hasSpecUpdate {
				if !candidate.HasStatus() {
					candidate.SetStatus(&privatev1.IdentityProviderStatus{})
				}
				candidate.GetStatus().SetPhase(privatev1.IdentityProviderPhase_IDENTITY_PROVIDER_PHASE_UNKNOWN)
			}
			return nil
		},
	)
	return
}

func (s *PrivateIdentityProvidersServer) Delete(ctx context.Context,
	request *privatev1.IdentityProvidersDeleteRequest) (response *privatev1.IdentityProvidersDeleteResponse, err error) {
	err = s.generic.Delete(ctx, request, &response)
	return
}

func (s *PrivateIdentityProvidersServer) Signal(ctx context.Context,
	request *privatev1.IdentityProvidersSignalRequest) (response *privatev1.IdentityProvidersSignalResponse, err error) {
	err = s.generic.Signal(ctx, request, &response)
	return
}

func oidcFrom(idp *privatev1.IdentityProvider) *privatev1.OidcConfig {
	if idp == nil {
		return nil
	}
	return idp.GetSpec().GetOidc()
}

// validateOidcUrls validates all URL fields in an OIDC configuration.
// Returns an error if any URL is invalid or does not meet security requirements.
func validateOidcUrls(oidc *privatev1.OidcConfig) error {
	return validateOidcUrlsPartial(oidc, nil)
}

// validateOidcUrlsPartial validates OIDC URL fields, optionally limiting validation
// to specific fields. If fieldsToValidate is nil, all fields are validated (full
// validation for create or full replacement). If fieldsToValidate is non-nil, only
// the listed fields are validated (partial update validation).
func validateOidcUrlsPartial(oidc *privatev1.OidcConfig, fieldsToValidate []string) error {
	if oidc == nil {
		return nil
	}

	// Helper to check if a field should be validated
	shouldValidate := func(fieldName string) bool {
		if fieldsToValidate == nil {
			return true // Validate all fields
		}
		for _, f := range fieldsToValidate {
			if f == fieldName {
				return true
			}
		}
		return false
	}

	// Validate required URLs only if they're in the update mask (or validating all)
	if shouldValidate("spec.oidc.authorization_url") {
		if err := validateOidcUrl(oidc.GetAuthorizationUrl(), "spec.oidc.authorization_url", true, true); err != nil {
			return err
		}
	}
	if shouldValidate("spec.oidc.token_url") {
		if err := validateOidcUrl(oidc.GetTokenUrl(), "spec.oidc.token_url", true, false); err != nil {
			return err
		}
	}
	if shouldValidate("spec.oidc.issuer") {
		if err := validateOidcUrl(oidc.GetIssuer(), "spec.oidc.issuer", true, false); err != nil {
			return err
		}
	}

	// Validate optional URLs only if present and in update mask
	if shouldValidate("spec.oidc.user_info_url") && oidc.HasUserInfoUrl() {
		if err := validateOidcUrl(oidc.GetUserInfoUrl(), "spec.oidc.user_info_url", false, false); err != nil {
			return err
		}
	}
	if shouldValidate("spec.oidc.jwks_url") && oidc.HasJwksUrl() {
		if err := validateOidcUrl(oidc.GetJwksUrl(), "spec.oidc.jwks_url", false, false); err != nil {
			return err
		}
	}
	if shouldValidate("spec.oidc.logout_url") && oidc.HasLogoutUrl() {
		if err := validateOidcUrl(oidc.GetLogoutUrl(), "spec.oidc.logout_url", false, false); err != nil {
			return err
		}
	}

	return nil
}

// validateOidcUrl validates a single OIDC URL field.
// It ensures the URL is:
// - Not empty (if required)
// - A valid absolute URL
// - Uses HTTPS scheme (security requirement)
func validateOidcUrl(urlStr, fieldName string, required, allowQuery bool) error {
	if urlStr == "" {
		if required {
			return grpcstatus.Errorf(grpccodes.InvalidArgument, "%s is required", fieldName)
		}
		return nil
	}

	parsed, err := url.Parse(urlStr)
	if err != nil {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "%s must be a valid URL: %v", fieldName, err)
	}

	if !parsed.IsAbs() {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "%s must be an absolute URL (e.g., https://example.com/path)", fieldName)
	}

	if parsed.Scheme != "https" {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "%s must use HTTPS", fieldName)
	}

	if parsed.Hostname() == "" {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "%s must have a hostname", fieldName)
	}

	if parsed.Fragment != "" || strings.Contains(urlStr, "#") {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "%s must not contain a fragment component", fieldName)
	}

	if !allowQuery && (parsed.RawQuery != "" || parsed.ForceQuery) {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "%s must not contain a query component", fieldName)
	}

	query, err := url.ParseQuery(parsed.RawQuery)
	if err != nil {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "%s must contain valid query parameters: %v", fieldName, err)
	}
	for name, values := range query {
		if !oidcAllowedAuthorizationUrlParameters.Contains(name) {
			return grpcstatus.Errorf(grpccodes.InvalidArgument,
				"%s query parameter %q is not allowed; only acr_values is currently supported", fieldName, name)
		}
		if len(values) > 1 {
			return grpcstatus.Errorf(grpccodes.InvalidArgument,
				"%s must contain at most one %q parameter; separate multiple values with spaces", fieldName, name)
		}
	}
	return nil
}

func (s *PrivateIdentityProvidersServer) validateClientSecretSecret(
	ctx context.Context, idp *privatev1.IdentityProvider) error {
	oidc := oidcFrom(idp)
	if oidc == nil {
		return nil
	}
	ref := oidc.GetClientSecretSecret()
	if ref == nil {
		return nil
	}
	if ref.GetId() == "" && ref.GetName() == "" {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "client_secret_secret must specify id or name")
	}
	resolved, err := resolveSecretReferenceOfType(ctx, s.logger, s.secretsDao, ref,
		"client_secret_secret", privatev1.SecretType_SECRET_TYPE_VALUE)
	if err != nil {
		return err
	}
	if resolved.Tenant == auth.SharedTenant {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"shared secrets cannot be used as identity provider client_secret_secret references")
	}
	resolvedRef := &privatev1.SecretLocalReference{}
	resolvedRef.SetId(resolved.ID)
	resolvedRef.SetName(resolved.Name)
	oidc.SetClientSecretSecret(resolvedRef)
	return nil
}
