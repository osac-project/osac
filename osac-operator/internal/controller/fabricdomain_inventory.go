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
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

// fabricDomainInventoryName is an administrator-owned onboarding map in the networking
// namespace. Each data key is an exact Netris inventory hostname and its value is a
// shared BareMetalInstanceType ID. It describes hardware identity, not tenant allocation.
const fabricDomainInventoryName = "osac-fabric-domain-inventory"

const fabricDomainCatalogPageSize int32 = 100

type resolvedFabricDomainConfig struct {
	Binding v1alpha1.FabricDomainProvisioningConfig
}

// resolveFabricDomainHardware deliberately does not infer a type from label selectors:
// more than one catalog type can match a host, and an unallocated host has no BMI yet.
func (r *FabricDomainReconciler) resolveFabricDomainHardware(
	ctx context.Context, domain *v1alpha1.FabricDomain, fabricManager string,
) (string, error) {
	if r.BareMetalInstanceTypesClient == nil {
		return "", fmt.Errorf("the private BareMetalInstanceTypes client is not configured")
	}
	inventory := &corev1.ConfigMap{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: domain.Namespace, Name: fabricDomainInventoryName}, inventory); err != nil {
		return "", fmt.Errorf("reading administrator inventory %s/%s: %w", domain.Namespace, fabricDomainInventoryName, err)
	}
	if len(domain.Spec.Servers) == 0 {
		return "", fmt.Errorf("the servers list must not be empty")
	}
	memberTypes := make(map[string]string, len(domain.Spec.Servers))
	typeIDs := make([]string, 0, len(domain.Spec.Servers))
	firstServerByType := make(map[string]string, len(domain.Spec.Servers))
	for _, server := range domain.Spec.Servers {
		if server == "" || strings.TrimSpace(server) != server {
			return "", fmt.Errorf("invalid inventory hostname %q", server)
		}
		if _, duplicate := memberTypes[server]; duplicate {
			return "", fmt.Errorf("duplicate inventory hostname %q", server)
		}
		typeID := inventory.Data[server]
		if typeID == "" || strings.TrimSpace(typeID) != typeID {
			return "", fmt.Errorf("server %q has no valid BareMetalInstanceType binding in ConfigMap %s/%s", server, domain.Namespace, fabricDomainInventoryName)
		}
		memberTypes[server] = typeID
		if _, found := firstServerByType[typeID]; !found {
			typeIDs = append(typeIDs, typeID)
			firstServerByType[typeID] = server
		}
	}

	// The List API does not guarantee result ordering. Keep each filtered set
	// below its 100-item page limit, and split any short page into smaller ID
	// filters so no member depends on offset pagination.
	sort.Strings(typeIDs)
	instancesByID := make(map[string]*privatev1.BareMetalInstanceType, len(typeIDs))
	for start := 0; start < len(typeIDs); start += int(fabricDomainCatalogPageSize) {
		end := min(start+int(fabricDomainCatalogPageSize), len(typeIDs))
		batch := typeIDs[start:end]
		instanceTypeList, err := r.listFabricDomainInstanceTypes(ctx, batch)
		if err != nil {
			return "", fmt.Errorf("resolving instance types %q: %w", batch, err)
		}
		for _, instanceType := range instanceTypeList {
			if instanceType == nil {
				return "", fmt.Errorf("instance type search returned an empty item")
			}
			id := instanceType.GetId()
			if _, duplicate := instancesByID[id]; duplicate {
				return "", fmt.Errorf("instance type search returned a duplicate item")
			}
			instancesByID[id] = instanceType
		}
	}

	profiles := make(map[string]string, len(typeIDs))
	for _, typeID := range typeIDs {
		instanceType := instancesByID[typeID]
		if instanceType == nil || instanceType.GetMetadata().GetDeletionTimestamp() != nil {
			return "", fmt.Errorf("instance type %q for server %q is missing or deleting", typeID, firstServerByType[typeID])
		}
		if instanceType.GetMetadata().GetTenant() != "shared" {
			return "", fmt.Errorf("instance type %q must belong to the shared catalog", typeID)
		}
		profileRef, found := instanceType.GetSpec().GetFabricBindings().GetEthernetEw()[fabricManager]
		if !found || profileRef == "" {
			return "", fmt.Errorf("instance type %q has no Ethernet east-west profile for fabric manager %q", typeID, fabricManager)
		}
		if fabricManager == netrisFabricManager && !validNetrisID(profileRef) {
			return "", fmt.Errorf("instance type %q has invalid Netris Server Cluster template ID %q", typeID, profileRef)
		}
		profiles[typeID] = profileRef
	}

	var selectedProfile string
	for _, server := range domain.Spec.Servers {
		profileRef := profiles[memberTypes[server]]
		if selectedProfile != "" && selectedProfile != profileRef {
			return "", fmt.Errorf("incompatible Ethernet east-west profiles: server %q resolves to %q, other members resolve to %q", server, profileRef, selectedProfile)
		}
		selectedProfile = profileRef
	}
	return selectedProfile, nil
}

