/*
Copyright (c) 2025 Red Hat Inc.

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

	"github.com/prometheus/client_golang/prometheus"

	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	"github.com/osac-project/osac/fulfillment-service/internal/vault"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

type PrivateClusterTemplatesServerBuilder struct {
	logger            *slog.Logger
	attributionLogic  auth.AttributionLogic
	tenancyLogic      auth.TenancyLogic
	metricsRegisterer prometheus.Registerer
	filterDesc        protoreflect.MessageDescriptor
	secretStore       vault.SecretStore
}

var _ privatev1.ClusterTemplatesServer = (*PrivateClusterTemplatesServer)(nil)

type PrivateClusterTemplatesServer struct {
	privatev1.UnimplementedClusterTemplatesServer
	logger             *slog.Logger
	clusterVersionsDao *dao.GenericDAO[*privatev1.ClusterVersion]
	secretsDao         *dao.GenericDAO[*privatev1.Secret]
	secretStore        vault.SecretStore
	generic            *GenericServer[*privatev1.ClusterTemplate]
}

func NewPrivateClusterTemplatesServer() *PrivateClusterTemplatesServerBuilder {
	return &PrivateClusterTemplatesServerBuilder{}
}

func (b *PrivateClusterTemplatesServerBuilder) SetLogger(value *slog.Logger) *PrivateClusterTemplatesServerBuilder {
	b.logger = value
	return b
}

func (b *PrivateClusterTemplatesServerBuilder) SetAttributionLogic(value auth.AttributionLogic) *PrivateClusterTemplatesServerBuilder {
	b.attributionLogic = value
	return b
}

func (b *PrivateClusterTemplatesServerBuilder) SetTenancyLogic(value auth.TenancyLogic) *PrivateClusterTemplatesServerBuilder {
	b.tenancyLogic = value
	return b
}

// SetSecretStore sets the store used to validate Vault-backed SSH-key Secret defaults.
func (b *PrivateClusterTemplatesServerBuilder) SetSecretStore(value vault.SecretStore) *PrivateClusterTemplatesServerBuilder {
	b.secretStore = value
	return b
}

// SetMetricsRegisterer sets the Prometheus registerer used to register the metrics for the underlying database
// access objects. This is optional. If not set, no metrics will be recorded.
func (b *PrivateClusterTemplatesServerBuilder) SetMetricsRegisterer(value prometheus.Registerer) *PrivateClusterTemplatesServerBuilder {
	b.metricsRegisterer = value
	return b
}

// SetFilterDesc sets the protobuf message descriptor used to validate and translate CEL filter
// expressions. This is optional. When unset, the descriptor of this server's own private message type is used.
func (b *PrivateClusterTemplatesServerBuilder) SetFilterDesc(value protoreflect.MessageDescriptor) *PrivateClusterTemplatesServerBuilder {
	b.filterDesc = value
	return b
}

func (b *PrivateClusterTemplatesServerBuilder) Build() (result *PrivateClusterTemplatesServer, err error) {
	// Check parameters:
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}
	if b.tenancyLogic == nil {
		err = errors.New("tenancy logic is mandatory")
		return
	}

	// Create the cluster versions DAO:
	clusterVersionsDao, err := dao.NewGenericDAO[*privatev1.ClusterVersion]().
		SetLogger(b.logger).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		Build()
	if err != nil {
		return
	}

	secretsDao, err := dao.NewGenericDAO[*privatev1.Secret]().
		SetLogger(b.logger).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		Build()
	if err != nil {
		return
	}

	// Create the generic server:
	generic, err := NewGenericServer[*privatev1.ClusterTemplate]().
		SetLogger(b.logger).
		SetService(privatev1.ClusterTemplates_ServiceDesc.ServiceName).
		SetAttributionLogic(b.attributionLogic).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		SetFilterDesc(b.filterDesc).
		AddAllowedTenants(auth.SharedTenant).
		Build()
	if err != nil {
		return
	}

	// Create and populate the object:
	result = &PrivateClusterTemplatesServer{
		logger:             b.logger,
		clusterVersionsDao: clusterVersionsDao,
		secretsDao:         secretsDao,
		secretStore:        b.secretStore,
		generic:            generic,
	}
	return
}

func (s *PrivateClusterTemplatesServer) List(ctx context.Context,
	request *privatev1.ClusterTemplatesListRequest) (response *privatev1.ClusterTemplatesListResponse, err error) {
	err = s.generic.List(ctx, request, &response)
	return
}

func (s *PrivateClusterTemplatesServer) Get(ctx context.Context,
	request *privatev1.ClusterTemplatesGetRequest) (response *privatev1.ClusterTemplatesGetResponse, err error) {
	err = s.generic.Get(ctx, request, &response)
	return
}

func (s *PrivateClusterTemplatesServer) Create(ctx context.Context,
	request *privatev1.ClusterTemplatesCreateRequest) (response *privatev1.ClusterTemplatesCreateResponse, err error) {
	err = s.generic.CreateWithCandidatePreparation(ctx, request, &response,
		func(ctx context.Context, _ *privatev1.ClusterTemplate, candidate *privatev1.ClusterTemplate) error {
			if err := s.validateSpecDefaults(ctx, candidate); err != nil {
				return err
			}
			if candidate.GetMetadata().GetName() == "" && candidate.GetId() != "" {
				candidate.GetMetadata().SetName(templateNameFromID(candidate.GetId()))
			}
			return nil
		})
	return
}

func (s *PrivateClusterTemplatesServer) Update(ctx context.Context,
	request *privatev1.ClusterTemplatesUpdateRequest) (response *privatev1.ClusterTemplatesUpdateResponse, err error) {
	err = s.generic.UpdateWithCandidatePreparation(ctx, request, &response,
		func(ctx context.Context, _ *privatev1.ClusterTemplate, candidate *privatev1.ClusterTemplate) error {
			if !updateIncludesField(request.GetUpdateMask(), "spec_defaults") {
				return nil
			}
			return s.validateSpecDefaults(ctx, candidate)
		})
	return
}

func (s *PrivateClusterTemplatesServer) validateSpecDefaults(
	ctx context.Context, template *privatev1.ClusterTemplate,
) error {
	if err := s.validateSpecDefaultsVersion(ctx, template); err != nil {
		return err
	}
	if err := s.validateSpecDefaultsPullSecret(ctx, template); err != nil {
		return err
	}
	return s.validateSpecDefaultsSSHKey(ctx, template)
}

func (s *PrivateClusterTemplatesServer) validateSpecDefaultsPullSecret(
	ctx context.Context, template *privatev1.ClusterTemplate) error {
	ref := template.GetSpecDefaults().GetPullSecretSecret()
	if ref == nil {
		return nil
	}
	resolved, err := resolveSecretReferenceOfType(ctx, s.logger, s.secretsDao, ref,
		"spec_defaults.pull_secret_secret", privatev1.SecretType_SECRET_TYPE_PULL_SECRET)
	if err != nil {
		return err
	}
	resolvedRef := &privatev1.SecretLocalReference{}
	resolvedRef.SetId(resolved.ID)
	resolvedRef.SetName(resolved.Name)
	template.GetSpecDefaults().SetPullSecretSecret(resolvedRef)
	return nil
}

// validateSpecDefaultsSSHKey resolves the template's registered SSH-key
// default with the same tenant/project rules used by VMaaS and BMaaS.
func (s *PrivateClusterTemplatesServer) validateSpecDefaultsSSHKey(
	ctx context.Context, template *privatev1.ClusterTemplate) error {
	ref := template.GetSpecDefaults().GetSshKey()
	if ref == nil {
		return nil
	}
	resolved, err := resolveTenantScopedSSHKey(ctx, s.logger, s.secretsDao, s.secretStore,
		template.GetMetadata().GetTenant(), ref, "spec_defaults.ssh_key")
	if err != nil {
		return err
	}
	template.GetSpecDefaults().SetSshKey(resolved.reference)
	return nil
}

func (s *PrivateClusterTemplatesServer) validateSpecDefaultsVersion(
	ctx context.Context, template *privatev1.ClusterTemplate,
) error {
	versionRef := template.GetSpecDefaults().GetVersion()
	if versionRef == nil || versionRef.GetName() == "" {
		return nil
	}
	return lookupAndValidateClusterVersion(ctx, s.logger, s.clusterVersionsDao, versionRef.GetName())
}

func (s *PrivateClusterTemplatesServer) Delete(ctx context.Context,
	request *privatev1.ClusterTemplatesDeleteRequest) (response *privatev1.ClusterTemplatesDeleteResponse, err error) {
	err = s.generic.Delete(ctx, request, &response)
	return
}

func (s *PrivateClusterTemplatesServer) Signal(ctx context.Context,
	request *privatev1.ClusterTemplatesSignalRequest) (response *privatev1.ClusterTemplatesSignalResponse, err error) {
	err = s.generic.Signal(ctx, request, &response)
	return
}
