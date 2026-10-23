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

package controller

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/event"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
	"github.com/osac-project/osac/osac-operator/pkg/provisioning"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

// tierListLimit is a generous upper bound on the number of storage tiers fetched in a
// single List call. Tier catalogs are expected to be small (fast/standard/archive-scale),
// so a single unpaginated call is used instead of looping on offset/total.
const tierListLimit = 1000

// resolveTierDefinitions lists tier definitions from the Tier API and resolves each
// tier's serving provider from its first backend association (multi-backend tiers
// are not yet supported — first association wins). Backend lookups are deduplicated
// by backend_id across all tiers: each unique backend is fetched at most once
// regardless of how many tiers reference it. The same Get call also resolves that
// backend's connection details (endpoint + credentials), returned as a map keyed by
// backend_id — resolved once per unique backend, never repeated per tier, so
// credential material is never duplicated in the extra_vars payload. Values are
// extracted from the response immediately; the response itself is never retained
// or logged.
//
// A tier with no backend association, or whose backend_id resolves to NotFound, is
// skipped with a logged warning rather than failing the whole resolution — a bad
// backend on one tier must not block resolution of tiers on other, healthy backends.
func resolveTierDefinitions(
	ctx context.Context,
	tiersClient StorageTiersLister,
	backendsClient StorageBackendsClient,
	secretsClient SecretsClient,
) ([]provisioning.TierDefinition, map[string]provisioning.BackendConnection, error) {
	osacDefs, _, conns, _, err := resolveTierDefinitionsWithStatus(ctx, tiersClient, backendsClient, secretsClient)
	return osacDefs, conns, err
}

// resolveTierDefinitionsWithStatus resolves OSAC-managed and CSP-managed tier
// definitions and backend connections from the Tier and Backend APIs and
// reports whether any tier was skipped due to a missing backend association, a
// NotFound backend, or an unresolvable backend password secret.
func resolveTierDefinitionsWithStatus(
	ctx context.Context,
	tiersClient StorageTiersLister,
	backendsClient StorageBackendsClient,
	secretsClient SecretsClient,
) ([]provisioning.TierDefinition, []provisioning.TierDefinition, map[string]provisioning.BackendConnection, bool, error) {
	log := ctrllog.FromContext(ctx)

	resp, err := tiersClient.List(ctx, privatev1.StorageTiersListRequest_builder{
		Limit: ptr.To(int32(tierListLimit)),
	}.Build())
	if err != nil {
		return nil, nil, nil, false, fmt.Errorf("list storage tiers: %w", err)
	}
	if len(resp.GetItems()) >= tierListLimit {
		log.Info("storage tier list hit the unpaginated fetch limit; some tiers may be missing",
			"limit", tierListLimit)
	}

	type backendResolution struct {
		provider   string
		cspManaged bool
		found      bool
	}
	backendCache := make(map[string]backendResolution)
	connections := make(map[string]provisioning.BackendConnection)
	var skippedTiers bool

	var osacDefinitions []provisioning.TierDefinition
	var cspDefinitions []provisioning.TierDefinition
	for _, tier := range resp.GetItems() {
		tierName := tier.GetMetadata().GetName()
		backends := tier.GetSpec().GetBackends()
		if len(backends) == 0 {
			log.Info("storage tier has no backend association, skipping", "tier", tierName)
			skippedTiers = true
			continue
		}

		assoc := backends[0]
		backendID := assoc.GetBackendId()

		resolution, cached := backendCache[backendID]
		if !cached {
			getResp, err := backendsClient.Get(ctx, privatev1.StorageBackendsGetRequest_builder{
				Id: backendID,
			}.Build())
			if err != nil {
				if status.Code(err) == codes.NotFound {
					log.Info("storage backend not found, skipping tiers that reference it",
						"backendID", backendID, "tier", tierName)
					backendCache[backendID] = backendResolution{found: false}
					skippedTiers = true
					continue
				}
				return nil, nil, nil, false, fmt.Errorf("get storage backend %q: %w", backendID, err)
			}
			spec := getResp.GetObject().GetSpec()
			if !spec.GetCspManaged() {
				password, perr := resolveBackendPassword(ctx, secretsClient, spec.GetCredentials())
				if perr != nil {
					// A backend whose password cannot be resolved is skipped with a
					// logged warning (like a NotFound backend) rather than provisioned
					// with an empty password.
					log.Info("storage backend credential reference not ready, skipping tiers that reference it",
						"backendID", backendID, "tier", tierName)
					backendCache[backendID] = backendResolution{found: false}
					skippedTiers = true
					continue
				}
				connections[backendID] = provisioning.BackendConnection{
					Endpoint: spec.GetEndpoint(),
					Username: spec.GetCredentials().GetUsername(),
					Password: password,
				}
			}
			resolution = backendResolution{
				provider:   spec.GetProvider(),
				cspManaged: spec.GetCspManaged(),
				found:      true,
			}
			backendCache[backendID] = resolution
		}
		if !resolution.found {
			skippedTiers = true
			continue
		}

		protocol := storageProtocolToString(tier.GetSpec().GetProtocol())
		if !resolution.cspManaged && protocol == "" {
			log.Info("storage tier has unspecified or unrecognized protocol, emitting empty protocol",
				"tier", tierName, "protocol", tier.GetSpec().GetProtocol())
		}

		def := provisioning.TierDefinition{
			Name:      tierName,
			Protocol:  protocol,
			Provider:  resolution.provider,
			BackendID: backendID,
			QosLimits: provisioning.TierQosLimits{
				MaxReadBandwidthMBs:  assoc.GetMaxReadBandwidthMbs(),
				MaxWriteBandwidthMBs: assoc.GetMaxWriteBandwidthMbs(),
			},
		}
		if resolution.cspManaged {
			cspDefinitions = append(cspDefinitions, def)
		} else {
			osacDefinitions = append(osacDefinitions, def)
		}
	}

	return osacDefinitions, cspDefinitions, connections, skippedTiers, nil
}