func (r *FabricDomainReconciler) listFabricDomainInstanceTypes(ctx context.Context, typeIDs []string) ([]*privatev1.BareMetalInstanceType, error) {
	quotedTypeIDs := make([]string, 0, len(typeIDs))
	expectedIDs := make(map[string]struct{}, len(typeIDs))
	for _, typeID := range typeIDs {
		quotedTypeIDs = append(quotedTypeIDs, strconv.Quote(typeID))
		expectedIDs[typeID] = struct{}{}
	}
	filter := fmt.Sprintf("this.id in [%s]", strings.Join(quotedTypeIDs, ", "))
	offset := int32(0)
	limit := fabricDomainCatalogPageSize
	response, err := r.BareMetalInstanceTypesClient.List(ctx, privatev1.BareMetalInstanceTypesListRequest_builder{
		Filter: &filter, Offset: &offset, Limit: &limit,
	}.Build())
	if err != nil {
		return nil, err
	}
	if response == nil {
		return nil, fmt.Errorf("instance type search returned an empty response")
	}
	items := response.GetItems()
	total, size := response.GetTotal(), response.GetSize()
	if total < 0 || total > int32(len(typeIDs)) || size < 0 || size > total || size != int32(len(items)) {
		return nil, fmt.Errorf("instance type search returned an incomplete or inconsistent result")
	}
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		if item == nil {
			return nil, fmt.Errorf("instance type search returned an empty item")
		}
		id := item.GetId()
		if _, expected := expectedIDs[id]; !expected {
			return nil, fmt.Errorf("instance type search returned an unexpected item")
		}
		if _, duplicate := seen[id]; duplicate {
			return nil, fmt.Errorf("instance type search returned a duplicate item")
		}
		seen[id] = struct{}{}
	}
	if size == total {
		return items, nil
	}
	if len(typeIDs) == 1 {
		return nil, fmt.Errorf("instance type search returned an incomplete page for a single ID")
	}
	middle := len(typeIDs) / 2
	firstHalf, err := r.listFabricDomainInstanceTypes(ctx, typeIDs[:middle])
	if err != nil {
		return nil, err
	}
	secondHalf, err := r.listFabricDomainInstanceTypes(ctx, typeIDs[middle:])
	if err != nil {
		return nil, err
	}
	return append(firstHalf, secondHalf...), nil
}

func (r *FabricDomainReconciler) mapInventoryToFabricDomains(ctx context.Context, obj client.Object) []reconcile.Request {
	if obj.GetName() != fabricDomainInventoryName || r.NetworkingNamespace != "" && obj.GetNamespace() != r.NetworkingNamespace {
		return nil
	}
	domains := &v1alpha1.FabricDomainList{}
	if err := r.List(ctx, domains, client.InNamespace(obj.GetNamespace())); err != nil {
		return nil
	}
	requests := make([]reconcile.Request, 0, len(domains.Items))
	for i := range domains.Items {
		requests = append(requests, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&domains.Items[i])})
	}
	return requests
}
