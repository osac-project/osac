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
	"log/slog"
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

// prepareClusterVersionChange applies whole-cluster version rules to the
// completed candidate before it can be persisted.
func (s *PrivateClustersServer) prepareClusterVersionChange(ctx context.Context,
	current, candidate *privatev1.Cluster, mask *fieldmaskpb.FieldMask,
	resolvedTarget *privatev1.ClusterVersion) error {
	controlPlaneChanged := updateIncludesField(mask, "spec.version") &&
		current.GetSpec().GetVersion().GetId() != candidate.GetSpec().GetVersion().GetId()
	if !controlPlaneChanged {
		return nil
	}
	source, err := getClusterVersionByID(ctx, s.clusterVersionsDao, current.GetSpec().GetVersion().GetId())
	if err != nil {
		return err
	}
	if resolvedTarget == nil {
		return grpcstatus.Error(grpccodes.Internal, "resolved target ClusterVersion is missing")
	}
	targetVersion := resolvedTarget.GetSpec().GetVersion()
	// A semantic-version match is a reference replacement, not an upgrade.
	// Comparing versions rather than image strings also handles tag and digest
	// forms of the same release.
	if source.GetSpec().GetVersion() == targetVersion {
		retireClusterUpgradeAttemptForReferenceReplacement(candidate, targetVersion)
		s.logger.InfoContext(ctx,
			"Replaced ClusterVersion reference with the same semantic version; no upgrade attempt created",
			slog.String("cluster_id", current.GetId()),
			slog.String("component", "control_plane"),
			slog.String("previous_cluster_version_id", source.GetId()),
			slog.String("new_cluster_version_id", resolvedTarget.GetId()),
		)
		return nil
	}

	attemptID := uuid.New()
	now := controllers.NowTruncated()
	candidate.GetStatus().SetVersionTransitionTime(now)
	sourceVersion := current.GetStatus().GetObservedVersion()
	if sourceVersion == "" {
		return grpcstatus.Error(grpccodes.FailedPrecondition,
			"cluster status observed_version is required before an upgrade")
	}
	createClusterUpgradeAttempt(current, candidate, sourceVersion, targetVersion, attemptID, now)
	return nil
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

// participatingClusterNodeSetNames freezes the existing node sets that still
// need to reach the accepted target version.
func participatingClusterNodeSetNames(current, candidate *privatev1.Cluster,
	targetVersion string) []string {
	names := make([]string, 0, len(current.GetSpec().GetNodeSets()))
	for name := range current.GetSpec().GetNodeSets() {
		if candidate.GetSpec().GetNodeSets()[name] == nil ||
			current.GetStatus().GetNodeSets()[name].GetObservedVersion() == targetVersion {
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
	names := participatingClusterNodeSetNames(current, candidate, targetVersion)
	controlPlaneSucceeded := sourceVersion == targetVersion
	if controlPlaneSucceeded && len(names) == 0 {
		return
	}

	controlPlaneState := privatev1.ClusterUpgradeControlPlaneState_builder{
		AcceptedTime:        now,
		StateTransitionTime: now,
		State:               privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_PENDING,
	}.Build()
	if controlPlaneSucceeded {
		controlPlaneState.SetState(privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_SUCCEEDED)
	}

	nodeSets := make([]*privatev1.ClusterUpgradeNodeSetState, 0, len(names))
	for _, name := range names {
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
	entryState := privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_PENDING
	entryMessage := "Waiting for control plane upgrade"
	if controlPlaneSucceeded {
		active.SetNodeSetsScheduledTime(now)
		entryState = privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_PROGRESSING
		entryMessage = "Node set upgrades in progress"
	}

	entry := privatev1.ClusterUpgradeHistoryEntry_builder{
		AttemptId:           attemptID,
		SourceVersion:       sourceVersion,
		TargetVersion:       targetVersion,
		AcceptedTime:        now,
		StateTransitionTime: now,
		State:               entryState,
		Message:             entryMessage,
	}.Build()

	if controlPlaneSucceeded {
		applyClusterNodeSetUpgradeTargets(candidate, names)
	}
	appendClusterUpgradeHistory(candidate, []*privatev1.ClusterUpgradeHistoryEntry{entry})
	candidate.GetStatus().SetActiveUpgradeAttempt(active)
	candidate.GetStatus().SetUpgradeAttemptId(attemptID)
}

// applyClusterNodeSetUpgradeTargets moves each participating node-set reference
// to the accepted cluster target.
func applyClusterNodeSetUpgradeTargets(candidate *privatev1.Cluster, names []string) {
	targetRef := candidate.GetSpec().GetVersion()
	if targetRef == nil {
		return
	}
	if !candidate.HasStatus() {
		candidate.SetStatus(&privatev1.ClusterStatus{})
	}
	statuses := candidate.GetStatus().GetNodeSets()
	if statuses == nil {
		statuses = map[string]*privatev1.ClusterNodeSetStatus{}
		candidate.GetStatus().SetNodeSets(statuses)
	}
	for _, name := range names {
		status := statuses[name]
		if status == nil {
			status = &privatev1.ClusterNodeSetStatus{}
			statuses[name] = status
		}
		status.SetVersion(cloneMessage(targetRef))
	}
}

// mergeClusterUpgradeHistory preserves server-owned history and validates
// feedback for the current attempt.
func mergeClusterUpgradeHistory(current, candidate *privatev1.Cluster,
	mask *fieldmaskpb.FieldMask, requestActive *privatev1.ClusterUpgradeAttemptState) {
	attemptID := current.GetStatus().GetUpgradeAttemptId()
	stored := current.GetStatus().GetUpgradeHistory()
	result := slices.Clone(stored)
	update := clusterUpgradeHistoryEntryByID(candidate.GetStatus().GetUpgradeHistory(), attemptID)
	feedbackRequested := updateIncludesField(mask, "status.active_upgrade_attempt") ||
		updateIncludesField(mask, "status.upgrade_history")
	incomingAttemptID := clusterUpgradeFeedbackAttemptID(attemptID, candidate, mask)

	if feedbackRequested && incomingAttemptID != attemptID {
		if !candidate.HasStatus() {
			candidate.SetStatus(&privatev1.ClusterStatus{})
		}
		candidate.GetStatus().SetUpgradeAttemptId(attemptID)
		restoreClusterObservedVersions(current, candidate)
		mergedActive, removedParticipants := removeDeletedClusterUpgradeParticipants(
			current.GetStatus().GetActiveUpgradeAttempt(), candidate)
		if entry := clusterUpgradeHistoryEntryByID(stored, attemptID); entry != nil {
			applyClusterUpgradeOutcome(entry, mergedActive, candidate,
				len(removedParticipants) > 0 || !proto.Equal(current.GetStatus().GetActiveUpgradeAttempt(), mergedActive),
				removedParticipants, controllers.NowTruncated())
		}
		candidate.GetStatus().SetActiveUpgradeAttempt(mergedActive)
		candidate.GetStatus().SetUpgradeHistory(stored)
		return
	}

	if updateIncludesField(mask, "status.active_upgrade_attempt") && requestActive != nil {
		restoreStaleUpgradeStatus(current, candidate, requestActive)
	}

	if updateIncludesField(mask, "status.upgrade_history") {
		for i, entry := range stored {
			if entry.GetAttemptId() != attemptID {
				continue
			}
			if update != nil {
				result[i] = mergeClusterUpgradeHistoryEntry(entry, update)
			}
			break
		}
	}
	if !candidate.HasStatus() {
		candidate.SetStatus(&privatev1.ClusterStatus{})
	}
	candidate.GetStatus().SetUpgradeAttemptId(attemptID)
	var mergedActive *privatev1.ClusterUpgradeAttemptState
	if updateIncludesField(mask, "status.active_upgrade_attempt") {
		mergedActive = mergeClusterUpgradeAttemptState(current, candidate, attemptID, requestActive)
	} else {
		mergedActive = current.GetStatus().GetActiveUpgradeAttempt()
	}
	mergedActive, removedParticipants := removeDeletedClusterUpgradeParticipants(mergedActive, candidate)
	if entry := clusterUpgradeHistoryEntryByID(result, attemptID); entry != nil {
		applyClusterUpgradeOutcome(entry, mergedActive, candidate,
			len(removedParticipants) > 0 || !proto.Equal(current.GetStatus().GetActiveUpgradeAttempt(), mergedActive),
			removedParticipants, controllers.NowTruncated())
	}
	candidate.GetStatus().SetActiveUpgradeAttempt(mergedActive)
	candidate.GetStatus().SetUpgradeHistory(result)
}

// restoreStaleUpgradeStatus applies the component-state freshness decision to
// the observations in the same request. A stale active-state snapshot cannot
// roll back observations or replace the status of a removed participant.
// Observation-only requests carry no active state and are left unchanged.
func restoreStaleUpgradeStatus(current, candidate *privatev1.Cluster,
	requestActive *privatev1.ClusterUpgradeAttemptState) {
	storedActive := current.GetStatus().GetActiveUpgradeAttempt()
	if storedActive == nil {
		restoreClusterObservedVersions(current, candidate)
		return
	}

	storedControlPlane := storedActive.GetControlPlane()
	requestControlPlane := requestActive.GetControlPlane()
	if requestControlPlane == nil || !newerClusterUpgradeTransition(
		storedControlPlane.GetState(), requestControlPlane.GetState(),
		storedControlPlane.GetStateTransitionTime(), requestControlPlane.GetStateTransitionTime()) {
		candidate.GetStatus().SetObservedVersion(current.GetStatus().GetObservedVersion())
	}

	storedNodeSets := make(map[string]*privatev1.ClusterUpgradeNodeSetState,
		len(storedActive.GetNodeSets()))
	for _, nodeSet := range storedActive.GetNodeSets() {
		storedNodeSets[nodeSet.GetNodeSetId()] = nodeSet
	}
	requestNodeSets := make(map[string]*privatev1.ClusterUpgradeNodeSetState,
		len(requestActive.GetNodeSets()))
	for _, nodeSet := range requestActive.GetNodeSets() {
		requestNodeSets[nodeSet.GetNodeSetId()] = nodeSet
	}
	for name, storedNodeSet := range storedNodeSets {
		requestNodeSet := requestNodeSets[name]
		if requestNodeSet != nil && newerClusterUpgradeTransition(
			storedNodeSet.GetState(), requestNodeSet.GetState(),
			storedNodeSet.GetStateTransitionTime(), requestNodeSet.GetStateTransitionTime()) {
			continue
		}
		restoreClusterNodeSetStatus(current, candidate, name)
	}
	for name := range requestNodeSets {
		if _, selected := storedNodeSets[name]; !selected {
			restoreClusterNodeSetStatus(current, candidate, name)
		}
	}
}

// restoreClusterNodeSetStatus restores the complete status for one node set.
// A replacement incarnation has no stored status until the operator reports it.
func restoreClusterNodeSetStatus(current, candidate *privatev1.Cluster, name string) {
	statuses := candidate.GetStatus().GetNodeSets()
	if statuses == nil {
		return
	}
	stored := current.GetStatus().GetNodeSets()[name]
	if stored == nil {
		delete(statuses, name)
		return
	}
	statuses[name] = proto.Clone(stored).(*privatev1.ClusterNodeSetStatus)
}

// clusterUpgradeFeedbackAttemptID determines the attempt named by feedback. A
// current attempt requires matching history; after retirement, only an explicitly
// named history entry identifies superseded feedback. Omitted attempt feedback
// leaves independent observations unaffected.
func clusterUpgradeFeedbackAttemptID(attemptID string, candidate *privatev1.Cluster,
	mask *fieldmaskpb.FieldMask) string {
	if attemptID != "" {
		requestAttemptID := candidate.GetStatus().GetUpgradeAttemptId()
		if requestAttemptID != "" && requestAttemptID != attemptID {
			return ""
		}
		if update := clusterUpgradeHistoryEntryByID(
			candidate.GetStatus().GetUpgradeHistory(), attemptID); update != nil {
			return attemptID
		}
		return ""
	}
	// After retirement, retained audit entries alone do not identify feedback.
	// Require the request's attempt pointer to name an entry in the history
	// payload; a complete status snapshot with no attempt pointer stays neutral.
	namedAttemptID := candidate.GetStatus().GetUpgradeAttemptId()
	if namedAttemptID == "" || !updateIncludesField(mask, "status.upgrade_history") {
		return ""
	}
	if clusterUpgradeHistoryEntryByID(candidate.GetStatus().GetUpgradeHistory(),
		namedAttemptID) == nil {
		return ""
	}
	return namedAttemptID
}

// restoreClusterObservedVersions rejects observation changes that arrive with
// superseded attempt feedback. Observation-only requests still merge normally.
func restoreClusterObservedVersions(current, candidate *privatev1.Cluster) {
	candidate.GetStatus().SetObservedVersion(current.GetStatus().GetObservedVersion())
	storedStatuses := current.GetStatus().GetNodeSets()
	statuses := make(map[string]*privatev1.ClusterNodeSetStatus, len(storedStatuses))
	for name, status := range storedStatuses {
		if status == nil {
			continue
		}
		statuses[name] = proto.Clone(status).(*privatev1.ClusterNodeSetStatus)
	}
	candidate.GetStatus().SetNodeSets(statuses)
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

// mergeClusterUpgradeHistoryEntry applies a valid newer transition to the
// stored aggregate entry while preserving its audit fields.
func mergeClusterUpgradeHistoryEntry(stored, update *privatev1.ClusterUpgradeHistoryEntry,
) *privatev1.ClusterUpgradeHistoryEntry {
	// Terminal outcomes are derived from participant evidence after component
	// state has been merged. A history-only status is not sufficient evidence.
	if isTerminalClusterUpgradeState(update.GetState()) {
		return stored
	}
	if !newerClusterUpgradeTransition(stored.GetState(), update.GetState(),
		stored.GetStateTransitionTime(), update.GetStateTransitionTime()) {
		return stored
	}
	if update.GetState() != stored.GetState() {
		return stored
	}
	changed := proto.Clone(stored).(*privatev1.ClusterUpgradeHistoryEntry)
	changed.SetMessage(update.GetMessage())
	return changed
}

// retireClusterUpgradeAttemptForReferenceReplacement stops feedback for an
// attempt whose target no longer matches the selected semantic version. The
// history remains, but Fulfillment no longer owns an active attempt.
func retireClusterUpgradeAttemptForReferenceReplacement(candidate *privatev1.Cluster,
	targetVersion string) {
	attemptID := candidate.GetStatus().GetUpgradeAttemptId()
	if attemptID == "" || candidate.GetStatus().GetActiveUpgradeAttempt() == nil {
		return
	}
	entry := clusterUpgradeHistoryEntryByID(
		candidate.GetStatus().GetUpgradeHistory(), attemptID)
	if entry.GetTargetVersion() == targetVersion {
		return
	}
	candidate.GetStatus().SetUpgradeAttemptId("")
	candidate.GetStatus().ClearActiveUpgradeAttempt()
}

// mergeClusterUpgradeAttemptState validates component feedback against the
// frozen active attempt. A nil incoming value cannot clear a nonterminal
// attempt.
func mergeClusterUpgradeAttemptState(current, candidate *privatev1.Cluster,
	attemptID string, requestActive *privatev1.ClusterUpgradeAttemptState) *privatev1.ClusterUpgradeAttemptState {
	storedEntry := clusterUpgradeHistoryEntryByID(
		current.GetStatus().GetUpgradeHistory(), attemptID)
	if storedEntry != nil && isTerminalClusterUpgradeState(storedEntry.GetState()) {
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
	if controlPlane := merged.GetControlPlane(); controlPlane != nil {
		mergeClusterUpgradeComponentState(controlPlane, incoming.GetControlPlane())
	}
	incomingNodeSets := make(map[string]*privatev1.ClusterUpgradeNodeSetState,
		len(incoming.GetNodeSets()))
	for _, nodeSet := range incoming.GetNodeSets() {
		incomingNodeSets[nodeSet.GetNodeSetId()] = nodeSet
	}
	// Worker state is mutable only when the request itself carries the same
	// release marker Fulfillment stored. The merged candidate cannot be used
	// here because a partial field mask can inherit the stored marker.
	workersScheduled := proto.Equal(merged.GetNodeSetsScheduledTime(),
		requestActive.GetNodeSetsScheduledTime()) && merged.GetNodeSetsScheduledTime() != nil
	for _, nodeSet := range merged.GetNodeSets() {
		if !workersScheduled {
			continue
		}
		mergeClusterUpgradeComponentState(nodeSet, incomingNodeSets[nodeSet.GetNodeSetId()])
	}
	return merged
}

// removeDeletedClusterUpgradeParticipants invalidates frozen membership when
// a caller accepts a node-set removal. Re-adding the same name later creates a
// new node set and does not restore the removed participant.
func removeDeletedClusterUpgradeParticipants(active *privatev1.ClusterUpgradeAttemptState,
	candidate *privatev1.Cluster) (*privatev1.ClusterUpgradeAttemptState, []string) {
	if active == nil {
		return nil, nil
	}
	merged := proto.Clone(active).(*privatev1.ClusterUpgradeAttemptState)
	removed := []string{}
	nodeSets := slices.DeleteFunc(merged.GetNodeSets(), func(nodeSet *privatev1.ClusterUpgradeNodeSetState) bool {
		if candidate.GetSpec().GetNodeSets()[nodeSet.GetNodeSetId()] == nil {
			removed = append(removed, nodeSet.GetNodeSetId())
			return true
		}
		return false
	})
	merged.SetNodeSets(nodeSets)
	return merged, removed
}

// applyClusterUpgradeOutcome records the aggregate state derived from merged
// participant feedback and the current spec. It does not trust an aggregate
// status supplied by the operator.
func applyClusterUpgradeOutcome(entry *privatev1.ClusterUpgradeHistoryEntry,
	active *privatev1.ClusterUpgradeAttemptState, candidate *privatev1.Cluster,
	participantChanged bool, removedParticipants []string, now *timestamppb.Timestamp) {
	if active == nil || active.GetControlPlane() == nil ||
		isTerminalClusterUpgradeState(entry.GetState()) {
		return
	}
	state, message, transitionTime := clusterUpgradeOutcome(clusterUpgradeOutcomeRequest{
		previous:               entry.GetState(),
		previousTransitionTime: entry.GetStateTransitionTime(),
		active:                 active,
		candidate:              candidate,
		removedParticipants:    removedParticipants,
		now:                    now,
	})
	if state == privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_SUCCEEDED && message == "" &&
		isClusterUpgradeRemovalMessage(entry.GetMessage()) {
		message = entry.GetMessage()
	}
	if state == entry.GetState() && state != privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_TIMED_OUT &&
		!participantChanged {
		return
	}
	if state == privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_TIMED_OUT {
		if last := entry.GetStateTransitionTime(); last != nil &&
			(transitionTime == nil || !transitionTime.AsTime().After(last.AsTime())) {
			transitionTime = now
		}
	}
	// A removal that lets every remaining participant complete the attempt records
	// the accepted membership change, not an earlier participant transition.
	if state == privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_SUCCEEDED &&
		len(removedParticipants) > 0 {
		transitionTime = now
	}
	setClusterUpgradeHistoryState(entry, state, message, transitionTime)
}

// clusterUpgradeOutcome derives the aggregate state and message from
// participants that remain selected by the candidate spec.
type clusterUpgradeOutcomeRequest struct {
	previous               privatev1.ClusterUpgradeState
	previousTransitionTime *timestamppb.Timestamp
	active                 *privatev1.ClusterUpgradeAttemptState
	candidate              *privatev1.Cluster
	removedParticipants    []string
	now                    *timestamppb.Timestamp
}

func clusterUpgradeOutcome(request clusterUpgradeOutcomeRequest) (
	privatev1.ClusterUpgradeState, string, *timestamppb.Timestamp) {
	previous := request.previous
	active := request.active
	candidate := request.candidate
	now := request.now
	controlPlane := active.GetControlPlane()
	if controlPlane.GetState() == privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_FAILED {
		message := controlPlane.GetMessage()
		if message == "" {
			message = "Control plane upgrade failed"
		}
		return privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_FAILED, message,
			controlPlane.GetStateTransitionTime()
	}

	evidence := newClusterUpgradeEvidence(active, previous,
		request.previousTransitionTime, request.removedParticipants)
	for _, nodeSet := range active.GetNodeSets() {
		if candidate.GetSpec().GetNodeSets()[nodeSet.GetNodeSetId()] != nil {
			evidence.observeNodeSet(nodeSet, previous)
		}
	}
	return evidence.outcome(now)
}

const clusterUpgradeRemovalMessagePrefix = "Participating node sets removed: "

// clusterUpgradeEvidence is the aggregate state machine's evidence accumulator.
// Participant transitions are collected first; outcome precedence is selected
// only after all selected participants have been observed.
type clusterUpgradeEvidence struct {
	allSucceeded            bool
	anyTimedOut             bool
	anyProgressing          bool
	controlPlaneProgressing bool
	controlPlaneSucceeded   bool
	workersScheduled        bool
	recovering              bool
	removalRecovery         bool
	removedNodeSets         []string
	failedNodeSet           *privatev1.ClusterUpgradeNodeSetState
	timeoutMessage          string
	timeoutTime             *timestamppb.Timestamp
	successTime             *timestamppb.Timestamp
	recoveryTime            *timestamppb.Timestamp
	recoveryFloor           *timestamppb.Timestamp
}

func newClusterUpgradeEvidence(active *privatev1.ClusterUpgradeAttemptState,
	previous privatev1.ClusterUpgradeState, previousTransitionTime *timestamppb.Timestamp,
	removedParticipants []string) clusterUpgradeEvidence {
	controlPlane := active.GetControlPlane()
	evidence := clusterUpgradeEvidence{
		allSucceeded: controlPlane.GetState() ==
			privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_SUCCEEDED,
		anyTimedOut: controlPlane.GetState() ==
			privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_TIMED_OUT,
		controlPlaneProgressing: controlPlane.GetState() ==
			privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_PROGRESSING,
		controlPlaneSucceeded: controlPlane.GetState() ==
			privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_SUCCEEDED,
		anyProgressing: controlPlane.GetState() ==
			privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_PROGRESSING ||
			(controlPlane.GetState() ==
				privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_SUCCEEDED &&
				len(active.GetNodeSets()) > 0),
		workersScheduled: active.GetNodeSetsScheduledTime() != nil,
		successTime:      controlPlane.GetStateTransitionTime(),
	}
	if evidence.successTime == nil {
		evidence.successTime = controlPlane.GetAcceptedTime()
	}
	if evidence.anyTimedOut {
		evidence.timeoutMessage = clusterUpgradeTimeoutReason(
			"Control plane", controlPlane.GetMessage())
		evidence.timeoutTime = controlPlane.GetStateTransitionTime()
	}
	evidence.recovering = previous ==
		privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_TIMED_OUT
	evidence.removedNodeSets = removedParticipants
	evidence.removalRecovery = evidence.recovering && len(removedParticipants) > 0
	if evidence.recovering {
		evidence.recoveryFloor = previousTransitionTime
	}
	if evidence.recovering && (controlPlane.GetState() ==
		privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_PROGRESSING ||
		controlPlane.GetState() == privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_SUCCEEDED) {
		evidence.observeRecovery(controlPlane.GetStateTransitionTime())
	}
	return evidence
}

func (evidence *clusterUpgradeEvidence) observeNodeSet(
	nodeSet *privatev1.ClusterUpgradeNodeSetState, previous privatev1.ClusterUpgradeState) {
	switch nodeSet.GetState() {
	case privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_FAILED:
		if evidence.failedNodeSet == nil {
			evidence.failedNodeSet = nodeSet
		}
		evidence.allSucceeded = false
	case privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_TIMED_OUT:
		evidence.anyTimedOut = true
		evidence.allSucceeded = false
		evidence.observeTimeout(nodeSet)
	case privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_PROGRESSING:
		evidence.anyProgressing = true
		evidence.allSucceeded = false
		if previous == privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_TIMED_OUT {
			evidence.observeRecovery(nodeSet.GetStateTransitionTime())
		}
	case privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_PENDING:
		evidence.allSucceeded = false
	case privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_SUCCEEDED:
		evidence.observeSuccess(nodeSet.GetStateTransitionTime())
		if previous == privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_TIMED_OUT {
			evidence.observeRecovery(nodeSet.GetStateTransitionTime())
		}
	}
}

func (evidence *clusterUpgradeEvidence) observeTimeout(
	nodeSet *privatev1.ClusterUpgradeNodeSetState) {
	if evidence.timeoutMessage == "" {
		evidence.timeoutMessage = clusterUpgradeTimeoutReason(
			fmt.Sprintf("Node set %q", nodeSet.GetNodeSetId()), nodeSet.GetMessage())
	}
	if t := nodeSet.GetStateTransitionTime(); t != nil &&
		(evidence.timeoutTime == nil || t.AsTime().Before(evidence.timeoutTime.AsTime())) {
		evidence.timeoutTime = t
	}
}

func (evidence *clusterUpgradeEvidence) observeSuccess(t *timestamppb.Timestamp) {
	if t != nil && (evidence.successTime == nil || t.AsTime().After(evidence.successTime.AsTime())) {
		evidence.successTime = t
	}
}

func (evidence *clusterUpgradeEvidence) observeRecovery(t *timestamppb.Timestamp) {
	if t == nil || (evidence.recoveryFloor != nil &&
		!t.AsTime().After(evidence.recoveryFloor.AsTime())) {
		return
	}
	if evidence.recoveryTime == nil || t.AsTime().After(evidence.recoveryTime.AsTime()) {
		evidence.recoveryTime = t
	}
}

func (evidence clusterUpgradeEvidence) stageMessage() string {
	if message := evidence.removalMessage(); message != "" {
		return message
	}
	if evidence.controlPlaneProgressing && !evidence.workersScheduled &&
		!evidence.allSucceeded {
		return "Control plane upgrade in progress"
	}
	if evidence.workersScheduled || evidence.controlPlaneSucceeded ||
		evidence.anyProgressing {
		return "Node set upgrades in progress"
	}
	return "Waiting for control plane upgrade"
}

func (evidence clusterUpgradeEvidence) removalMessage() string {
	if len(evidence.removedNodeSets) == 0 {
		return ""
	}
	return clusterUpgradeRemovalMessagePrefix + strings.Join(evidence.removedNodeSets, ", ")
}

func isClusterUpgradeRemovalMessage(message string) bool {
	return strings.HasPrefix(message, clusterUpgradeRemovalMessagePrefix)
}

func (evidence clusterUpgradeEvidence) outcome(now *timestamppb.Timestamp) (
	privatev1.ClusterUpgradeState, string, *timestamppb.Timestamp) {
	switch {
	case evidence.failedNodeSet != nil:
		message := evidence.failedNodeSet.GetMessage()
		if message == "" {
			message = fmt.Sprintf("Node set %q upgrade failed",
				evidence.failedNodeSet.GetNodeSetId())
		}
		return privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_FAILED, message,
			evidence.failedNodeSet.GetStateTransitionTime()
	case evidence.allSucceeded:
		transitionTime := evidence.successTime
		if evidence.removalRecovery {
			transitionTime = now
		}
		message := ""
		if len(evidence.removedNodeSets) > 0 {
			message = evidence.removalMessage()
		}
		return privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_SUCCEEDED,
			message, transitionTime
	case evidence.anyTimedOut:
		return privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_TIMED_OUT,
			evidence.timeoutMessage, evidence.timeoutTime
	case evidence.anyProgressing || evidence.workersScheduled:
		transitionTime := evidence.successTime
		if evidence.recovering {
			transitionTime = evidence.recoveryTime
			if evidence.removalRecovery || transitionTime == nil {
				transitionTime = now
			}
		}
		return privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_PROGRESSING,
			evidence.stageMessage(), transitionTime
	default:
		return privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_PENDING,
			evidence.stageMessage(), evidence.successTime
	}
}

// clusterUpgradeTimeoutReason preserves component-supplied detail and provides
// a component-identified fallback when the operator omitted the reason.
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
}

// mergeClusterUpgradeComponentState accepts only a valid newer transition and
// preserves Fulfillment-owned identity, acceptance, and timeout data.
func mergeClusterUpgradeComponentState[S any, P interface {
	clusterUpgradeComponentState
	*S
}](stored, update P) {
	if update == nil || !newerClusterUpgradeTransition(stored.GetState(),
		update.GetState(), stored.GetStateTransitionTime(), update.GetStateTransitionTime()) {
		return
	}
	if update.GetState() == stored.GetState() {
		stored.SetMessage(update.GetMessage())
		return
	}
	stored.SetState(update.GetState())
	stored.SetMessage(update.GetMessage())
	stored.SetStateTransitionTime(update.GetStateTransitionTime())
}

// newerClusterUpgradeTransition reports whether feedback supplies a valid,
// strictly newer transition.
func newerClusterUpgradeTransition(state, nextState privatev1.ClusterUpgradeState,
	last, next *timestamppb.Timestamp) bool {
	if isTerminalClusterUpgradeState(state) ||
		nextState == privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_UNSPECIFIED ||
		next == nil || last == nil || !next.AsTime().After(last.AsTime()) {
		return false
	}
	return nextState == state || validClusterUpgradeTransition(state, nextState)
}

// validClusterUpgradeTransition applies the aggregate state machine.
func validClusterUpgradeTransition(from, to privatev1.ClusterUpgradeState) bool {
	switch from {
	case privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_PENDING:
		return to == privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_PROGRESSING ||
			to == privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_TIMED_OUT ||
			to == privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_SUCCEEDED ||
			to == privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_FAILED
	case privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_PROGRESSING:
		return to == privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_TIMED_OUT ||
			to == privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_SUCCEEDED ||
			to == privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_FAILED
	case privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_TIMED_OUT:
		return to == privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_PROGRESSING ||
			to == privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_SUCCEEDED ||
			to == privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_FAILED
	default:
		return false
	}
}

// isTerminalClusterUpgradeState reports whether an aggregate entry is immutable.
func isTerminalClusterUpgradeState(state privatev1.ClusterUpgradeState) bool {
	return state == privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_SUCCEEDED ||
		state == privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_FAILED
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
		status := &privatev1.ClusterNodeSetStatus{}
		status.SetVersion(cloneMessage(stored.GetVersion()))
		statuses[name] = status
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
	version, resolved, err := s.validateVersionReferenceUpdate(ctx, existing.GetMetadata(),
		existing.GetSpec().GetVersion(), newSpec.GetVersion())
	if err != nil {
		return nil, err
	}
	newSpec.SetVersion(version)
	return resolved, nil
}

// validateVersionReferenceUpdate preserves an omitted or unchanged cluster target;
// changed selections use the shared ClusterVersion catalog lookup.
func (s *PrivateClustersServer) validateVersionReferenceUpdate(ctx context.Context,
	owner *privatev1.Metadata, stored, selected *privatev1.ClusterVersionReference,
) (*privatev1.ClusterVersionReference, *privatev1.ClusterVersion, error) {
	if selected != nil && selected.GetId() == "" && selected.GetName() == "" {
		return nil, nil, grpcstatus.Error(grpccodes.InvalidArgument,
			"spec.version must specify id or name")
	}
	if selected == nil || stored.GetId() == selected.GetId() {
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
	if stored.GetId() == selected.GetId() {
		return cloneMessage(stored), nil, nil
	}
	return selected, version, nil
}

// advanceClusterUpgradeAfterControlPlaneSuccess schedules the frozen node-set
// batch only after the active control-plane state has succeeded.
func advanceClusterUpgradeAfterControlPlaneSuccess(candidate *privatev1.Cluster) {
	attemptID := candidate.GetStatus().GetUpgradeAttemptId()
	active := candidate.GetStatus().GetActiveUpgradeAttempt()
	if attemptID == "" || active == nil || active.GetControlPlane() == nil {
		return
	}
	entry := currentClusterUpgradeHistoryEntry(candidate, attemptID)
	if entry == nil {
		return
	}
	now := controllers.NowTruncated()
	switch active.GetControlPlane().GetState() {
	case privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_SUCCEEDED:
		if isTerminalClusterUpgradeState(entry.GetState()) {
			break
		}
		selected := make([]string, 0, len(active.GetNodeSets()))
		for _, nodeSet := range active.GetNodeSets() {
			if candidate.GetSpec().GetNodeSets()[nodeSet.GetNodeSetId()] == nil {
				continue
			}
			selected = append(selected, nodeSet.GetNodeSetId())
		}
		if len(selected) == 0 {
			setClusterUpgradeHistoryState(entry,
				privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_SUCCEEDED, "", now)
			break
		}
		if active.GetNodeSetsScheduledTime() != nil {
			break
		}
		active.SetNodeSetsScheduledTime(now)
		for _, nodeSet := range active.GetNodeSets() {
			if nodeSet.GetState() == privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_UNSPECIFIED {
				nodeSet.SetState(privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_PENDING)
			}
		}
		applyClusterNodeSetUpgradeTargets(candidate, selected)
		setClusterUpgradeHistoryState(entry,
			privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_PROGRESSING,
			entry.GetMessage(), now)
	case privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_FAILED:
		message := active.GetControlPlane().GetMessage()
		if message == "" {
			message = "Control plane upgrade failed"
		}
		setClusterUpgradeHistoryState(entry,
			privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_FAILED, message, now)
	}
	if isTerminalClusterUpgradeState(entry.GetState()) {
		candidate.GetStatus().ClearActiveUpgradeAttempt()
	}
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
		if message != "" {
			entry.SetMessage(message)
		}
		return
	}
	if last := entry.GetStateTransitionTime(); last != nil && !now.AsTime().After(last.AsTime()) {
		now = timestamppb.New(last.AsTime().Add(time.Nanosecond))
	}
	entry.SetState(state)
	entry.SetMessage(message)
	entry.SetStateTransitionTime(now)
}