// resolveBackendPassword returns the storage backend password used for AAP provisioning,
// preferring the inline credentials value and otherwise fetching data["value"] from
// the referenced Secret via the Secrets API. A backend with neither an inline password
// nor a password_secret reference yields an empty password with no error (backends that
// legitimately need no credential are unaffected). When password_secret is set the secret
// must resolve to a non-empty data["value"]; any failure is returned so the caller can
// skip that backend rather than silently provision with an empty password. The returned
// error carries only the secret id and reason — never the secret value.
func resolveBackendPassword(
	ctx context.Context,
	secretsClient SecretsClient,
	creds *privatev1.StorageBackendCredentials,
) (string, error) {
	if password := creds.GetPassword(); password != "" {
		return password, nil
	}
	ref := creds.GetPasswordSecret()
	if ref == nil {
		return "", nil
	}
	if secretsClient == nil {
		return "", fmt.Errorf("password_secret is set but no secrets client is configured")
	}
	id := ref.GetId()
	if id == "" {
		return "", fmt.Errorf("password_secret must have an id")
	}
	resp, err := secretsClient.Get(ctx, privatev1.SecretsGetRequest_builder{
		Id: id,
	}.Build())
	if err != nil {
		return "", fmt.Errorf("get password secret %q: %w", id, err)
	}
	if secretType := resp.GetObject().GetType(); secretType != privatev1.SecretType_SECRET_TYPE_VALUE {
		return "", fmt.Errorf("secret %q has type %q, expected %q", id, secretType,
			privatev1.SecretType_SECRET_TYPE_VALUE)
	}
	value, ok := resp.GetObject().GetData()["value"]
	if !ok || len(value) == 0 {
		return "", fmt.Errorf("secret %q is missing data[%q]", id, "value")
	}
	return string(value), nil
}

