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
	"sort"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	bmfov1alpha1 "github.com/osac-project/osac/bare-metal-fulfillment-operator/api/v1alpha1"
	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
)

// attachedSubnetScope holds subnet CR names and IPv4 CIDRs derived from workload
// network attachments that reference a SecurityGroup.
type attachedSubnetScope struct {
	Refs  []string
	CIDRs []string
}

// deriveAttachedSubnetScope scans ComputeInstance, ClusterOrder, and BareMetalInstance
// attachments in the configured namespaces and returns unique Ready subnet refs and
// CIDRs for the given SecurityGroup. pending is true when an attached subnet exists
// but is not yet Ready.
func (r *SecurityGroupReconciler) deriveAttachedSubnetScope(
	ctx context.Context,
	sg *v1alpha1.SecurityGroup,
) (attachedSubnetScope, bool, error) {
	refSet := make(map[string]struct{})

	if err := r.collectComputeInstanceSubnetRefs(ctx, sg, refSet); err != nil {
		return attachedSubnetScope{}, false, err
	}
	if err := r.collectClusterOrderSubnetRefs(ctx, sg, refSet); err != nil {
		return attachedSubnetScope{}, false, err
	}
	if r.BareMetalInstanceEnabled {
		if err := r.collectBareMetalInstanceSubnetRefs(ctx, sg, refSet); err != nil {
			return attachedSubnetScope{}, false, err
		}
	}

	refs := sortedKeys(refSet)
	if len(refs) == 0 {
		return attachedSubnetScope{}, false, nil
	}

	scope := attachedSubnetScope{Refs: make([]string, 0, len(refs)), CIDRs: make([]string, 0, len(refs))}
	for _, ref := range refs {
		subnet, ready, err := r.resolveAttachedSubnet(ctx, sg.Namespace, sg.Spec.VirtualNetwork, ref)
		if err != nil {
			return attachedSubnetScope{}, false, err
		}
		if !ready {
			return attachedSubnetScope{}, true, nil
		}
		scope.Refs = append(scope.Refs, subnet.Name)
		scope.CIDRs = append(scope.CIDRs, subnet.Spec.IPv4CIDR)
	}

	return scope, false, nil
}

func (r *SecurityGroupReconciler) collectComputeInstanceSubnetRefs(
	ctx context.Context,
	sg *v1alpha1.SecurityGroup,
	refSet map[string]struct{},
) error {
	list := &v1alpha1.ComputeInstanceList{}
	if err := r.List(ctx, list, client.InNamespace(r.ComputeInstanceNamespace)); err != nil {
		return fmt.Errorf("listing ComputeInstances: %w", err)
	}
	for i := range list.Items {
		for _, att := range list.Items[i].Spec.NetworkAttachments {
			if securityGroupRefsContains(att.SecurityGroupRefs, sg.Name) {
				refSet[att.SubnetRef] = struct{}{}
			}
		}
	}
	return nil
}

func (r *SecurityGroupReconciler) collectClusterOrderSubnetRefs(
	ctx context.Context,
	sg *v1alpha1.SecurityGroup,
	refSet map[string]struct{},
) error {
	list := &v1alpha1.ClusterOrderList{}
	if err := r.List(ctx, list, client.InNamespace(r.ClusterOrderNamespace)); err != nil {
		return fmt.Errorf("listing ClusterOrders: %w", err)
	}
	for i := range list.Items {
		att := list.Items[i].Spec.NetworkAttachment
		if att == nil {
			continue
		}
		if securityGroupRefsContains(att.SecurityGroupRefs, sg.Name) {
			refSet[att.SubnetRef] = struct{}{}
		}
	}
	return nil
}

func (r *SecurityGroupReconciler) collectBareMetalInstanceSubnetRefs(
	ctx context.Context,
	sg *v1alpha1.SecurityGroup,
	refSet map[string]struct{},
) error {
	sgUUID := sg.Labels[osacSecurityGroupIDLabel]
	if sgUUID == "" {
		return nil
	}

	list := &bmfov1alpha1.BareMetalInstanceList{}
	if err := r.List(ctx, list, client.InNamespace(r.BaremetalInstanceNamespace)); err != nil {
		return fmt.Errorf("listing BareMetalInstances: %w", err)
	}
	for i := range list.Items {
		for _, att := range list.Items[i].Spec.NetworkAttachments {
			if securityGroupRefsContains(att.SecurityGroupRefs, sgUUID) {
				refSet[att.SubnetRef] = struct{}{}
			}
		}
	}
	return nil
}

