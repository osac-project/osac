/*
Copyright 2026.

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

package provisioning

import (
	"context"

	v1alpha1 "github.com/osac-project/osac-operator/api/v1alpha1"
)

type contextKey int

const (
	tenantStorageClassesKey contextKey = iota
	adminKubeconfigKey
	storageTierDefinitionsKey
	storageBackendConnectionsKey
)

// TierDefinition is the flat, AAP-schema-shaped representation of a storage tier
// (mirrors osac-aap's storage_provider role argument_specs.yaml: name/protocol/
// provider/qos_limits/quota), resolved from the Tier and Backend APIs.
type TierDefinition struct {
	Name     string
	Protocol string
	Provider string
	// BackendID is the join key into a map of BackendConnection values keyed by
	// backend_id — not the connection itself.
	BackendID string
	QosLimits TierQosLimits
	QuotaGiB  int64
}

// TierQosLimits carries the bandwidth limits for a TierDefinition's backend association.
type TierQosLimits struct {
	MaxReadBandwidthMBs  int32
	MaxWriteBandwidthMBs int32
}

// BackendConnection carries one storage backend's management-endpoint connection
// details, resolved once per unique backend_id across all tiers so credential
// material is never duplicated in the extra_vars payload.
type BackendConnection struct {
	Endpoint string
	Username string
	Password string
}

// WithTenantStorageClasses returns a context carrying the tenant's resolved
// storage classes. The AAP provider reads this when building extra_vars.
func WithTenantStorageClasses(ctx context.Context, scs []v1alpha1.ResolvedStorageClass) context.Context {
	return context.WithValue(ctx, tenantStorageClassesKey, scs)
}

// TenantStorageClassesFromContext retrieves the tenant storage classes from the
// context, or nil if not set.
func TenantStorageClassesFromContext(ctx context.Context) []v1alpha1.ResolvedStorageClass {
	scs, _ := ctx.Value(tenantStorageClassesKey).([]v1alpha1.ResolvedStorageClass)
	return scs
}

// WithAdminKubeconfig returns a context carrying the admin kubeconfig for a
// CaaS cluster. The AAP provider reads this when building extra_vars.
func WithAdminKubeconfig(ctx context.Context, kubeconfig string) context.Context {
	return context.WithValue(ctx, adminKubeconfigKey, kubeconfig)
}

// AdminKubeconfigFromContext retrieves the admin kubeconfig from the context,
// or empty string if not set.
func AdminKubeconfigFromContext(ctx context.Context) string {
	kc, _ := ctx.Value(adminKubeconfigKey).(string)
	return kc
}

// WithStorageTierDefinitions returns a context carrying the resolved storage tier
// definitions. The AAP provider reads this when building extra_vars.
func WithStorageTierDefinitions(ctx context.Context, tiers []TierDefinition) context.Context {
	return context.WithValue(ctx, storageTierDefinitionsKey, tiers)
}

// StorageTierDefinitionsFromContext retrieves the storage tier definitions from the
// context, or nil if not set.
func StorageTierDefinitionsFromContext(ctx context.Context) []TierDefinition {
	tiers, _ := ctx.Value(storageTierDefinitionsKey).([]TierDefinition)
	return tiers
}

// WithStorageBackendConnections returns a context carrying the resolved storage
// backend connection details, keyed by backend_id. The AAP provider reads this when
// building extra_vars.
func WithStorageBackendConnections(ctx context.Context, conns map[string]BackendConnection) context.Context {
	return context.WithValue(ctx, storageBackendConnectionsKey, conns)
}

// StorageBackendConnectionsFromContext retrieves the storage backend connections
// from the context, or nil if not set.
func StorageBackendConnectionsFromContext(ctx context.Context) map[string]BackendConnection {
	conns, _ := ctx.Value(storageBackendConnectionsKey).(map[string]BackendConnection)
	return conns
}
