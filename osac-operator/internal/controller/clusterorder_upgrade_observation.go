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
	configv1 "github.com/openshift/api/config/v1"
	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
)

// recordUpgradeObservations refreshes each component's observation from its
// stamped HyperShift resource.
func recordUpgradeObservations(order *v1alpha1.ClusterOrder,
	hc *hypershiftv1beta1.HostedCluster, pools []hypershiftv1beta1.NodePool) {
	specRequests := make(map[string]*v1alpha1.NodeRequest, len(order.Spec.NodeRequests))
	for i := range order.Spec.NodeRequests {
		request := &order.Spec.NodeRequests[i]
		specRequests[request.NodeSet] = request
	}
	requestsByNodeSet := make(map[string]*v1alpha1.NodeRequestStatus, len(order.Status.NodeRequests))
	for i := range order.Status.NodeRequests {
		request := &order.Status.NodeRequests[i]
		request.UpgradeObservation = nil
		if specRequests[request.NodeSet] != nil {
			requestsByNodeSet[request.NodeSet] = request
		}
	}
	id := order.Annotations[v1alpha1.UpgradeAttemptIDAnnotation]
	order.Status.UpgradeObservation = controlPlaneUpgradeObservation(order, hc, id)
	for i := range pools {
		pool := &pools[i]
		nodeSet, ok := nodePoolNodeSet(pool)
		if !ok {
			continue
		}
		if request := requestsByNodeSet[nodeSet]; request != nil {
			request.UpgradeObservation = nodePoolUpgradeObservation(pool, specRequests[nodeSet], id)
		}
	}
}

// controlPlaneUpgradeObservation classifies the selected attempt from both
// HyperShift version histories and current HostedCluster health.
func controlPlaneUpgradeObservation(order *v1alpha1.ClusterOrder,
	hc *hypershiftv1beta1.HostedCluster, id string) *v1alpha1.UpgradeObservation {
	if id == "" || hc.Annotations[v1alpha1.UpgradeAttemptIDAnnotation] != id ||
		hc.Spec.Release.Image != order.Spec.ReleaseImage {
		return nil
	}
	observation := &v1alpha1.UpgradeObservation{AttemptID: id}
	completedControlPlane := controlPlaneVersionAtState(&hc.Status.ControlPlaneVersion, configv1.CompletedUpdate)
	completedGuest := versionAtState(hc.Status.Version, configv1.CompletedUpdate)
	if completedControlPlane != "" && completedControlPlane == completedGuest &&
		currentHostedClusterConditionStatus(hc, string(hypershiftv1beta1.HostedClusterAvailable), metav1.ConditionTrue) &&
		currentHostedClusterConditionStatus(hc, string(hypershiftv1beta1.HostedClusterDegraded), metav1.ConditionFalse) &&
		currentHostedClusterConditionStatus(hc, string(hypershiftv1beta1.ClusterVersionSucceeding), metav1.ConditionTrue) &&
		currentHostedClusterConditionStatus(hc, string(hypershiftv1beta1.ClusterVersionProgressing), metav1.ConditionFalse) {
		observation.State = v1alpha1.UpgradeObservationSucceeded
		observation.Version = completedControlPlane
		return observation
	}
	partialControlPlane := controlPlaneVersionAtState(&hc.Status.ControlPlaneVersion, configv1.PartialUpdate)
	partialClusterVersion := versionAtState(hc.Status.Version, configv1.PartialUpdate)
	// The order carries no target version with which to disambiguate conflicting
	// partial histories; feedback must not attribute either one to the attempt.
	if partialControlPlane != "" && partialClusterVersion != "" && partialControlPlane != partialClusterVersion {
		return observation
	}
	if partialControlPlane != "" {
		observation.State = v1alpha1.UpgradeObservationProgressing
		observation.Version = partialControlPlane
	} else if partialClusterVersion != "" {
		observation.State = v1alpha1.UpgradeObservationProgressing
		observation.Version = partialClusterVersion
	}
	return observation
}

// versionAtState returns the desired semver only when the newest
// HostedCluster status.version history entry reports it in the requested state.
func versionAtState(status *hypershiftv1beta1.ClusterVersionStatus, state configv1.UpdateState) string {
	if status == nil || len(status.History) == 0 {
		return ""
	}
	latest := status.History[0]
	return historyVersionAtState(status.Desired.Version, latest.Version, latest.State, state)
}

