/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package controller

import (
	configv1 "github.com/openshift/api/config/v1"
	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	ckv1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

// recordObservedClusterVersions stores live versions on ClusterOrder status.
// Missing or ambiguous observations leave the last known values unchanged.
func recordObservedClusterVersions(order *ckv1alpha1.ClusterOrder,
	hc *hypershiftv1beta1.HostedCluster, pools []hypershiftv1beta1.NodePool) {
	if hc == nil || hc.Labels[osacClusterOrderNameLabel] != order.Name {
		return
	}
	if hc.Status.Version != nil {
		for _, entry := range hc.Status.Version.History {
			if entry.State == configv1.CompletedUpdate {
				if entry.Version != "" {
					order.Status.ObservedVersion = entry.Version
				}
				break
			}
		}
	}

	requested := make(map[string]bool, len(order.Spec.NodeRequests))
	for _, request := range order.Spec.NodeRequests {
		requested[request.NodeSet] = true
	}
	statusByNodeSet := make(map[string]*ckv1alpha1.NodeRequestStatus, len(order.Status.NodeRequests))
	for i := range order.Status.NodeRequests {
		status := &order.Status.NodeRequests[i]
		if requested[status.NodeSet] && status.NumberOfNodes > 0 {
			statusByNodeSet[status.NodeSet] = status
		}
	}
	selected := make(map[string]string, len(statusByNodeSet))
	seen := make(map[string]bool, len(statusByNodeSet))
	ambiguous := make(map[string]bool)
	for i := range pools {
		pool := &pools[i]
		if pool.Namespace != hc.Namespace ||
			pool.Labels[osacClusterOrderNameLabel] != order.Name || pool.Spec.ClusterName != hc.Name {
			continue
		}
		name, ok := nodePoolNodeSet(pool)
		if !ok || statusByNodeSet[name] == nil {
			continue
		}
		if seen[name] {
			ambiguous[name] = true
		}
		seen[name] = true
		if pool.Status.Replicas > 0 {
			selected[name] = pool.Status.Version
		}
	}
	for name, version := range selected {
		if !ambiguous[name] && version != "" {
			statusByNodeSet[name].ObservedVersion = version
		}
	}
}

// syncClusterOrderObservedVersions copies the order's live observations to the
// existing output-only Cluster status fields. Empty fields preserve the
// ClusterVersion baseline or the previous live observation.
func syncClusterOrderObservedVersions(order *ckv1alpha1.ClusterOrder, remote *privatev1.Cluster) {
	if order.Status.ObservedVersion != "" {
		remote.GetStatus().SetObservedVersion(order.Status.ObservedVersion)
	}
	requested := make(map[string]bool, len(order.Spec.NodeRequests))
	for _, request := range order.Spec.NodeRequests {
		requested[request.NodeSet] = true
	}
	for _, request := range order.Status.NodeRequests {
		if request.ObservedVersion == "" || request.NumberOfNodes <= 0 ||
			!requested[request.NodeSet] ||
			remote.GetSpec().GetNodeSets()[request.NodeSet] == nil {
			continue
		}
		if status := remote.GetStatus().GetNodeSets()[request.NodeSet]; status != nil {
			status.SetObservedVersion(request.ObservedVersion)
		}
	}
}
