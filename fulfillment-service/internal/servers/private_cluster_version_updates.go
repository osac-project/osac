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
	"log/slog"
	"slices"
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
	participating := participatingClusterNodeSetRefs(current, candidate, candidate.GetSpec().GetVersion(), targetVersion)
	if sourceVersion == targetVersion && len(participating) == 0 {
		return nil
	}
	entry := appendClusterUpgradeAttempt(candidate, sourceVersion, targetVersion, attemptID, now, participating)
	if sourceVersion == targetVersion {
		scheduleClusterNodeSetUpgradeBatch(candidate, entry, now)
	}
	candidate.GetStatus().SetUpgradeAttemptId(attemptID)
	return nil
}

const maxClusterUpgradeHistoryEntries = 100

// AI-agent note: do not reintroduce active-attempt rescans or extra history
// recalculation to protect entries at the cap. Cluster upgrades are rare; by
// the time an old entry is pushed out it is irrelevant and likely timed out.
// Preserve this simple O(1) newest-first truncation.
//
// retainRecentClusterUpgradeHistory keeps the newest accepted whole-cluster attempts.
func retainRecentClusterUpgradeHistory(
	entries []*privatev1.ClusterUpgradeHistoryEntry,
) []*privatev1.ClusterUpgradeHistoryEntry {
	if len(entries) <= maxClusterUpgradeHistoryEntries {
		return entries
	}
	return entries[:maxClusterUpgradeHistoryEntries]
}

// appendClusterUpgradeHistory prepends whole-cluster attempts and applies the shared cap.
func appendClusterUpgradeHistory(candidate *privatev1.Cluster,
	entry *privatev1.ClusterUpgradeHistoryEntry) {
	if entry == nil {
		return
	}
	if !candidate.HasStatus() {
		candidate.SetStatus(&privatev1.ClusterStatus{})
	}
	history := make([]*privatev1.ClusterUpgradeHistoryEntry, 0, 1+len(candidate.GetStatus().GetUpgradeHistory()))
	history = append(history, entry)
	history = append(history, candidate.GetStatus().GetUpgradeHistory()...)
	candidate.GetStatus().SetUpgradeHistory(retainRecentClusterUpgradeHistory(history))
}