// resolveAndInjectTierContext resolves storage tier definitions and backend
// connections from the Tier API and injects them into ctx for downstream AAP
// calls to pick up, returning the tier definitions directly for callers that
// also need them locally (e.g. tier-coverage validation).
//
// The returned error is non-nil when both clients were configured but the
// Tier/Backend API call failed (gRPC Unavailable, Unauthenticated, etc.).
// Callers that own AAP storage provisioning must check this error and avoid
// launching jobs whose extra_vars would be missing tier/backend data. A nil
// error covers both "resolution succeeded" and "clients not configured"
// (backward-compatible).
//
// logKV is passed through to the info log call for caller-specific context
// (e.g. "tenant", name or "computeInstance", name).
func resolveAndInjectTierContext(
	ctx context.Context,
	tiersClient StorageTiersLister,
	backendsClient StorageBackendsClient,
	secretsClient SecretsClient,
	logKV ...any,
) (context.Context, []provisioning.TierDefinition, error) {
	ctx, osacTiers, _, _, err := resolveAndPartitionTierContext(ctx, tiersClient, backendsClient, secretsClient, logKV...)
	return ctx, osacTiers, err
}

// resolveAndPartitionTierContext resolves storage tier definitions and backend
// connections from the Tier and Backend APIs, partitions resolved tiers into
// OSAC-managed and CSP-managed slices, injects only OSAC-managed tiers and
// backend connections into ctx for AAP extra_vars, and reports whether any
// tier was skipped during resolution.
func resolveAndPartitionTierContext(
	ctx context.Context,
	tiersClient StorageTiersLister,
	backendsClient StorageBackendsClient,
	secretsClient SecretsClient,
	logKV ...any,
) (context.Context, []provisioning.TierDefinition, []provisioning.TierDefinition, bool, error) {
	log := ctrllog.FromContext(ctx)

	var osacTiers []provisioning.TierDefinition
	var cspTiers []provisioning.TierDefinition
	var backendConnections map[string]provisioning.BackendConnection
	var skippedTiers bool
	var resolutionErr error
	if tiersClient != nil && backendsClient != nil {
		osacTiers, cspTiers, backendConnections, skippedTiers, resolutionErr = resolveTierDefinitionsWithStatus(ctx, tiersClient, backendsClient, secretsClient)
		if resolutionErr != nil {
			osacTiers = nil
			cspTiers = nil
			backendConnections = nil
			skippedTiers = false
		}
	} else if tiersClient != nil || backendsClient != nil {
		log.Info("storage tier resolution skipped: TiersClient and BackendsClient must both be set",
			append(logKV, "tiersClientSet", tiersClient != nil, "backendsClientSet", backendsClient != nil)...)
	}

	ctx = provisioning.WithStorageTierDefinitions(ctx, osacTiers)
	ctx = provisioning.WithStorageBackendConnections(ctx, backendConnections)
	return ctx, osacTiers, cspTiers, skippedTiers, resolutionErr
}

// storageCatalogSnapshot holds the latest platform-wide StorageTier and
// StorageBackend catalog snapshot fetched from the fulfillment service.
type storageCatalogSnapshot struct {
	osacTiers          []provisioning.TierDefinition
	cspTiers           []provisioning.TierDefinition
	backendConnections map[string]provisioning.BackendConnection
	skippedTiers       bool
	backendRegistered  bool
	resolutionErr      error
}

func (s *storageCatalogSnapshot) clone() (
	[]provisioning.TierDefinition,
	[]provisioning.TierDefinition,
	map[string]provisioning.BackendConnection,
	bool,
	error,
) {
	if s.resolutionErr != nil {
		return nil, nil, nil, false, s.resolutionErr
	}
	var osacTiers []provisioning.TierDefinition
	if len(s.osacTiers) > 0 {
		osacTiers = append(make([]provisioning.TierDefinition, 0, len(s.osacTiers)), s.osacTiers...)
	}
	var cspTiers []provisioning.TierDefinition
	if len(s.cspTiers) > 0 {
		cspTiers = append(make([]provisioning.TierDefinition, 0, len(s.cspTiers)), s.cspTiers...)
	}
	var conns map[string]provisioning.BackendConnection
	if s.backendConnections != nil {
		conns = make(map[string]provisioning.BackendConnection, len(s.backendConnections))
		for k, v := range s.backendConnections {
			conns[k] = v
		}
	}
	return osacTiers, cspTiers, conns, s.skippedTiers, nil
}

