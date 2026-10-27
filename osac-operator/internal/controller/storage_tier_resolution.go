/*
Copyright 2025.

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
	"regexp"
	"sort"
	"strings"

	storagev1 "k8s.io/api/storage/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
	"github.com/osac-project/osac/osac-operator/pkg/provisioning"
)

type tierResolutionResult struct {
	resolved          []v1alpha1.ResolvedStorageClass
	resolvedMessages  []string
	errorMessages     []string
	duplicateMessages []string
	// ambiguousTiers holds the name of every tier excluded from resolved because
	// multiple StorageClasses matched it — a distinct, separately-reported problem
	// from a tier having no StorageClass at all.
	ambiguousTiers []string
}

func (r *tierResolutionResult) conditionMessage() string {
	parts := make([]string, 0, len(r.resolvedMessages)+len(r.errorMessages))
	parts = append(parts, r.resolvedMessages...)
	parts = append(parts, r.errorMessages...)
	return strings.Join(parts, "; ")
}

var tierLabelPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9._-]*[a-z0-9])?$`)

// joinStorageClassNames renders the names of one tier's StorageClasses, sorted: the
// client's List returns them in no defined order, and a condition or event message
// that reorders its names between reconciles reads as a new problem.
func joinStorageClassNames(items []storagev1.StorageClass) (joined string, names []string) {
	names = make([]string, len(items))
	for i := range items {
		names[i] = items[i].GetName()
	}
	sort.Strings(names)
	return strings.Join(names, ", "), names
}

func groupByTier(scList []storagev1.StorageClass) map[string][]storagev1.StorageClass {
	groups := make(map[string][]storagev1.StorageClass)
	for _, sc := range scList {
		raw, exists := sc.GetLabels()[osacStorageTierLabel]
		if !exists || raw == "" {
			continue
		}
		tier := strings.ToLower(raw)
		if !tierLabelPattern.MatchString(tier) {
			continue
		}
		groups[tier] = append(groups[tier], sc)
	}
	return groups
}

func resolveLabeledStorageClasses(
	ctx context.Context,
	targetClient client.Client,
	tenantLabelValue, resolvedScope, duplicateScope string,
	expectedTiers map[string]struct{},
) (tierResolutionResult, error) {
	log := ctrllog.FromContext(ctx)

	scList := &storagev1.StorageClassList{}
	if err := targetClient.List(ctx, scList, client.MatchingLabels{osacTenantKey: tenantLabelValue}); err != nil {
		return tierResolutionResult{}, err
	}

	byTier := groupByTier(scList.Items)

	sortedTiers := make([]string, 0, len(byTier))
	for tier := range byTier {
		if expectedTiers != nil {
			if _, ok := expectedTiers[tier]; !ok {
				continue
			}
		}
		sortedTiers = append(sortedTiers, tier)
	}
	sort.Strings(sortedTiers)

	var result tierResolutionResult
	for _, tier := range sortedTiers {
		scs := byTier[tier]
		if len(scs) == 1 {
			scName := scs[0].GetName()
			result.resolved = append(result.resolved, v1alpha1.ResolvedStorageClass{
				Name: scName,
				Tier: tier,
			})
			msg := fmt.Sprintf("tier %q: StorageClass %q (%s)", tier, scName, resolvedScope)
			result.resolvedMessages = append(result.resolvedMessages, msg)
		} else {
			joined, names := joinStorageClassNames(scs)
			msg := fmt.Sprintf("tier %q: multiple %s StorageClasses [%s]", tier, duplicateScope, joined)
			log.Info(msg, "tenant", tenantLabelValue, "tier", tier, "storageClasses", names)
			result.errorMessages = append(result.errorMessages, msg)
			result.duplicateMessages = append(result.duplicateMessages, msg)
			result.ambiguousTiers = append(result.ambiguousTiers, tier)
		}
	}

	return result, nil
}

func getTenantStorageClasses(ctx context.Context, targetClient client.Client, tenantName string) (tierResolutionResult, error) {
	result, err := resolveLabeledStorageClasses(ctx, targetClient, tenantName, "tenant-specific", "tenant", nil)
	if err != nil {
		return tierResolutionResult{}, err
	}

	if len(result.resolved) == 0 && len(result.errorMessages) == 0 {
		result.errorMessages = append(result.errorMessages,
			fmt.Sprintf("no StorageClasses with %s label found for tenant %q", osacStorageTierLabel, tenantName))
	}

	return result, nil
}

func tierNameSet(tiers []provisioning.TierDefinition) map[string]struct{} {
	set := make(map[string]struct{}, len(tiers))
	for _, t := range tiers {
		set[strings.ToLower(t.Name)] = struct{}{}
	}
	return set
}

// getCSPStorageClasses resolves StorageClasses labeled osac.openshift.io/tenant=csp-managed
// for the configured CSP-managed storage tiers.
func getCSPStorageClasses(
	ctx context.Context,
	targetClient client.Client,
	cspTiers []provisioning.TierDefinition,
) (tierResolutionResult, error) {
	return resolveLabeledStorageClasses(ctx, targetClient, cspManagedTenantValue, cspManagedTenantValue, cspManagedTenantValue, tierNameSet(cspTiers))
}
