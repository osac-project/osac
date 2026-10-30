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
			request.UpgradeObservation = nodePoolUpgradeObservationForAttempt(
				order, pool, nodeSet, ids.NodeSets[nodeSet], previous[nodeSet])
		}
	}
}

// nodePoolUpgradeObservationForAttempt carries the pre-stamp baseline across
// polls and attributes progress only after the matching attempt is stamped.
func nodePoolUpgradeObservationForAttempt(order *v1alpha1.ClusterOrder,
	pool *hypershiftv1beta1.NodePool, nodeSet, id string,
	previous *v1alpha1.UpgradeObservation) *v1alpha1.UpgradeObservation {
	if id == "" && !nodeSetRequested(order, nodeSet) {
		// A removed NodeSet can still report its last stamped attempt.
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

// controlPlaneUpgradeObservation requires HyperShift to have observed the
// current spec generation before attributing its version history to this ID.
func controlPlaneUpgradeObservation(order *v1alpha1.ClusterOrder,
	hc *hypershiftv1beta1.HostedCluster, id string) *v1alpha1.UpgradeObservation {
	if id == "" || hc.Annotations[v1alpha1.UpgradeAttemptIDAnnotation] != id ||
		hc.Spec.Release.Image != order.Spec.ReleaseImage {
		return nil
	}
	observation := &v1alpha1.UpgradeObservation{AttemptID: id}
	if rejected := currentHostedClusterCondition(hc, string(hypershiftv1beta1.ValidReleaseImage)); rejected != nil && rejected.Status == metav1.ConditionFalse && terminalReleaseReason(rejected.Reason) {
		observation.State = v1alpha1.UpgradeObservationFailed
		observation.Message = "Control-plane release image was rejected: " + rejected.Reason
	}
	if !hostedClusterObservedRelease(hc, order.Spec.ReleaseImage) {
		return observation
	}
	if len(hc.Status.Version.History) > 0 && hc.Status.Version.History[0].Image == order.Spec.ReleaseImage {
		applyControlPlaneHistoryObservation(hc, hc.Status.Version.History[0], observation)
	}
	if currentHostedClusterConditionTrue(hc, string(hypershiftv1beta1.ClusterVersionProgressing)) && observation.State == "" {
		observation.State = v1alpha1.UpgradeObservationProgressing
	}
	return observation
}

// hostedClusterObservedRelease reports whether HyperShift has
// observed the requested release.
func hostedClusterObservedRelease(hc *hypershiftv1beta1.HostedCluster, image string) bool {
	return hc.Status.Version != nil && hc.Status.Version.ObservedGeneration >= hc.Generation &&
		hc.Status.Version.Desired.Image == image
}

// applyControlPlaneHistoryObservation requires current HostedCluster health
// before treating a completed HyperShift update as successful.
func applyControlPlaneHistoryObservation(hc *hypershiftv1beta1.HostedCluster,
	history configv1.UpdateHistory, observation *v1alpha1.UpgradeObservation) {
	if history.State == configv1.CompletedUpdate {
		observation.Version = history.Version
		if currentHostedClusterConditionTrue(hc, string(hypershiftv1beta1.HostedClusterAvailable)) &&
			!currentHostedClusterConditionTrue(hc, string(hypershiftv1beta1.HostedClusterDegraded)) {
			observation.State = v1alpha1.UpgradeObservationSucceeded
		}
	}
	if (history.State == configv1.PartialUpdate || history.State == configv1.CompletedUpdate) &&
		observation.State == "" {
		observation.State = v1alpha1.UpgradeObservationProgressing
	}
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
	if reason := nodePoolRejectedReleaseReason(pool); reason != "" {
		observation.State = v1alpha1.UpgradeObservationFailed
		observation.Message = "Node-pool release image was rejected: " + reason
	}
	updating := currentNodePoolCondition(pool, hypershiftv1beta1.NodePoolUpdatingVersionConditionType)
	if updating != nil && updating.Status == corev1.ConditionTrue && observation.State == "" {
		observation.State = v1alpha1.UpgradeObservationProgressing
	}
	if observation.State != v1alpha1.UpgradeObservationFailed && nodePoolReadyAtVersion(pool) {
		observation.ReadyAtVersion = true
		observation.State = v1alpha1.UpgradeObservationSucceeded
	}
	return observation
}

// nodePoolRejectedReleaseReason returns the terminal reason for
// a rejected NodePool release.
func nodePoolRejectedReleaseReason(pool *hypershiftv1beta1.NodePool) string {
	reason := ""
	for _, kind := range []string{hypershiftv1beta1.NodePoolValidReleaseImageConditionType,
		hypershiftv1beta1.NodePoolValidPlatformImageType} {
		if rejected := currentNodePoolCondition(pool, kind); rejected != nil &&
			rejected.Status == corev1.ConditionFalse && terminalReleaseReason(rejected.Reason) {
			reason = rejected.Reason
		}
	}
	return reason
}

// nodePoolReadyAtVersion reports whether every NodePool replica
// is ready at the reported version.
func nodePoolReadyAtVersion(pool *hypershiftv1beta1.NodePool) bool {
	if pool.Status.Version == "" {
		return false
	}
	if updating := nodePoolCondition(pool, hypershiftv1beta1.NodePoolUpdatingVersionConditionType); updating != nil &&
		updating.Status == corev1.ConditionTrue {
		return false
	}
	return nodePoolIsReady(pool) &&
		(pool.Spec.Replicas == nil || pool.Status.Replicas == *pool.Spec.Replicas)
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

// currentHostedClusterConditionTrue reports whether the current
// condition is true.
func currentHostedClusterConditionTrue(hc *hypershiftv1beta1.HostedCluster, kind string) bool {
	condition := currentHostedClusterCondition(hc, kind)
	return condition != nil && condition.Status == metav1.ConditionTrue
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

// terminalReleaseReason reports whether a release-image
// rejection reason is terminal.
func terminalReleaseReason(reason string) bool {
	switch reason {
	case "ValidationFailed", "UnsupportedReleaseImage", "InvalidReleaseImage", "UnsupportedVersion", "VersionSkew", "DowngradeNotSupported":
		return true
	default:
		return false
	}
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

// nodePoolReleaseMatchesOrder reports whether a NodePool uses its requested
// release image, falling back to the order-wide image when necessary.
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