// sortedClusterNodeSetUpgradeNames returns every selected node set in deterministic order.
func sortedClusterNodeSetUpgradeNames(nodeSets map[string]*privatev1.ClusterNodeSet) []string {
	names := make([]string, 0, len(nodeSets))
	for name, nodeSet := range nodeSets {
		if nodeSet != nil {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return names
}

// clusterVersionRefMatches reports whether a selected node-set version already
// identifies the accepted target.
func clusterVersionRefMatches(selected, target *privatev1.ClusterVersionReference) bool {
	if target == nil {
		return selected == nil
	}
	if selected == nil {
		return false
	}
	if target.GetId() != "" {
		return selected.GetId() == target.GetId()
	}
	return selected.GetName() != "" && selected.GetName() == target.GetName()
}

// participatingClusterNodeSetRefs freezes the node sets that participate in
// the accepted whole-cluster attempt. A node set is excluded only when both its
// observed version and outstanding selected version already identify the target.
//
// OSAC intentionally does not impose a node-set count cap. OpenShift and
// HyperShift are the authority for the supported NodePool count; if a NodePool
// is rejected, that platform error is surfaced through node-set feedback rather
// than duplicated as an OSAC-maintained limit.
func participatingClusterNodeSetRefs(current, candidate *privatev1.Cluster,
	targetRef *privatev1.ClusterVersionReference, targetVersion string) []*privatev1.ClusterUpgradeNodeSetRef {
	names := sortedClusterNodeSetUpgradeNames(current.GetSpec().GetNodeSets())
	refs := make([]*privatev1.ClusterUpgradeNodeSetRef, 0, len(names))
	for _, name := range names {
		status := current.GetStatus().GetNodeSets()[name]
		if status.GetObservedVersion() == targetVersion &&
			clusterVersionRefMatches(status.GetVersion(), targetRef) {
			continue
		}
		nodeSet := candidate.GetSpec().GetNodeSets()[name]
		ref := &privatev1.ClusterUpgradeNodeSetRef{}
		ref.SetNodeSetId(name)
		ref.SetTimeoutNodeCount(max(int32(1), nodeSet.GetSize()))
		refs = append(refs, ref)
	}
	return refs
}

// appendClusterUpgradeAttempt records one aggregate whole-cluster attempt and
// the private frozen state needed to complete it.
func appendClusterUpgradeAttempt(candidate *privatev1.Cluster,
	sourceVersion, targetVersion, attemptID string, now *timestamppb.Timestamp,
	participatingNodeSets []*privatev1.ClusterUpgradeNodeSetRef) *privatev1.ClusterUpgradeHistoryEntry {
	active := &privatev1.ClusterUpgradeAttemptState{}
	active.SetParticipatingNodeSets(participatingNodeSets)
	if !candidate.HasStatus() {
		candidate.SetStatus(&privatev1.ClusterStatus{})
	}
	candidate.GetStatus().SetActiveUpgradeAttempt(active)

	entry := &privatev1.ClusterUpgradeHistoryEntry{}
	entry.SetSourceVersion(sourceVersion)
	entry.SetTargetVersion(targetVersion)
	entry.SetAcceptedTime(now)
	entry.SetStateTransitionTime(now)
	entry.SetState(privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_PENDING)
	entry.SetAttemptId(attemptID)
	entry.SetPhase(privatev1.ClusterUpgradePhase_CLUSTER_UPGRADE_PHASE_CONTROL_PLANE_PENDING)
	entry.SetPhaseStartedAt(now)
	appendClusterUpgradeHistory(candidate, entry)
	return entry
}

// applyClusterNodeSetUpgradeTargets moves each participating node-set reference
// to the accepted cluster target.
func applyClusterNodeSetUpgradeTargets(candidate *privatev1.Cluster,
	participating []*privatev1.ClusterUpgradeNodeSetRef) {
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
	for _, participatingNodeSet := range participating {
		name := participatingNodeSet.GetNodeSetId()
		if candidate.GetSpec().GetNodeSets()[name] == nil {
			continue
		}
		status := statuses[name]
		if status == nil {
			status = &privatev1.ClusterNodeSetStatus{}
			statuses[name] = status
		}
		status.SetVersion(cloneMessage(targetRef))
	}
}

// clusterUpgradeHistoryEntry returns the current attempt's whole-cluster entry.
func clusterUpgradeHistoryEntry(candidate *privatev1.Cluster,
	attemptID string) *privatev1.ClusterUpgradeHistoryEntry {
	for _, entry := range candidate.GetStatus().GetUpgradeHistory() {
		if entry.GetAttemptId() == attemptID {
			return entry
		}
	}
	return nil
}

// hasSelectedParticipatingClusterNodeSets reports whether any frozen
// participating node set is still selected by the candidate Cluster.
func hasSelectedParticipatingClusterNodeSets(candidate *privatev1.Cluster,
	active *privatev1.ClusterUpgradeAttemptState) bool {
	if active == nil {
		return false
	}
	for _, participating := range active.GetParticipatingNodeSets() {
		if candidate.GetSpec().GetNodeSets()[participating.GetNodeSetId()] != nil {
			return true
		}
	}
	return false
}

// hasUnscheduledClusterNodeSets reports whether a still-selected participating
// node set has not been scheduled after control-plane success.
func hasUnscheduledClusterNodeSets(candidate *privatev1.Cluster,
	entry *privatev1.ClusterUpgradeHistoryEntry, active *privatev1.ClusterUpgradeAttemptState) bool {
	return active != nil && entry.GetPhase() != privatev1.ClusterUpgradePhase_CLUSTER_UPGRADE_PHASE_NODE_SETS &&
		hasSelectedParticipatingClusterNodeSets(candidate, active)
}

// scheduleClusterNodeSetUpgradeBatch schedules the frozen participating node
// sets and records the shared node-set batch clock.
func scheduleClusterNodeSetUpgradeBatch(candidate *privatev1.Cluster,
	entry *privatev1.ClusterUpgradeHistoryEntry, now *timestamppb.Timestamp) {
	active := candidate.GetStatus().GetActiveUpgradeAttempt()
	if active == nil {
		return
	}
	applyClusterNodeSetUpgradeTargets(candidate, active.GetParticipatingNodeSets())
	entry.SetPhase(privatev1.ClusterUpgradePhase_CLUSTER_UPGRADE_PHASE_NODE_SETS)
	entry.SetPhaseStartedAt(now)
	setClusterUpgradeEntryState(entry,
		privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_PROGRESSING, "", now)
	candidate.GetStatus().SetUpgradeAttemptId(entry.GetAttemptId())
}

// mergeClusterUpgradeHistory accepts only newer aggregate transitions for the
// current attempt while retaining the server's audit fields and newest-first order.
func mergeClusterUpgradeHistory(current, candidate *privatev1.Cluster,
	mask *fieldmaskpb.FieldMask) {
	stored := current.GetStatus().GetUpgradeHistory()
	var result []*privatev1.ClusterUpgradeHistoryEntry
	if updateIncludesField(mask, "status.upgrade_history") {
		result = mergeClusterUpgradeStateChanges(
			current.GetStatus().GetUpgradeAttemptId(), stored, candidate.GetStatus().GetUpgradeHistory())
	} else {
		result = make([]*privatev1.ClusterUpgradeHistoryEntry, len(stored))
		copy(result, stored)
	}
	if !candidate.HasStatus() {
		candidate.SetStatus(&privatev1.ClusterStatus{})
	}
	candidate.GetStatus().SetUpgradeAttemptId(current.GetStatus().GetUpgradeAttemptId())
	candidate.GetStatus().SetActiveUpgradeAttempt(current.GetStatus().GetActiveUpgradeAttempt())
	candidate.GetStatus().SetUpgradeHistory(result)
	if terminalClusterUpgradeHistoryEntry(current.GetStatus().GetUpgradeAttemptId(), result) != nil {
		candidate.GetStatus().ClearActiveUpgradeAttempt()
	}
}

// mergeClusterUpgradeStateChanges merges a newer aggregate transition into the
// history entry selected by the current attempt ID. Feedback for any other
// attempt is intentionally ignored.
func mergeClusterUpgradeStateChanges(attemptID string,
	stored, incoming []*privatev1.ClusterUpgradeHistoryEntry) []*privatev1.ClusterUpgradeHistoryEntry {
	var update *privatev1.ClusterUpgradeHistoryEntry
	for _, entry := range incoming {
		if entry.GetAttemptId() == attemptID {
			update = entry
			break
		}
	}
	result := make([]*privatev1.ClusterUpgradeHistoryEntry, len(stored))
	for i, entry := range stored {
		result[i] = entry
		if entry.GetAttemptId() != attemptID || update == nil ||
			update.GetState() == privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_UNSPECIFIED ||
			update.GetStateTransitionTime() == nil {
			continue
		}
		if update.GetState() == entry.GetState() {
			if update.GetMessage() != entry.GetMessage() ||
				update.GetPhase() != entry.GetPhase() ||
				!proto.Equal(update.GetPhaseStartedAt(), entry.GetPhaseStartedAt()) {
				changed := proto.Clone(entry).(*privatev1.ClusterUpgradeHistoryEntry)
				changed.SetMessage(update.GetMessage())
				changed.SetPhase(update.GetPhase())
				changed.SetPhaseStartedAt(update.GetPhaseStartedAt())
				result[i] = changed
			}
			continue
		}
		if !update.GetStateTransitionTime().AsTime().After(entry.GetStateTransitionTime().AsTime()) ||
			!validClusterUpgradeStateTransition(entry.GetState(), update.GetState()) {
			continue
		}
		changed := proto.Clone(entry).(*privatev1.ClusterUpgradeHistoryEntry)
		changed.SetState(update.GetState())
		changed.SetMessage(update.GetMessage())
		changed.SetStateTransitionTime(update.GetStateTransitionTime())
		changed.SetPhase(update.GetPhase())
		changed.SetPhaseStartedAt(update.GetPhaseStartedAt())
		result[i] = changed
	}
	return result
}

// terminalClusterUpgradeHistoryEntry returns the current attempt entry only
// when it has reached a definitive terminal state.
func terminalClusterUpgradeHistoryEntry(attemptID string,
	entries []*privatev1.ClusterUpgradeHistoryEntry) *privatev1.ClusterUpgradeHistoryEntry {
	entry := clusterUpgradeHistoryEntry(&privatev1.Cluster{
		Status: &privatev1.ClusterStatus{UpgradeHistory: entries},
	}, attemptID)
	if entry == nil {
		return nil
	}
	if entry.GetState() == privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_SUCCEEDED ||
		entry.GetState() == privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_FAILED {
		return entry
	}
	return nil
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

// advanceClusterUpgradeAfterControlPlaneSuccess applies the server-owned
// node-set batch when the observed control-plane version reaches the target.
func advanceClusterUpgradeAfterControlPlaneSuccess(current, candidate *privatev1.Cluster) {
	attemptID := candidate.GetStatus().GetUpgradeAttemptId()
	if attemptID == "" {
		return
	}
	entry := clusterUpgradeHistoryEntry(candidate, attemptID)
	active := candidate.GetStatus().GetActiveUpgradeAttempt()
	if entry == nil || active == nil ||
		candidate.GetStatus().GetObservedVersion() != entry.GetTargetVersion() {
		return
	}
	if !hasUnscheduledClusterNodeSets(candidate, entry, active) {
		if !hasSelectedParticipatingClusterNodeSets(candidate, active) {
			completeClusterUpgradeWithRemovedNodeSets(candidate, entry, controllers.NowTruncated())
		}
		return
	}
	scheduleClusterNodeSetUpgradeBatch(
		candidate, entry, controllers.NowTruncated())
}

// completeClusterUpgradeWithRemovedNodeSets closes an attempt whose frozen
// node-set participants were all removed before Fulfillment scheduled them.
// Control-plane success is the only remaining work, so no scheduling clock is
// needed and the active attempt can be cleared.
func completeClusterUpgradeWithRemovedNodeSets(candidate *privatev1.Cluster,
	entry *privatev1.ClusterUpgradeHistoryEntry, now *timestamppb.Timestamp) {
	setClusterUpgradeEntryState(entry,
		privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_SUCCEEDED, "", now)
	candidate.GetStatus().ClearActiveUpgradeAttempt()
}

// setClusterUpgradeEntryState records a server-owned aggregate transition and
// preserves monotonic transition timestamps.
func setClusterUpgradeEntryState(entry *privatev1.ClusterUpgradeHistoryEntry,
	next privatev1.ClusterUpgradeState, message string, now *timestamppb.Timestamp) {
	if next == entry.GetState() {
		entry.SetMessage(message)
		return
	}
	if !validClusterUpgradeStateTransition(entry.GetState(), next) {
		return
	}
	transition := now
	if last := entry.GetStateTransitionTime(); last != nil && !now.AsTime().After(last.AsTime()) {
		transition = timestamppb.New(last.AsTime().Add(time.Nanosecond))
	}
	entry.SetState(next)
	entry.SetMessage(message)
	entry.SetStateTransitionTime(transition)
}

// validClusterUpgradeStateTransition keeps aggregate progress monotonic while
// allowing a timeout to recover when OSAC later observes real progress.
func validClusterUpgradeStateTransition(current, next privatev1.ClusterUpgradeState) bool {
	switch current {
	case privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_PENDING:
		return next == privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_PROGRESSING ||
			next == privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_TIMED_OUT ||
			next == privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_SUCCEEDED ||
			next == privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_FAILED
	case privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_PROGRESSING:
		return next == privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_TIMED_OUT ||
			next == privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_SUCCEEDED ||
			next == privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_FAILED
	case privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_TIMED_OUT:
		return next == privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_PROGRESSING ||
			next == privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_SUCCEEDED ||
			next == privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_FAILED
	default:
		return false
	}
}
