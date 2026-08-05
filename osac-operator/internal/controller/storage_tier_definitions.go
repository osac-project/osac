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
	"sort"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/utils/ptr"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
	privatev1 "github.com/osac-project/osac/osac-operator/internal/api/osac/private/v1"
	"github.com/osac-project/osac/osac-operator/pkg/provisioning"
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
) ([]provisioning.TierDefinition, map[string]provisioning.BackendConnection, error) {
	log := ctrllog.FromContext(ctx)

	resp, err := tiersClient.List(ctx, privatev1.StorageTiersListRequest_builder{
		Limit: ptr.To(int32(tierListLimit)),
	}.Build())
	if err != nil {
		return nil, nil, fmt.Errorf("list storage tiers: %w", err)
	}
	if len(resp.GetItems()) >= tierListLimit {
		log.Info("storage tier list hit the unpaginated fetch limit; some tiers may be missing",
			"limit", tierListLimit)
	}

	type backendResolution struct {
		provider string
		found    bool
	}
	backendCache := make(map[string]backendResolution)
	connections := make(map[string]provisioning.BackendConnection)

	var definitions []provisioning.TierDefinition
	for _, tier := range resp.GetItems() {
		tierName := tier.GetMetadata().GetName()
		backends := tier.GetSpec().GetBackends()
		if len(backends) == 0 {
			log.Info("storage tier has no backend association, skipping", "tier", tierName)
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
					continue
				}
				return nil, nil, fmt.Errorf("get storage backend %q: %w", backendID, err)
			}
			spec := getResp.GetObject().GetSpec()
			resolution = backendResolution{provider: spec.GetProvider(), found: true}
			backendCache[backendID] = resolution
			connections[backendID] = provisioning.BackendConnection{
				Endpoint: spec.GetEndpoint(),
				Username: spec.GetCredentials().GetUsername(),
				Password: spec.GetCredentials().GetPassword(),
			}
		}
		if !resolution.found {
			continue
		}

		protocol := storageProtocolToString(assoc.GetProtocol())
		if protocol == "" {
			log.Info("storage tier has unrecognized protocol, emitting empty protocol",
				"tier", tierName, "protocol", assoc.GetProtocol())
		}

		definitions = append(definitions, provisioning.TierDefinition{
			Name:      tierName,
			Protocol:  protocol,
			Provider:  resolution.provider,
			BackendID: backendID,
			QosLimits: provisioning.TierQosLimits{
				MaxReadBandwidthMBs:  assoc.GetMaxReadBandwidthMbs(),
				MaxWriteBandwidthMBs: assoc.GetMaxWriteBandwidthMbs(),
			},
			QuotaGiB: assoc.GetQuotaGib(),
		})
	}

	return definitions, connections, nil
}

// resolveAndInjectTierContext resolves storage tier definitions and backend
// connections from the Tier API and injects them into ctx for downstream AAP
// calls to pick up, returning the tier definitions directly for callers that
// also need them locally (e.g. tier-coverage validation). A nil tiersClient
// or backendsClient (no fulfillment service connection configured) and a
// failed resolution are both treated as "no tier data" — logged and
// non-fatal — so a transient Tier/Backend API failure never blocks
// reconciliation of a resource that functioned without this data before this
// feature existed. logKV is passed through to the error/info log calls for
// caller-specific context (e.g. "tenant", name or "computeInstance", name).
func resolveAndInjectTierContext(
	ctx context.Context,
	tiersClient StorageTiersLister,
	backendsClient StorageBackendsClient,
	logKV ...any,
) (context.Context, []provisioning.TierDefinition) {
	log := ctrllog.FromContext(ctx)

	var tierDefinitions []provisioning.TierDefinition
	var backendConnections map[string]provisioning.BackendConnection
	if tiersClient != nil && backendsClient != nil {
		var err error
		tierDefinitions, backendConnections, err = resolveTierDefinitions(ctx, tiersClient, backendsClient)
		if err != nil {
			log.Error(err, "failed to resolve storage tier definitions, proceeding without tier data", logKV...)
			tierDefinitions = nil
			backendConnections = nil
		}
	} else if tiersClient != nil || backendsClient != nil {
		log.Info("storage tier resolution skipped: TiersClient and BackendsClient must both be set",
			append(logKV, "tiersClientSet", tiersClient != nil, "backendsClientSet", backendsClient != nil)...)
	}

	ctx = provisioning.WithStorageTierDefinitions(ctx, tierDefinitions)
	ctx = provisioning.WithStorageBackendConnections(ctx, backendConnections)
	return ctx, tierDefinitions
}

// storageProtocolToString maps the Tier API's StorageProtocol enum to the lowercase
// string form osac-aap's storage_provider role expects.
func storageProtocolToString(p privatev1.StorageProtocol) string {
	switch p {
	case privatev1.StorageProtocol_STORAGE_PROTOCOL_NFS:
		return "nfs"
	case privatev1.StorageProtocol_STORAGE_PROTOCOL_BLOCK:
		return "block"
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
// come from StorageClass label resolution (resolveTenantSpecificStorageClasses /
// getTenantStorageClasses — pre-existing, unaffected by the Tier API integration)
// and reflect what has actually been provisioned in the cluster. This function's
// job is to catch the gap between the two. Returns condMsg unchanged when there
// are no missing tiers (including when defined is empty because TiersClient is nil).
func (r *StorageReconciler) appendMissingTierWarnings(
	instance *v1alpha1.Tenant,
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
		r.Recorder.Eventf(instance, nil, corev1.EventTypeWarning, eventReasonMissingStorageTier, eventActionDetectMissingTier, "%s", msg)
	}

	if condMsg == "" {
		return strings.Join(messages, "; ")
	}
	return condMsg + "; " + strings.Join(messages, "; ")
}
