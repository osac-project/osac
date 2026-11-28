/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package servers

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/osac-project/osac/fulfillment-service/internal/controllers"
	"github.com/osac-project/osac/fulfillment-service/internal/uuid"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

// clusterUpgradeAdmissible reports whether the server-owned gate explicitly admits a
// new upgrade request.
func clusterUpgradeAdmissible(status *privatev1.ClusterStatus) bool {
	condition := clusterCondition(status.GetConditions(),
		privatev1.ClusterConditionType_CLUSTER_CONDITION_TYPE_UPGRADE_ADMISSIBLE)
	return condition.GetStatus() == privatev1.ConditionStatus_CONDITION_STATUS_TRUE
}

func clusterCondition(conditions []*privatev1.ClusterCondition,
	conditionType privatev1.ClusterConditionType) *privatev1.ClusterCondition {
	for _, condition := range conditions {
		if condition.GetType() == conditionType {
			return condition
		}
	}
	return nil
}

func clusterUpgradeHasActiveAttempt(status *privatev1.ClusterStatus) bool {
	return status.GetActiveUpgradeAttempt() != nil
}

func clusterUpgradeBlockedState(state privatev1.ClusterState) bool {
	return state == privatev1.ClusterState_CLUSTER_STATE_FAILED ||
		state == privatev1.ClusterState_CLUSTER_STATE_DELETING ||
		state == privatev1.ClusterState_CLUSTER_STATE_DELETE_FAILED
}

func clusterUpgradeReady(cluster *privatev1.Cluster) bool {
	status := cluster.GetStatus()
	if clusterUpgradeBlockedState(status.GetState()) {
		return false
	}
	controlPlaneAvailable := clusterCondition(status.GetConditions(),
		privatev1.ClusterConditionType_CLUSTER_CONDITION_TYPE_CONTROL_PLANE_AVAILABLE)
	workersReady := clusterCondition(status.GetConditions(),
		privatev1.ClusterConditionType_CLUSTER_CONDITION_TYPE_WORKERS_READY)
	if controlPlaneAvailable.GetStatus() != privatev1.ConditionStatus_CONDITION_STATUS_TRUE ||
		workersReady.GetStatus() != privatev1.ConditionStatus_CONDITION_STATUS_TRUE ||
		status.GetObservedVersion() == "" {
		return false
	}
	for name, nodeSet := range cluster.GetSpec().GetNodeSets() {
		if nodeSet == nil {
			continue
		}
		if status.GetNodeSets()[name].GetObservedVersion() == "" {
			return false
		}
	}
	return true
}

func reconcileClusterUpgradeAdmissibility(current, candidate *privatev1.Cluster, successRelease bool) {
	status := candidate.GetStatus()
	currentGate := clusterCondition(current.GetStatus().GetConditions(),
		privatev1.ClusterConditionType_CLUSTER_CONDITION_TYPE_UPGRADE_ADMISSIBLE)
	switch {
	case clusterUpgradeHasActiveAttempt(status):
		setClusterUpgradeAdmissible(status, currentGate, false, "UpgradeInProgress",
			"An upgrade attempt is already active.")
	case clusterUpgradeBlockedState(status.GetState()):
		setClusterUpgradeAdmissible(status, currentGate, false, "ClusterStateBlocked",
			"Cluster state "+status.GetState().String()+" cannot accept an upgrade.")
	case successRelease:
		setClusterUpgradeAdmissible(status, currentGate, true, "Admissible",
			"A new cluster upgrade request may be admitted.")
	case clusterUpgradeAdmissible(current.GetStatus()):
		setClusterUpgradeAdmissible(status, currentGate, true, "Admissible",
			"A new cluster upgrade request may be admitted.")
	case clusterUpgradeReady(candidate):
		setClusterUpgradeAdmissible(status, currentGate, true, "Admissible",
			"A new cluster upgrade request may be admitted.")
	default:
		setClusterUpgradeAdmissible(status, currentGate, false, "AwaitingReadiness",
			"Observed control-plane and node-pool versions are not yet established.")
	}
}