func (r *SecurityGroupReconciler) resolveAttachedSubnet(
	ctx context.Context,
	namespace, virtualNetwork, ref string,
) (*v1alpha1.Subnet, bool, error) {
	subnet := &v1alpha1.Subnet{}
	err := r.Get(ctx, client.ObjectKey{Namespace: namespace, Name: ref}, subnet)
	if apierrors.IsNotFound(err) {
		subnetList := &v1alpha1.SubnetList{}
		if err := r.List(ctx, subnetList,
			client.InNamespace(namespace),
			client.MatchingLabels{osacSubnetIDLabel: ref},
		); err != nil {
			return nil, false, fmt.Errorf("listing Subnet by uuid label %q: %w", ref, err)
		}
		if len(subnetList.Items) == 0 {
			return nil, false, fmt.Errorf("subnet %q not found in namespace %q", ref, namespace)
		}
		if len(subnetList.Items) > 1 {
			return nil, false, fmt.Errorf("expected one Subnet with uuid %q, found %d", ref, len(subnetList.Items))
		}
		subnet = subnetList.Items[0].DeepCopy()
	} else if err != nil {
		return nil, false, fmt.Errorf("getting Subnet %q: %w", ref, err)
	}

	if subnet.Spec.VirtualNetwork != virtualNetwork {
		return nil, false, fmt.Errorf(
			"subnet %q belongs to VirtualNetwork %q, expected %q",
			subnet.Name, subnet.Spec.VirtualNetwork, virtualNetwork,
		)
	}
	if subnet.Spec.IPv4CIDR == "" {
		return nil, false, fmt.Errorf("subnet %q has no ipv4Cidr", subnet.Name)
	}
	if subnet.Status.Phase != v1alpha1.SubnetPhaseReady {
		return subnet, false, nil
	}
	return subnet, true, nil
}

func securityGroupRefsContains(refs []string, target string) bool {
	for _, ref := range refs {
		if ref == target {
			return true
		}
	}
	return false
}

func sortedKeys(set map[string]struct{}) []string {
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// mapAttachmentsToSecurityGroups enqueues SecurityGroup reconcile requests when a
// workload network attachment references a SecurityGroup.
func (r *SecurityGroupReconciler) mapAttachmentsToSecurityGroups(
	ctx context.Context,
	obj client.Object,
) []reconcile.Request {
	requests := make([]reconcile.Request, 0)

	switch o := obj.(type) {
	case *v1alpha1.ComputeInstance:
		for _, att := range o.Spec.NetworkAttachments {
			for _, sgRef := range att.SecurityGroupRefs {
				requests = append(requests, reconcile.Request{
					NamespacedName: client.ObjectKey{Namespace: r.NetworkingNamespace, Name: sgRef},
				})
			}
		}
	case *v1alpha1.ClusterOrder:
		if o.Spec.NetworkAttachment != nil {
			for _, sgRef := range o.Spec.NetworkAttachment.SecurityGroupRefs {
				requests = append(requests, reconcile.Request{
					NamespacedName: client.ObjectKey{Namespace: r.NetworkingNamespace, Name: sgRef},
				})
			}
		}
	case *bmfov1alpha1.BareMetalInstance:
		for _, att := range o.Spec.NetworkAttachments {
			for _, sgUUID := range att.SecurityGroupRefs {
				sgList := &v1alpha1.SecurityGroupList{}
				if err := r.List(ctx, sgList,
					client.InNamespace(r.NetworkingNamespace),
					client.MatchingLabels{osacSecurityGroupIDLabel: sgUUID},
				); err != nil {
					continue
				}
				for i := range sgList.Items {
					requests = append(requests, reconcile.Request{
						NamespacedName: client.ObjectKeyFromObject(&sgList.Items[i]),
					})
				}
			}
		}
	}

	return dedupeReconcileRequests(requests)
}

func dedupeReconcileRequests(requests []reconcile.Request) []reconcile.Request {
	if len(requests) <= 1 {
		return requests
	}
	seen := make(map[string]struct{}, len(requests))
	unique := make([]reconcile.Request, 0, len(requests))
	for _, req := range requests {
		key := req.Namespace + "/" + req.Name
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		unique = append(unique, req)
	}
	return unique
}