func (s *storageCatalogSnapshot) equal(other *storageCatalogSnapshot) bool {
	if s == nil || other == nil {
		return s == other
	}
	if (s.resolutionErr != nil) != (other.resolutionErr != nil) {
		return false
	}
	if s.resolutionErr != nil {
		return s.resolutionErr.Error() == other.resolutionErr.Error()
	}
	return s.skippedTiers == other.skippedTiers &&
		s.backendRegistered == other.backendRegistered &&
		reflect.DeepEqual(s.osacTiers, other.osacTiers) &&
		reflect.DeepEqual(s.cspTiers, other.cspTiers) &&
		reflect.DeepEqual(s.backendConnections, other.backendConnections)
}

// syncCatalog fetches the platform-wide StorageTier and StorageBackend catalog
// once from the fulfillment service, updates the cached snapshot on r, and
// notifies the controller workqueue to reconcile all Tenants when the catalog
// snapshot changes.
func (r *StorageReconciler) syncCatalog(ctx context.Context) (bool, error) {
	if r.TiersClient == nil || r.BackendsClient == nil {
		r.catalogMu.Lock()
		r.cachedCatalog = nil
		r.catalogMu.Unlock()
		return false, nil
	}

	osacTiers, cspTiers, conns, skippedTiers, tierErr := resolveTierDefinitionsWithStatus(
		ctx, r.TiersClient, r.BackendsClient, r.SecretsClient,
	)
	var backendReg bool
	if tierErr == nil {
		backendReg, tierErr = r.fetchBackendRegistered(ctx)
	}

	var next *storageCatalogSnapshot
	if tierErr != nil {
		next = &storageCatalogSnapshot{resolutionErr: tierErr}
	} else {
		next = &storageCatalogSnapshot{
			osacTiers:          osacTiers,
			cspTiers:           cspTiers,
			backendConnections: conns,
			skippedTiers:       skippedTiers,
			backendRegistered:  backendReg,
		}
	}

	r.catalogMu.Lock()
	prev := r.cachedCatalog
	changed := !prev.equal(next)
	r.cachedCatalog = next
	eventsCh := r.catalogEvents
	r.catalogMu.Unlock()

	if changed && eventsCh != nil {
		select {
		case eventsCh <- event.TypedGenericEvent[*v1alpha1.Tenant]{}:
		default:
		}
	}
	return changed, tierErr
}

// resolveTenantTierContext returns the partitioned tier definitions and
// injects OSAC-managed tiers and backend connections into ctx, reading from
// the shared catalog snapshot when populated by the periodic catalog poller
// and falling back to a direct fetch otherwise.
func (r *StorageReconciler) resolveTenantTierContext(
	ctx context.Context,
	tenantName string,
) (context.Context, []provisioning.TierDefinition, []provisioning.TierDefinition, bool, error) {
	r.catalogMu.RLock()
	snap := r.cachedCatalog
	r.catalogMu.RUnlock()
	if snap != nil {
		osacTiers, cspTiers, conns, skippedTiers, err := snap.clone()
		ctx = provisioning.WithStorageTierDefinitions(ctx, osacTiers)
		ctx = provisioning.WithStorageBackendConnections(ctx, conns)
		return ctx, osacTiers, cspTiers, skippedTiers, err
	}
	return resolveAndPartitionTierContext(ctx, r.TiersClient, r.BackendsClient, r.SecretsClient, "tenant", tenantName)
}

type storageCatalogSyncRunnable struct {
	reconciler *StorageReconciler
	interval   time.Duration
}

