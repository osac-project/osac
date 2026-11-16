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
	requestsByNodeSet := make(map[string]*v1alpha1.NodeRequestStatus, len(order.Status.NodeRequests))
	previous := make(map[string]*v1alpha1.UpgradeObservation, len(order.Status.NodeRequests))
	for i := range order.Status.NodeRequests {
		request := &order.Status.NodeRequests[i]
		previous[request.NodeSet] = request.UpgradeObservation
		request.UpgradeObservation = nil
		requestsByNodeSet[request.NodeSet] = request
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
			request.UpgradeObservation = nodePoolUpgradeObservationForAttempt(
				order, pool, nodeSet, id, previous[nodeSet])
		}
	}
}

// nodePoolUpgradeObservationForAttempt carries the pre-stamp baseline across
// polls and attributes progress only after the matching attempt is stamped.
func nodePoolUpgradeObservationForAttempt(order *v1alpha1.ClusterOrder,
	pool *hypershiftv1beta1.NodePool, nodeSet, id string,
	previous *v1alpha1.UpgradeObservation) *v1alpha1.UpgradeObservation {
	if !nodeSetRequested(order, nodeSet) {
		// A removed NodeSet reports from the attempt stamped on its NodePool,
		// even after Fulfillment starts a newer whole-cluster attempt.
		id = pool.Annotations[v1alpha1.UpgradeAttemptIDAnnotation]
	}
	baseline := matchingPreviousBaseline(previous, id)
	var current *v1alpha1.UpgradeObservation
	if id != "" && pool.Annotations[v1alpha1.UpgradeAttemptIDAnnotation] != id {
		current = nodePoolUpgradeBaseline(pool, id)
		if current == nil && baseline != nil {
			current = &v1alpha1.UpgradeObservation{AttemptID: id}
		}
	} else {
		current = nodePoolUpgradeObservation(order, pool, nodeSet, id)
	}
	if current == nil {
		return nil
	}
	if baseline != nil {
		current.BaselineTransitionTime = baseline
	}
	markLegacyNodePoolProgress(pool, id, current)
	return current
}

// matchingPreviousBaseline returns the baseline only when the
// observation belongs to the attempt.
func matchingPreviousBaseline(previous *v1alpha1.UpgradeObservation, id string) *metav1.Time {
	if previous == nil || previous.AttemptID != id {
		return nil
	}
	return previous.BaselineTransitionTime
}

// markLegacyNodePoolProgress accepts an UpdatingVersion condition without an
// observed generation only when it changed after the pre-stamp baseline.
func markLegacyNodePoolProgress(pool *hypershiftv1beta1.NodePool, id string,
	observation *v1alpha1.UpgradeObservation) {
	if observation.BaselineTransitionTime == nil || observation.State != "" ||
		pool.Annotations[v1alpha1.UpgradeAttemptIDAnnotation] != id {
		return
	}
	updating := nodePoolCondition(pool, hypershiftv1beta1.NodePoolUpdatingVersionConditionType)
	if updating != nil && updating.ObservedGeneration == 0 &&
		updating.Status == corev1.ConditionTrue &&
		updating.LastTransitionTime.Time.After(observation.BaselineTransitionTime.Time) {
		observation.State = v1alpha1.UpgradeObservationProgressing
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
func nodePoolUpgradeObservation(order *v1alpha1.ClusterOrder,
	pool *hypershiftv1beta1.NodePool, nodeSet, id string) *v1alpha1.UpgradeObservation {
	if id == "" || pool.Annotations[v1alpha1.UpgradeAttemptIDAnnotation] != id ||
		(nodeSetRequested(order, nodeSet) && !nodePoolReleaseMatchesOrder(order, nodeSet, pool.Spec.Release.Image)) {
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

// nodePoolUpgradeBaseline records the UpdatingVersion transition seen before
// AAP stamps the new attempt, so later polls can reject stale progress.
func nodePoolUpgradeBaseline(pool *hypershiftv1beta1.NodePool, id string) *v1alpha1.UpgradeObservation {
	condition := nodePoolCondition(pool, hypershiftv1beta1.NodePoolUpdatingVersionConditionType)
	if condition == nil || condition.LastTransitionTime.IsZero() {
		return nil
	}
	baseline := condition.LastTransitionTime
	return &v1alpha1.UpgradeObservation{AttemptID: id, BaselineTransitionTime: &baseline}
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

// nodeSetRequested reports whether the order explicitly requests the node set.
func nodeSetRequested(order *v1alpha1.ClusterOrder, nodeSet string) bool {
	for _, request := range order.Spec.NodeRequests {
		if request.NodeSet == nodeSet {
			return true
		}
	}
	return false
}

// nodePoolReleaseMatchesOrder reports whether a NodePool uses the explicit
// release image requested for that node set. A missing node image never matches
// because the AAP provider also requires an explicit per-node-set image.
func nodePoolReleaseMatchesOrder(order *v1alpha1.ClusterOrder, nodeSet, image string) bool {
	for _, request := range order.Spec.NodeRequests {
		if request.NodeSet == nodeSet {
			return request.ReleaseImage != "" && image == request.ReleaseImage
		}
	}
	return false
}