// setClusterUpgradeAdmissible replaces the admissibility condition from the authoritative
// current condition and records a transition only when that status changes.
func setClusterUpgradeAdmissible(status *privatev1.ClusterStatus, currentGate *privatev1.ClusterCondition,
	open bool, reason, message string) {
	if status == nil {
		return
	}
	conditions := status.GetConditions()
	gate := clusterCondition(conditions,
		privatev1.ClusterConditionType_CLUSTER_CONDITION_TYPE_UPGRADE_ADMISSIBLE)
	if gate == nil {
		gate = privatev1.ClusterCondition_builder{
			Type: privatev1.ClusterConditionType_CLUSTER_CONDITION_TYPE_UPGRADE_ADMISSIBLE,
		}.Build()
		conditions = append(conditions, gate)
	}

	desiredStatus := privatev1.ConditionStatus_CONDITION_STATUS_FALSE
	if open {
		desiredStatus = privatev1.ConditionStatus_CONDITION_STATUS_TRUE
	}
	if currentGate.GetStatus() != desiredStatus || currentGate.GetLastTransitionTime() == nil {
		gate.SetLastTransitionTime(controllers.NowTruncated())
	} else {
		gate.SetLastTransitionTime(currentGate.GetLastTransitionTime())
	}
	gate.SetStatus(desiredStatus)
	gate.SetReason(reason)
	gate.SetMessage(message)
	status.SetConditions(conditions)
}

// prepareClusterVersionChange applies whole-cluster version rules to the
// completed candidate before it can be persisted.
func (s *PrivateClustersServer) prepareClusterVersionChange(ctx context.Context,
	current, candidate *privatev1.Cluster, mask *fieldmaskpb.FieldMask,
	resolvedTarget *privatev1.ClusterVersion) error {
	controlPlaneChanged := updateIncludesField(mask, "spec.version") &&
		current.GetSpec().GetVersion().GetId() != candidate.GetSpec().GetVersion().GetId()
	explicitVersionSelection := updateMaskIncludesExactPath(mask, "spec.version")
	if clusterUpgradeHasActiveAttempt(current.GetStatus()) &&
		(explicitVersionSelection || controlPlaneChanged) {
		return grpcstatus.Error(grpccodes.FailedPrecondition,
			"An upgrade attempt is already active.")
	}
	if !controlPlaneChanged {
		return nil
	}
	if clusterUpgradeBlockedState(candidate.GetStatus().GetState()) {
		return grpcstatus.Errorf(grpccodes.FailedPrecondition,
			"cluster state %s cannot accept an upgrade", candidate.GetStatus().GetState())
	}
	source, err := getClusterVersionByID(ctx, s.clusterVersionsDao, current.GetSpec().GetVersion().GetId())
	if err != nil {
		return err
	}
	if resolvedTarget == nil {
		return grpcstatus.Error(grpccodes.Internal, "resolved target ClusterVersion is missing")
	}
	targetVersion := resolvedTarget.GetSpec().GetVersion()
	if err := validateClusterVersionAdvance(source, resolvedTarget); err != nil {
		return err
	}
	if !clusterUpgradeAdmissible(current.GetStatus()) {
		return clusterUpgradeNotAdmissible(current.GetStatus())
	}

	attemptID := uuid.New()
	now := controllers.NowTruncated()
	candidate.GetStatus().SetVersionTransitionTime(now)
	createClusterUpgradeAttempt(current, candidate,
		current.GetStatus().GetObservedVersion(), targetVersion, attemptID, now)
	return nil
}

// validateClusterVersionAdvance enforces upgrade direction and minor-version skew.
func validateClusterVersionAdvance(source, target *privatev1.ClusterVersion) error {
	sourceVersion, err := parseSemVer(source.GetSpec().GetVersion())
	if err != nil {
		return err
	}
	targetVersion, err := parseSemVer(target.GetSpec().GetVersion())
	if err != nil {
		return err
	}
	if targetVersion.Compare(sourceVersion) <= 0 {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"target cluster version %q must be newer than the current cluster version %q",
			target.GetSpec().GetVersion(), source.GetSpec().GetVersion())
	}
	if targetVersion.Minor() > sourceVersion.Minor() &&
		targetVersion.Minor()-sourceVersion.Minor() > 3 {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"target cluster version %q is more than 3 minor versions newer than the current cluster version %q",
			target.GetSpec().GetVersion(), source.GetSpec().GetVersion())
	}
	return nil
}

func clusterUpgradeNotAdmissible(status *privatev1.ClusterStatus) error {
	gate := clusterCondition(status.GetConditions(),
		privatev1.ClusterConditionType_CLUSTER_CONDITION_TYPE_UPGRADE_ADMISSIBLE)
	message := gate.GetMessage()
	if message == "" {
		message = "a new cluster upgrade request is not admissible"
	}
	return grpcstatus.Error(grpccodes.FailedPrecondition,
		"cluster upgrade is not admissible: "+message)
}