// NewStorageCatalogSyncRunnable returns a manager.Runnable that periodically
// polls the fulfillment service for StorageTier and StorageBackend catalog
// changes once for the entire operator and enqueues Tenants only when the
// catalog snapshot changes.
func NewStorageCatalogSyncRunnable(reconciler *StorageReconciler, interval time.Duration) manager.Runnable {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	return &storageCatalogSyncRunnable{
		reconciler: reconciler,
		interval:   interval,
	}
}

// Start runs the periodic catalog poll loop until ctx is canceled.
func (s *storageCatalogSyncRunnable) Start(ctx context.Context) error {
	log := ctrllog.FromContext(ctx).WithName("storage-catalog-sync")
	if _, err := s.reconciler.syncCatalog(ctx); err != nil {
		log.Error(err, "initial storage catalog sync failed")
	}

	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if _, err := s.reconciler.syncCatalog(ctx); err != nil {
				log.Error(err, "periodic storage catalog sync failed")
			}
		}
	}
}

// storageProtocolToString maps the Tier API's StorageProtocol enum to the lowercase
// string form osac-aap's storage_provider role expects.
func storageProtocolToString(p privatev1.StorageProtocol) string {
	switch p {
	case privatev1.StorageProtocol_STORAGE_PROTOCOL_NFS:
		return "nfs"
	case privatev1.StorageProtocol_STORAGE_PROTOCOL_BLOCK:
		return "block"
	case privatev1.StorageProtocol_STORAGE_PROTOCOL_UNSPECIFIED:
		return ""
	default:
		return ""
	}
}

// missingTierNames returns the sorted names of tiers in defined that have no
// matching entry in resolved and are not already flagged in ambiguousTiers.
// Ambiguous tiers (multiple StorageClasses matched) are a distinct, separately
// reported problem — a tier with too many StorageClasses is not "missing."
//
// resolved and ambiguousTiers are keyed by groupByTier's lowercased tier label,
// but defined's tier.Name comes straight from the Tier API with no case
// normalization — so the covered lookup below must lowercase tier.Name to match.
func missingTierNames(defined []provisioning.TierDefinition, resolved []v1alpha1.ResolvedStorageClass, ambiguousTiers []string) []string {
	covered := make(map[string]struct{}, len(resolved)+len(ambiguousTiers))
	for _, sc := range resolved {
		covered[sc.Tier] = struct{}{}
	}
	for _, tier := range ambiguousTiers {
		covered[tier] = struct{}{}
	}

	var missing []string
	for _, tier := range defined {
		if _, ok := covered[strings.ToLower(tier.Name)]; !ok {
			missing = append(missing, tier.Name)
		}
	}
	sort.Strings(missing)
	return missing
}

// appendMissingTierWarnings emits a MissingStorageTier Warning Event for each tier
// defined by the Tier API with no matching entry in resolved or ambiguousTiers, and
// appends a human-readable summary of those tiers to condMsg. defined is the Tier
// API's source of truth for which tiers should exist; resolved and ambiguousTiers
// come from StorageClass label resolution (getTenantStorageClasses /
// getCSPStorageClasses) and reflect what has actually been provisioned in the
// cluster. This function's job is to catch the gap between the two. Returns condMsg
// unchanged when there are no missing tiers (including when defined is empty
// because TiersClient is nil).
func (r *StorageReconciler) appendMissingTierWarnings(
	tenant *v1alpha1.Tenant,
	defined []provisioning.TierDefinition,
	resolved []v1alpha1.ResolvedStorageClass,
	ambiguousTiers []string,
	condMsg string,
) string {
	names := missingTierNames(defined, resolved, ambiguousTiers)
	if len(names) == 0 {
		return condMsg
	}

	messages := make([]string, len(names))
	for i, name := range names {
		msg := fmt.Sprintf("tier %q has no StorageClass", name)
		messages[i] = msg
		r.Recorder.Eventf(tenant, nil, corev1.EventTypeWarning, eventReasonMissingStorageTier, eventActionDetectMissingTier, "%s", msg)
	}

	if condMsg == "" {
		return strings.Join(messages, "; ")
	}
	return condMsg + "; " + strings.Join(messages, "; ")
}
