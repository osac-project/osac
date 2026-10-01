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

type resolvedFabricDomainConfig struct {
	Binding       v1alpha1.FabricDomainProvisioningConfig
	InstanceTypes map[string]string
}

// resolveFabricDomainHardware deliberately does not infer a type from label selectors:
// more than one catalog type can match a host, and an unallocated host has no BMI yet.
func (r *FabricDomainReconciler) resolveFabricDomainHardware(
	ctx context.Context, domain *v1alpha1.FabricDomain, networkClassID string,
) (string, map[string]string, error) {
	if r.BareMetalInstanceTypesClient == nil {
		return "", nil, fmt.Errorf("the private BareMetalInstanceTypes client is not configured")
	}
	inventory := &corev1.ConfigMap{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: domain.Namespace, Name: fabricDomainInventoryName}, inventory); err != nil {
		return "", nil, fmt.Errorf("reading administrator inventory %s/%s: %w", domain.Namespace, fabricDomainInventoryName, err)
	}
	if len(domain.Spec.Servers) == 0 {
		return "", nil, fmt.Errorf("the servers list must not be empty")
	}
	templates := make(map[string]string)
	memberTypes := make(map[string]string, len(domain.Spec.Servers))
	var selectedTemplate string
	for _, server := range domain.Spec.Servers {
		if server == "" || strings.TrimSpace(server) != server {
			return "", nil, fmt.Errorf("invalid inventory hostname %q", server)
		}
		if _, duplicate := memberTypes[server]; duplicate {
			return "", nil, fmt.Errorf("duplicate inventory hostname %q", server)
		}
		typeID := inventory.Data[server]
		if typeID == "" || strings.TrimSpace(typeID) != typeID {
			return "", nil, fmt.Errorf("server %q has no valid BareMetalInstanceType binding in ConfigMap %s/%s", server, domain.Namespace, fabricDomainInventoryName)
		}
		memberTypes[server] = typeID
		templateID, found := templates[typeID]
		if !found {
			response, err := r.BareMetalInstanceTypesClient.Get(ctx, privatev1.BareMetalInstanceTypesGetRequest_builder{Id: typeID}.Build())
			if err != nil {
				return "", nil, fmt.Errorf("resolving instance type %q for server %q: %w", typeID, server, err)
			}
			instanceType := response.GetObject()
			if instanceType == nil || instanceType.GetMetadata().GetDeletionTimestamp() != nil {
				return "", nil, fmt.Errorf("instance type %q for server %q is missing or deleting", typeID, server)
			}
			if instanceType.GetMetadata().GetTenant() != "shared" {
				return "", nil, fmt.Errorf("instance type %q must belong to the shared catalog", typeID)
			}
			binding := instanceType.GetSpec().GetFabricBindings().GetEthernetEw().GetNetris()
			if binding == nil || binding.GetNetworkClass() != networkClassID {
				return "", nil, fmt.Errorf("instance type %q has no Netris Ethernet binding for NetworkClass %q", typeID, networkClassID)
			}
			templateID = binding.GetTemplateId()
			if !validNetrisID(templateID) {
				return "", nil, fmt.Errorf("instance type %q has invalid Netris template ID %q", typeID, templateID)
			}
			templates[typeID] = templateID
		}
		if selectedTemplate != "" && selectedTemplate != templateID {
			return "", nil, fmt.Errorf("incompatible Ethernet templates: server %q resolves to %q, other members resolve to %q", server, templateID, selectedTemplate)
		}
		selectedTemplate = templateID
	}
	return selectedTemplate, memberTypes, nil
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