const maxClusterUpgradeHistoryEntries = 100

// AI-agent note: do not reintroduce active-attempt rescans or extra history
// recalculation to protect entries at the cap. Cluster upgrades are rare; by
// the time an old entry is pushed out it is irrelevant and likely timed out.
// Preserve this simple O(1) newest-first truncation.
//
// retainRecentClusterUpgradeHistory keeps the newest accepted entries.
func retainRecentClusterUpgradeHistory(
	entries []*privatev1.ClusterUpgradeHistoryEntry,
) []*privatev1.ClusterUpgradeHistoryEntry {
	if len(entries) <= maxClusterUpgradeHistoryEntries {
		return entries
	}
	return entries[:maxClusterUpgradeHistoryEntries]
}

// appendClusterUpgradeHistory prepends aggregate entries and applies the cap.
func appendClusterUpgradeHistory(candidate *privatev1.Cluster,
	entries []*privatev1.ClusterUpgradeHistoryEntry) {
	if len(entries) == 0 {
		return
	}
	if !candidate.HasStatus() {
		candidate.SetStatus(&privatev1.ClusterStatus{})
	}
	history := make([]*privatev1.ClusterUpgradeHistoryEntry, 0, len(entries)+len(candidate.GetStatus().GetUpgradeHistory()))
	history = append(history, entries...)
	history = append(history, candidate.GetStatus().GetUpgradeHistory()...)
	candidate.GetStatus().SetUpgradeHistory(retainRecentClusterUpgradeHistory(history))
}