// controlPlaneVersionAtState applies the same phase gate to the control-plane
// component history.
func controlPlaneVersionAtState(status *hypershiftv1beta1.ControlPlaneVersionStatus, state configv1.UpdateState) string {
	if status == nil || len(status.History) == 0 {
		return ""
	}
	latest := status.History[0]
	return historyVersionAtState(status.Desired.Version, latest.Version, latest.State, state)
}

func historyVersionAtState(desiredVersion, latestVersion string,
	latestState, state configv1.UpdateState) string {
	if desiredVersion == "" || latestVersion != desiredVersion || latestState != state {
		return ""
	}
	return latestVersion
}

// nodePoolUpgradeObservation classifies only the NodePool stamped for the
// selected attempt and requested release image.
func nodePoolUpgradeObservation(pool *hypershiftv1beta1.NodePool,
	request *v1alpha1.NodeRequest, id string) *v1alpha1.UpgradeObservation {
	if id == "" || pool.Annotations[v1alpha1.UpgradeAttemptIDAnnotation] != id {
		return nil
	}
	if request.ReleaseImage == "" || pool.Spec.Release.Image != request.ReleaseImage {
		return nil
	}
	observation := &v1alpha1.UpgradeObservation{AttemptID: id, Version: pool.Status.Version}
	updating := currentNodePoolCondition(pool, hypershiftv1beta1.NodePoolUpdatingVersionConditionType)
	if updating != nil && updating.Status == corev1.ConditionTrue {
		observation.State = v1alpha1.UpgradeObservationProgressing
	}
	if nodePoolReadyAtVersion(pool) {
		observation.ReadyAtVersion = true
		observation.State = v1alpha1.UpgradeObservationSucceeded
	}
	return observation
}

// nodePoolReadyAtVersion reports the current completion signals for a
// NodePool with a nonempty reported version.
func nodePoolReadyAtVersion(pool *hypershiftv1beta1.NodePool) bool {
	if pool.Status.Version == "" {
		return false
	}
	return currentNodePoolConditionStatus(pool, hypershiftv1beta1.NodePoolUpdatingVersionConditionType, corev1.ConditionFalse) &&
		currentNodePoolConditionStatus(pool, hypershiftv1beta1.NodePoolAllMachinesReadyConditionType, corev1.ConditionTrue) &&
		currentNodePoolConditionStatus(pool, hypershiftv1beta1.NodePoolReadyConditionType, corev1.ConditionTrue)
}

// currentHostedClusterCondition returns a HostedCluster
// condition observed at the current generation.
func currentHostedClusterCondition(hc *hypershiftv1beta1.HostedCluster, kind string) *metav1.Condition {
	condition := apimeta.FindStatusCondition(hc.Status.Conditions, kind)
	if condition == nil || condition.ObservedGeneration < hc.Generation {
		return nil
	}
	return condition
}

// currentHostedClusterConditionStatus requires a condition from the current
// HostedCluster generation with the specified status.
func currentHostedClusterConditionStatus(hc *hypershiftv1beta1.HostedCluster, kind string,
	status metav1.ConditionStatus) bool {
	condition := currentHostedClusterCondition(hc, kind)
	return condition != nil && condition.Status == status
}

// currentNodePoolCondition returns a NodePool condition observed
// at the current generation.
func currentNodePoolCondition(pool *hypershiftv1beta1.NodePool, kind string) *hypershiftv1beta1.NodePoolCondition {
	condition := nodePoolCondition(pool, kind)
	if condition == nil || condition.ObservedGeneration < pool.Generation {
		return nil
	}
	return condition
}

// currentNodePoolConditionStatus requires a condition from the current
// NodePool generation with the specified status.
func currentNodePoolConditionStatus(pool *hypershiftv1beta1.NodePool, kind string,
	status corev1.ConditionStatus) bool {
	condition := currentNodePoolCondition(pool, kind)
	return condition != nil && condition.Status == status
}

// nodePoolCondition returns the NodePool condition of the requested kind.
func nodePoolCondition(pool *hypershiftv1beta1.NodePool, kind string) *hypershiftv1beta1.NodePoolCondition {
	for i := range pool.Status.Conditions {
		condition := &pool.Status.Conditions[i]
		if condition.Type == kind {
			return condition
		}
	}
	return nil
}
