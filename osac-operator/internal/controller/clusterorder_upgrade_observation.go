/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package controller

import (
	"encoding/json"

	configv1 "github.com/openshift/api/config/v1"
	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
)

// recordUpgradeObservations refreshes each component's observation from its
// stamped HyperShift resource.
func recordUpgradeObservations(order *v1alpha1.ClusterOrder,
	hc *hypershiftv1beta1.HostedCluster, pools []hypershiftv1beta1.NodePool) {
	order.Status.UpgradeObservation = nil
	requestsByNodeSet := make(map[string]*v1alpha1.NodeRequestStatus, len(order.Status.NodeRequests))
	for i := range order.Status.NodeRequests {
		request := &order.Status.NodeRequests[i]
		request.UpgradeObservation = nil
		requestsByNodeSet[request.NodeSet] = request
	}
	var ids v1alpha1.ClusterOrderUpgradeAttemptIDs
	if encoded := order.Annotations[v1alpha1.UpgradeAttemptIDsAnnotation]; encoded != "" {
		_ = json.Unmarshal([]byte(encoded), &ids)
	}
	order.Status.UpgradeObservation = controlPlaneUpgradeObservation(order, hc, ids.ControlPlane)
	for i := range pools {
		pool := &pools[i]
		nodeSet, ok := nodePoolNodeSet(pool)
		if !ok {
			continue
		}
		if request := requestsByNodeSet[nodeSet]; request != nil {
			id := ids.NodeSets[nodeSet]
			if id == "" && !nodeSetRequested(order, nodeSet) {
				// A removed NodeSet has no current ID on the order, but its
				// NodePool can still report the attempt stamped before removal.
				id = pool.Annotations[v1alpha1.UpgradeAttemptIDAnnotation]
			}
			request.UpgradeObservation = nodePoolUpgradeObservation(order, pool, nodeSet, id)
		}
	}
}

// controlPlaneUpgradeObservation requires HyperShift to have observed the
// current spec generation before attributing its version history to this ID.
func controlPlaneUpgradeObservation(order *v1alpha1.ClusterOrder,
	hc *hypershiftv1beta1.HostedCluster, id string) *v1alpha1.UpgradeObservation {
	if id == "" || hc.Annotations[v1alpha1.UpgradeAttemptIDAnnotation] != id ||
		hc.Spec.Release.Image != order.Spec.ReleaseImage || hc.Status.Version == nil ||
		hc.Status.Version.ObservedGeneration < hc.Generation ||
		hc.Status.Version.Desired.Image != order.Spec.ReleaseImage {
		return nil
	}
	observedVersion := ""
	if len(hc.Status.Version.History) > 0 &&
		hc.Status.Version.History[0].State == configv1.CompletedUpdate &&
		hc.Status.Version.History[0].Image == order.Spec.ReleaseImage {
		observedVersion = hc.Status.Version.History[0].Version
	}
	return &v1alpha1.UpgradeObservation{AttemptID: id, Version: observedVersion}
}

func nodePoolUpgradeObservation(order *v1alpha1.ClusterOrder,
	pool *hypershiftv1beta1.NodePool, nodeSet, id string) *v1alpha1.UpgradeObservation {
	if id == "" || pool.Annotations[v1alpha1.UpgradeAttemptIDAnnotation] != id ||
		pool.Status.Version == "" ||
		(nodeSetRequested(order, nodeSet) && !nodePoolReleaseMatchesOrder(order, nodeSet, pool.Spec.Release.Image)) {
		return nil
	}
	return &v1alpha1.UpgradeObservation{AttemptID: id, Version: pool.Status.Version}
}

func nodeSetRequested(order *v1alpha1.ClusterOrder, nodeSet string) bool {
	for _, request := range order.Spec.NodeRequests {
		if request.NodeSet == nodeSet {
			return true
		}
	}
	return false
}

func nodePoolReleaseMatchesOrder(order *v1alpha1.ClusterOrder, nodeSet, image string) bool {
	for _, request := range order.Spec.NodeRequests {
		if request.NodeSet == nodeSet {
			desired := request.ReleaseImage
			if desired == "" {
				desired = order.Spec.ReleaseImage
			}
			return image == desired
		}
	}
	return false
}
