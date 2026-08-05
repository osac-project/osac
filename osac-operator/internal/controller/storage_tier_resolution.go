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

	"github.com/osac-project/osac-operator/api/v1alpha1"
)

type tierResolutionResult struct {
	resolved          []v1alpha1.ResolvedStorageClass
	resolvedMessages  []string
	errorMessages     []string
	duplicateMessages []string
	// ambiguousTiers holds the name of every tier excluded from resolved because
	// multiple StorageClasses matched it (tenant-specific or Default) — a distinct,
	// separately-reported problem from a tier having no StorageClass at all.
	ambiguousTiers []string
}

func (r *tierResolutionResult) conditionMessage() string {
	parts := make([]string, 0, len(r.resolvedMessages)+len(r.errorMessages))
	parts = append(parts, r.resolvedMessages...)
	parts = append(parts, r.errorMessages...)
	return strings.Join(parts, "; ")
}

var tierLabelPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9._-]*[a-z0-9])?$`)

func joinStorageClassNames(items []storagev1.StorageClass) (joined string, names []string) {
	names = make([]string, len(items))
	for i := range items {
		names[i] = items[i].GetName()
	}
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

func getTenantStorageClasses(ctx context.Context, targetClient client.Client, tenantName string) (tierResolutionResult, error) {
	log := ctrllog.FromContext(ctx)

	tenantSCList := &storagev1.StorageClassList{}
	if err := targetClient.List(ctx, tenantSCList, client.MatchingLabels{osacTenantKey: tenantName}); err != nil {
		return tierResolutionResult{}, err
	}

	defaultSCList := &storagev1.StorageClassList{}
	if err := targetClient.List(ctx, defaultSCList, client.MatchingLabels{osacTenantKey: defaultStorageClassSentinel}); err != nil {
		return tierResolutionResult{}, err
	}

	tenantByTier := groupByTier(tenantSCList.Items)
	defaultByTier := groupByTier(defaultSCList.Items)

	allTiers := make(map[string]struct{})
	for t := range tenantByTier {
		allTiers[t] = struct{}{}
	}
	for t := range defaultByTier {
		allTiers[t] = struct{}{}
	}

	sortedTiers := make([]string, 0, len(allTiers))
	for t := range allTiers {
		sortedTiers = append(sortedTiers, t)
	}
	sort.Strings(sortedTiers)

	var result tierResolutionResult

	for _, tier := range sortedTiers {
		tenantSCs := tenantByTier[tier]
		defaultSCs := defaultByTier[tier]

		switch len(tenantSCs) {
		case 1:
			scName := tenantSCs[0].GetName()
			result.resolved = append(result.resolved, v1alpha1.ResolvedStorageClass{
				Name: scName,
				Tier: tier,
			})
			msg := fmt.Sprintf("tier %q: StorageClass %q (tenant-specific)", tier, scName)
			result.resolvedMessages = append(result.resolvedMessages, msg)
			continue
		case 0:
			// Fall through to Default resolution below.
		default:
			joined, names := joinStorageClassNames(tenantSCs)
			msg := fmt.Sprintf("tier %q: multiple tenant StorageClasses [%s]", tier, joined)
			log.Info(msg, "tenant", tenantName, "tier", tier, "storageClasses", names)
			result.errorMessages = append(result.errorMessages, msg)
			result.duplicateMessages = append(result.duplicateMessages, msg)
			result.ambiguousTiers = append(result.ambiguousTiers, tier)
			continue
		}

		switch len(defaultSCs) {
		case 1:
			scName := defaultSCs[0].GetName()
			result.resolved = append(result.resolved, v1alpha1.ResolvedStorageClass{
				Name: scName,
				Tier: tier,
			})
			msg := fmt.Sprintf("tier %q: StorageClass %q (shared Default)", tier, scName)
			result.resolvedMessages = append(result.resolvedMessages, msg)
		case 0:
			// Tier not available.
		default:
			joined, names := joinStorageClassNames(defaultSCs)
			msg := fmt.Sprintf("tier %q: multiple shared Default StorageClasses [%s]", tier, joined)
			log.Info(msg, "tenant", tenantName, "tier", tier, "storageClasses", names)
			result.errorMessages = append(result.errorMessages, msg)
			result.duplicateMessages = append(result.duplicateMessages, msg)
			result.ambiguousTiers = append(result.ambiguousTiers, tier)
		}
	}

	if len(result.resolved) == 0 && len(result.errorMessages) == 0 {
		result.errorMessages = append(result.errorMessages,
			fmt.Sprintf("no StorageClasses with %s label found for tenant %q", osacStorageTierLabel, tenantName))
	}

	return result, nil
}