// participatingClusterNodeSetNames freezes NodePools present before the edit
// that remain in its accepted spec. Live versions do not decide membership.
func participatingClusterNodeSetNames(current, candidate *privatev1.Cluster) []string {
	names := make([]string, 0, len(current.GetSpec().GetNodeSets()))
	for name := range current.GetSpec().GetNodeSets() {
		if candidate.GetSpec().GetNodeSets()[name] == nil {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// createClusterUpgradeAttempt records one aggregate history entry and the
// detailed state needed to finish the current attempt.
func createClusterUpgradeAttempt(current, candidate *privatev1.Cluster,
	sourceVersion, targetVersion, attemptID string, now *timestamppb.Timestamp) {
	names := participatingClusterNodeSetNames(current, candidate)
	controlPlaneState := privatev1.ClusterUpgradeControlPlaneState_builder{
		AcceptedTime:        now,
		StateTransitionTime: now,
		State:               privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_PENDING,
	}.Build()

	nodeSets := make([]*privatev1.ClusterUpgradeNodeSetState, 0, len(names))
	for _, name := range names {
		// The candidate size only estimates the warning deadline. It is never
		// recalculated when this NodePool scales during the attempt.
		nodeSets = append(nodeSets, privatev1.ClusterUpgradeNodeSetState_builder{
			NodeSetId:           name,
			TimeoutNodeCount:    max(int32(1), candidate.GetSpec().GetNodeSets()[name].GetSize()),
			State:               privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_PENDING,
			StateTransitionTime: now,
		}.Build())
	}

	active := privatev1.ClusterUpgradeAttemptState_builder{
		ControlPlane: controlPlaneState,
		NodeSets:     nodeSets,
	}.Build()
	entry := privatev1.ClusterUpgradeHistoryEntry_builder{
		AttemptId:           attemptID,
		SourceVersion:       sourceVersion,
		TargetVersion:       targetVersion,
		AcceptedTime:        now,
		StateTransitionTime: now,
		State:               privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_PENDING,
		Message:             "Waiting for control plane upgrade",
	}.Build()

	appendClusterUpgradeHistory(candidate, []*privatev1.ClusterUpgradeHistoryEntry{entry})
	candidate.GetStatus().SetActiveUpgradeAttempt(active)
	candidate.GetStatus().SetUpgradeAttemptId(attemptID)
}

// ensureClusterNodeSetStatus returns the mutable status for a node set,
// creating the status map and entry when necessary.
func ensureClusterNodeSetStatus(candidate *privatev1.Cluster, name string) *privatev1.ClusterNodeSetStatus {
	if !candidate.HasStatus() {
		candidate.SetStatus(&privatev1.ClusterStatus{})
	}
	statuses := candidate.GetStatus().GetNodeSets()
	if statuses == nil {
		statuses = map[string]*privatev1.ClusterNodeSetStatus{}
		candidate.GetStatus().SetNodeSets(statuses)
	}
	status := statuses[name]
	if status == nil {
		status = &privatev1.ClusterNodeSetStatus{}
		statuses[name] = status
	}
	return status
}

// applyClusterNodeSetUpgradeTargets moves each participating node-set reference
// to the accepted cluster target.
func applyClusterNodeSetUpgradeTargets(candidate *privatev1.Cluster, names []string) {
	targetRef := candidate.GetSpec().GetVersion()
	if targetRef == nil {
		return
	}
	for _, name := range names {
		ensureClusterNodeSetStatus(candidate, name).SetVersion(cloneMessage(targetRef))
	}
}

// mergeClusterUpgradeHistory preserves server-owned history and validates
// feedback for the current attempt.
func mergeClusterUpgradeHistory(current, candidate *privatev1.Cluster,
	mask *fieldmaskpb.FieldMask, requestAttemptID string,
	requestActive *privatev1.ClusterUpgradeAttemptState,
	resolvedTarget *privatev1.ClusterVersion) {
	attemptID := current.GetStatus().GetUpgradeAttemptId()
	stored := current.GetStatus().GetUpgradeHistory()
	result := slices.Clone(stored)
	if !candidate.HasStatus() {
		candidate.SetStatus(&privatev1.ClusterStatus{})
	}
	candidate.GetStatus().SetUpgradeAttemptId(attemptID)
	// Admission rejects a new target while an attempt is active. A
	// same-version replacement is only accepted once no attempt is active.
	if selectsDifferentClusterUpgradeTarget(current, candidate, mask, resolvedTarget) {
		candidate.GetStatus().SetActiveUpgradeAttempt(current.GetStatus().GetActiveUpgradeAttempt())
		candidate.GetStatus().SetUpgradeHistory(stored)
		return
	}
	mergedActive := mergeCurrentClusterUpgradeAttempt(current, candidate, mask, requestAttemptID, requestActive)
	updateCurrentClusterUpgradeHistory(result, attemptID, mergedActive)
	candidate.GetStatus().SetActiveUpgradeAttempt(mergedActive)
	candidate.GetStatus().SetUpgradeHistory(result)
}

func selectsDifferentClusterUpgradeTarget(current, candidate *privatev1.Cluster,
	mask *fieldmaskpb.FieldMask, resolvedTarget *privatev1.ClusterVersion) bool {
	return updateIncludesField(mask, "spec.version") &&
		current.GetSpec().GetVersion().GetId() != candidate.GetSpec().GetVersion().GetId() &&
		clusterUpgradeHistoryEntryByID(current.GetStatus().GetUpgradeHistory(),
			current.GetStatus().GetUpgradeAttemptId()).GetTargetVersion() != resolvedTarget.GetSpec().GetVersion()
}

func mergeCurrentClusterUpgradeAttempt(current, candidate *privatev1.Cluster,
	mask *fieldmaskpb.FieldMask, requestAttemptID string,
	requestActive *privatev1.ClusterUpgradeAttemptState) *privatev1.ClusterUpgradeAttemptState {
	attemptID := current.GetStatus().GetUpgradeAttemptId()
	mergedActive := current.GetStatus().GetActiveUpgradeAttempt()
	if updateIncludesField(mask, "status.active_upgrade_attempt") &&
		attemptID != "" && requestAttemptID == attemptID {
		mergedActive = mergeClusterUpgradeAttemptState(current, candidate, attemptID, requestActive)
	}
	return removeDeletedClusterUpgradeParticipants(mergedActive, candidate)
}

func updateCurrentClusterUpgradeHistory(result []*privatev1.ClusterUpgradeHistoryEntry,
	attemptID string, mergedActive *privatev1.ClusterUpgradeAttemptState) {
	for i, entry := range result {
		if attemptID != "" && entry.GetAttemptId() == attemptID {
			result[i] = proto.Clone(entry).(*privatev1.ClusterUpgradeHistoryEntry)
			applyClusterUpgradeOutcome(result[i], mergedActive, controllers.NowTruncated())
			break
		}
	}
}

// clusterUpgradeHistoryEntryByID returns the first entry with the attempt ID.
func clusterUpgradeHistoryEntryByID(entries []*privatev1.ClusterUpgradeHistoryEntry,
	attemptID string) *privatev1.ClusterUpgradeHistoryEntry {
	for _, entry := range entries {
		if entry.GetAttemptId() == attemptID {
			return entry
		}
	}
	return nil
}

// mergeClusterUpgradeAttemptState validates component feedback against the
// frozen active attempt and publishes each accepted success version once.
// A nil incoming value cannot clear a nonterminal attempt.
func mergeClusterUpgradeAttemptState(current, candidate *privatev1.Cluster,
	attemptID string, requestActive *privatev1.ClusterUpgradeAttemptState) *privatev1.ClusterUpgradeAttemptState {
	storedEntry := clusterUpgradeHistoryEntryByID(
		current.GetStatus().GetUpgradeHistory(), attemptID)
	if storedEntry != nil && isTerminalClusterUpgradeState(storedEntry.GetState()) &&
		clusterUpgradeAttemptDefinitive(current.GetStatus().GetActiveUpgradeAttempt()) {
		return nil
	}
	incoming := candidate.GetStatus().GetActiveUpgradeAttempt()
	if incoming == nil {
		return current.GetStatus().GetActiveUpgradeAttempt()
	}
	stored := current.GetStatus().GetActiveUpgradeAttempt()
	if stored == nil {
		return nil
	}
	merged := proto.Clone(stored).(*privatev1.ClusterUpgradeAttemptState)
	targetVersion := storedEntry.GetTargetVersion()
	mergeClusterUpgradeControlPlane(candidate, stored, incoming, merged, targetVersion)
	// NodePool state is mutable only when the request itself carries the same
	// release marker Fulfillment stored. The merged candidate cannot be used
	// here because a partial field mask can inherit the stored marker.
	if merged.GetNodeSetsScheduledTime() != nil && proto.Equal(merged.GetNodeSetsScheduledTime(),
		requestActive.GetNodeSetsScheduledTime()) {
		mergeClusterUpgradeNodePools(candidate, stored, incoming, merged, targetVersion)
	}
	return merged
}

func mergeClusterUpgradeControlPlane(candidate *privatev1.Cluster,
	stored, incoming, merged *privatev1.ClusterUpgradeAttemptState, targetVersion string) {
	controlPlane := merged.GetControlPlane()
	if controlPlane == nil {
		return
	}
	mergeClusterUpgradeComponentState(controlPlane, incoming.GetControlPlane())
	if newlySucceededUpgradeComponent(stored.GetControlPlane(), controlPlane) {
		candidate.GetStatus().SetObservedVersion(targetVersion)
	}
}

func mergeClusterUpgradeNodePools(candidate *privatev1.Cluster,
	stored, incoming, merged *privatev1.ClusterUpgradeAttemptState, targetVersion string) {
	incomingNodeSets := make(map[string]*privatev1.ClusterUpgradeNodeSetState,
		len(incoming.GetNodeSets()))
	for _, nodeSet := range incoming.GetNodeSets() {
		incomingNodeSets[nodeSet.GetNodeSetId()] = nodeSet
	}
	for i, nodeSet := range merged.GetNodeSets() {
		mergeClusterUpgradeComponentState(nodeSet, incomingNodeSets[nodeSet.GetNodeSetId()])
		if newlySucceededUpgradeComponent(stored.GetNodeSets()[i], nodeSet) &&
			candidate.GetSpec().GetNodeSets()[nodeSet.GetNodeSetId()] != nil {
			publishClusterNodePoolObservedVersion(candidate, nodeSet.GetNodeSetId(), targetVersion)
		}
	}
}

func newlySucceededUpgradeComponent(stored, merged clusterUpgradeComponentState) bool {
	return stored.GetState() != privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_SUCCEEDED &&
		merged.GetState() == privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_SUCCEEDED
}

func publishClusterNodePoolObservedVersion(candidate *privatev1.Cluster, nodeSetID, targetVersion string) {
	ensureClusterNodeSetStatus(candidate, nodeSetID).SetObservedVersion(targetVersion)
}

// removeDeletedClusterUpgradeParticipants invalidates frozen membership when
// a caller accepts a node-set removal. Re-adding the same name later creates a
// new node set and does not restore the removed participant.
func removeDeletedClusterUpgradeParticipants(active *privatev1.ClusterUpgradeAttemptState,
	candidate *privatev1.Cluster) *privatev1.ClusterUpgradeAttemptState {
	if active == nil {
		return nil
	}
	merged := proto.Clone(active).(*privatev1.ClusterUpgradeAttemptState)
	nodeSets := slices.DeleteFunc(merged.GetNodeSets(), func(nodeSet *privatev1.ClusterUpgradeNodeSetState) bool {
		return candidate.GetSpec().GetNodeSets()[nodeSet.GetNodeSetId()] == nil
	})
	merged.SetNodeSets(nodeSets)
	return merged
}

// applyClusterUpgradeOutcome derives the public state from the current
// component phases and timeout warnings. Fulfillment owns this calculation.
func applyClusterUpgradeOutcome(entry *privatev1.ClusterUpgradeHistoryEntry,
	active *privatev1.ClusterUpgradeAttemptState, now *timestamppb.Timestamp) {
	if active == nil || active.GetControlPlane() == nil ||
		isTerminalClusterUpgradeState(entry.GetState()) {
		return
	}
	state, message := clusterUpgradeOutcome(active)
	setClusterUpgradeHistoryState(entry, state, message, now)
}

func clusterUpgradeOutcome(active *privatev1.ClusterUpgradeAttemptState) (
	privatev1.ClusterUpgradeState, string) {
	controlPlane := active.GetControlPlane()
	nodePoolState, nodePoolMessage := clusterNodePoolUpgradeState(active.GetNodeSets())
	switch {
	case controlPlane.GetState() == privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_SUCCEEDED &&
		nodePoolState == privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_SUCCEEDED:
		return privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_SUCCEEDED, ""
	case controlPlane.GetTimeoutTime() != nil:
		return privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_TIMED_OUT,
			clusterUpgradeTimeoutReason("Control plane", controlPlane.GetMessage())
	case nodePoolMessage != "":
		return privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_TIMED_OUT, nodePoolMessage
	case controlPlane.GetState() == privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_SUCCEEDED &&
		nodePoolState == privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_PENDING:
		return privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_PROGRESSING, "Waiting for node pool upgrades"
	case controlPlane.GetState() == privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_SUCCEEDED:
		return privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_PROGRESSING, "NodePool upgrades in progress"
	case controlPlane.GetState() == privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_PROGRESSING ||
		nodePoolState == privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_PROGRESSING:
		return privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_PROGRESSING, "Control plane upgrade in progress"
	default:
		return privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_PENDING, "Waiting for control plane upgrade"
	}
}

// clusterNodePoolUpgradeState returns the combined NodePool phase and its
// first timeout message.
func clusterNodePoolUpgradeState(nodePools []*privatev1.ClusterUpgradeNodeSetState) (
	privatev1.ClusterUpgradeState, string) {
	state := privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_SUCCEEDED
	timeoutMessage := ""
	for _, nodePool := range nodePools {
		if nodePool.GetState() == privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_PROGRESSING {
			state = privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_PROGRESSING
		} else if nodePool.GetState() != privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_SUCCEEDED &&
			state == privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_SUCCEEDED {
			state = privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_PENDING
		}
		if timeoutMessage == "" && nodePool.GetTimeoutTime() != nil {
			timeoutMessage = clusterUpgradeTimeoutReason(
				fmt.Sprintf("NodePool %q", nodePool.GetNodeSetId()), nodePool.GetMessage())
		}
	}
	return state, timeoutMessage
}

func clusterUpgradeTimeoutReason(component, message string) string {
	if message != "" {
		return message
	}
	return component + " upgrade timed out"
}

// clusterUpgradeComponentState is the mutable state shape shared by
// control-plane and node-set upgrade participants.
type clusterUpgradeComponentState interface {
	GetState() privatev1.ClusterUpgradeState
	SetState(privatev1.ClusterUpgradeState)
	GetMessage() string
	SetMessage(string)
	GetStateTransitionTime() *timestamppb.Timestamp
	SetStateTransitionTime(*timestamppb.Timestamp)
	GetTimeoutTime() *timestamppb.Timestamp
	SetTimeoutTime(*timestamppb.Timestamp)
	ClearTimeoutTime()
}

// mergeClusterUpgradeComponentState keeps phase transitions and timeout
// warnings independent. A warning can be recorded once for the current phase;
// only a new phase clears it.
func mergeClusterUpgradeComponentState[S any, P interface {
	clusterUpgradeComponentState
	*S
}](stored, update P) {
	if update == nil || isTerminalClusterUpgradeState(stored.GetState()) {
		return
	}
	if update.GetState() != stored.GetState() {
		if !validClusterUpgradePhaseTransition(stored.GetState(), update.GetState()) ||
			!newerUpgradeTime(stored.GetStateTransitionTime(), update.GetStateTransitionTime()) {
			return
		}
		stored.SetState(update.GetState())
		stored.SetMessage(update.GetMessage())
		stored.SetStateTransitionTime(update.GetStateTransitionTime())
		stored.ClearTimeoutTime()
		return
	}
	if update.GetTimeoutTime() != nil && stored.GetTimeoutTime() == nil &&
		proto.Equal(stored.GetStateTransitionTime(), update.GetStateTransitionTime()) &&
		newerUpgradeTime(stored.GetStateTransitionTime(), update.GetTimeoutTime()) {
		stored.SetTimeoutTime(update.GetTimeoutTime())
		stored.SetMessage(update.GetMessage())
	}
}

func newerUpgradeTime(last, next *timestamppb.Timestamp) bool {
	return last != nil && next != nil && next.AsTime().After(last.AsTime())
}

// validClusterUpgradePhaseTransition applies the component phase machine.
func validClusterUpgradePhaseTransition(from, to privatev1.ClusterUpgradeState) bool {
	switch from {
	case privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_PENDING:
		return to == privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_PROGRESSING ||
			to == privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_SUCCEEDED
	case privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_PROGRESSING:
		return to == privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_SUCCEEDED
	default:
		return false
	}
}

// isTerminalClusterUpgradeState reports whether an aggregate entry is immutable.
func isTerminalClusterUpgradeState(state privatev1.ClusterUpgradeState) bool {
	return state == privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_SUCCEEDED
}

// validateNodeSetVersionStatusUpdate rejects explicit attempts to address
// the server-owned version field in node-set status.
func validateNodeSetVersionStatusUpdate(request *privatev1.ClustersUpdateRequest) error {
	for _, path := range request.GetUpdateMask().GetPaths() {
		if strings.HasPrefix(path, "status.node_sets.") && strings.HasSuffix(path, ".version") {
			return grpcstatus.Errorf(grpccodes.InvalidArgument,
				"%s is server-owned", path)
		}
	}
	return nil
}

// preserveNodeSetVersions keeps the stored selected version for every
// existing node set. New node-set state is initialized by Fulfillment later in
// the update, and the scheduler may replace these values only after this
// caller-owned candidate has been restored from stored state.
func preserveNodeSetVersions(current, candidate *privatev1.Cluster) {
	if !candidate.HasStatus() {
		candidate.SetStatus(&privatev1.ClusterStatus{})
	}
	statuses := candidate.GetStatus().GetNodeSets()
	if statuses == nil {
		statuses = map[string]*privatev1.ClusterNodeSetStatus{}
		candidate.GetStatus().SetNodeSets(statuses)
	}
	for name, status := range statuses {
		if status == nil {
			continue
		}
		stored := current.GetStatus().GetNodeSets()[name]
		if stored == nil {
			status.ClearVersion()
			continue
		}
		status.SetVersion(cloneMessage(stored.GetVersion()))
	}
	// A stale status snapshot may omit a node set added by a concurrent
	// update. Restore its stored version when it is still in candidate spec.
	for name, nodeSet := range candidate.GetSpec().GetNodeSets() {
		if nodeSet == nil || statuses[name] != nil {
			continue
		}
		stored := current.GetStatus().GetNodeSets()[name]
		if stored == nil || stored.GetVersion() == nil {
			continue
		}
		ensureClusterNodeSetStatus(candidate, name).SetVersion(cloneMessage(stored.GetVersion()))
	}
}

// validateVersionUpdate validates and canonicalizes the selected cluster target.
func (s *PrivateClustersServer) validateVersionUpdate(ctx context.Context,
	request *privatev1.ClustersUpdateRequest) (*privatev1.ClusterVersion, error) {
	if updateMaskIncludesDescendantField(request.GetUpdateMask(), "spec.version") {
		return nil, grpcstatus.Errorf(grpccodes.InvalidArgument,
			"spec.version must be replaced as a complete reference")
	}
	if !updateIncludesField(request.GetUpdateMask(), "spec.version") {
		return nil, nil
	}
	newSpec := request.GetObject().GetSpec()
	if newSpec == nil {
		return nil, nil
	}
	existing, found, err := s.getExistingCluster(ctx, request)
	if err != nil || !found {
		return nil, err
	}
	// A full-object edit carries the stored target rather than selecting it.
	if request.GetUpdateMask() == nil && newSpec.GetVersion() != nil &&
		existing.GetSpec().GetVersion() != nil &&
		newSpec.GetVersion().GetId() == existing.GetSpec().GetVersion().GetId() {
		newSpec.SetVersion(cloneMessage(existing.GetSpec().GetVersion()))
		return nil, nil
	}
	version, resolved, err := s.validateVersionReferenceUpdate(ctx, existing.GetMetadata(),
		existing.GetSpec().GetVersion(), newSpec.GetVersion())
	if err != nil {
		return nil, err
	}
	newSpec.SetVersion(version)
	return resolved, nil
}

// validateVersionReferenceUpdate resolves a selected ClusterVersion.
func (s *PrivateClustersServer) validateVersionReferenceUpdate(ctx context.Context,
	owner *privatev1.Metadata, stored, selected *privatev1.ClusterVersionReference,
) (*privatev1.ClusterVersionReference, *privatev1.ClusterVersion, error) {
	if selected != nil && selected.GetId() == "" && selected.GetName() == "" {
		return nil, nil, grpcstatus.Error(grpccodes.InvalidArgument,
			"spec.version must specify id or name")
	}
	if selected == nil {
		if stored == nil {
			return nil, nil, nil
		}
		return cloneMessage(stored), nil, nil
	}
	version, err := resolveAndCanonicalizeReference(ctx, s.clusterVersionsDao, owner,
		selected, "version", grpccodes.InvalidArgument)
	if err != nil {
		return nil, nil, err
	}
	if err := validateResolvedClusterVersion(version, version.GetMetadata().GetName(), ""); err != nil {
		return nil, nil, err
	}
	return selected, version, nil
}

// advanceClusterUpgradeAfterControlPlaneSuccess schedules the frozen NodePool
// batch after control-plane success and retires successful attempts.
func advanceClusterUpgradeAfterControlPlaneSuccess(candidate *privatev1.Cluster) bool {
	attemptID := candidate.GetStatus().GetUpgradeAttemptId()
	active := candidate.GetStatus().GetActiveUpgradeAttempt()
	if attemptID == "" || active == nil || active.GetControlPlane() == nil {
		return false
	}
	entry := currentClusterUpgradeHistoryEntry(candidate, attemptID)
	if entry == nil {
		return false
	}
	if !isTerminalClusterUpgradeState(entry.GetState()) &&
		active.GetControlPlane().GetState() == privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_SUCCEEDED &&
		active.GetNodeSetsScheduledTime() == nil {
		selected := make([]string, 0, len(active.GetNodeSets()))
		for _, nodeSet := range active.GetNodeSets() {
			selected = append(selected, nodeSet.GetNodeSetId())
		}
		if len(selected) > 0 {
			active.SetNodeSetsScheduledTime(controllers.NowTruncated())
			applyClusterNodeSetUpgradeTargets(candidate, selected)
		}
	}
	if isTerminalClusterUpgradeState(entry.GetState()) &&
		clusterUpgradeAttemptDefinitive(active) {
		candidate.GetStatus().ClearActiveUpgradeAttempt()
		return true
	}
	return false
}

// clusterUpgradeAttemptDefinitive reports whether every frozen participant
// has succeeded and active tracking can be retired.
func clusterUpgradeAttemptDefinitive(active *privatev1.ClusterUpgradeAttemptState) bool {
	if active == nil || active.GetControlPlane() == nil {
		return false
	}
	state, _ := clusterUpgradeOutcome(active)
	return state == privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_SUCCEEDED
}

// currentClusterUpgradeHistoryEntry returns the entry owned by the active
// attempt pointer.
func currentClusterUpgradeHistoryEntry(candidate *privatev1.Cluster,
	attemptID string) *privatev1.ClusterUpgradeHistoryEntry {
	return clusterUpgradeHistoryEntryByID(candidate.GetStatus().GetUpgradeHistory(), attemptID)
}

// setClusterUpgradeHistoryState records a monotonic OSAC-owned transition.
func setClusterUpgradeHistoryState(entry *privatev1.ClusterUpgradeHistoryEntry,
	state privatev1.ClusterUpgradeState, message string, now *timestamppb.Timestamp) {
	if entry.GetState() == state {
		entry.SetMessage(message)
		return
	}
	if last := entry.GetStateTransitionTime(); last != nil && !now.AsTime().After(last.AsTime()) {
		now = timestamppb.New(last.AsTime().Add(time.Nanosecond))
	}
	entry.SetState(state)
	entry.SetMessage(message)
	entry.SetStateTransitionTime(now)
}
