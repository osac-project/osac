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
	"sort"
	"strings"

	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"
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
	if sourceVersion == targetVersion {
		scheduleClusterNodeSetUpgradeBatch(current, candidate, targetVersion, attemptID, now)
	} else {
		appendClusterUpgradeAttempt(candidate, sourceVersion, targetVersion, attemptID, now)
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
// retainRecentClusterUpgradeHistory keeps the newest accepted entries.
func retainRecentClusterUpgradeHistory(
	entries []*privatev1.ClusterUpgradeHistoryEntry,
) []*privatev1.ClusterUpgradeHistoryEntry {
	if len(entries) <= maxClusterUpgradeHistoryEntries {
		return entries
	}
	return entries[:maxClusterUpgradeHistoryEntries]
}

// appendClusterUpgradeHistory prepends entries and applies the shared cap for
// every upgrade component.
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

// appendClusterUpgradeAttempt records the accepted control-plane component
// of a whole-cluster attempt.
func appendClusterUpgradeAttempt(candidate *privatev1.Cluster,
	sourceVersion, targetVersion, attemptID string, now *timestamppb.Timestamp) {
	component := &privatev1.ClusterUpgradeComponent{}
	component.SetControlPlane(&emptypb.Empty{})
	entry := &privatev1.ClusterUpgradeHistoryEntry{}
	entry.SetComponent(component)
	entry.SetSourceVersion(sourceVersion)
	entry.SetTargetVersion(targetVersion)
	entry.SetAcceptedTime(now)
	entry.SetStateTransitionTime(now)
	entry.SetState(privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_PENDING)
	entry.SetAttemptId(attemptID)
	appendClusterUpgradeHistory(candidate, []*privatev1.ClusterUpgradeHistoryEntry{entry})
}

// sortedClusterNodeSetUpgradeNames returns every participating node set in
// deterministic order.
func sortedClusterNodeSetUpgradeNames(nodeSets map[string]*privatev1.ClusterNodeSet) []string {
	names := make([]string, 0, len(nodeSets))
	for name, nodeSet := range nodeSets {
		if nodeSet != nil {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
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

// appendClusterNodeSetUpgradeHistory records one entry per participating node
// set under the shared attempt ID.
func appendClusterNodeSetUpgradeHistory(current, candidate *privatev1.Cluster,
	names []string, targetVersion, attemptID string, now *timestamppb.Timestamp) {
	entries := make([]*privatev1.ClusterUpgradeHistoryEntry, 0, len(names))
	for _, name := range names {
		nodeSet := candidate.GetSpec().GetNodeSets()[name]
		sourceVersion := current.GetStatus().GetNodeSets()[name].GetObservedVersion()
		isNewNodeSet := current.GetSpec().GetNodeSets()[name] == nil
		if isNewNodeSet {
			// A new node set is created with the candidate control-plane
			// version; it does not participate in an upgrade from an older
			// version
			sourceVersion = targetVersion
		}
		component := &privatev1.ClusterUpgradeComponent{}
		component.SetNodeSetId(name)
		entry := &privatev1.ClusterUpgradeHistoryEntry{}
		entry.SetComponent(component)
		entry.SetSourceVersion(sourceVersion)
		entry.SetTargetVersion(targetVersion)
		entry.SetAcceptedTime(now)
		entry.SetStateTransitionTime(now)
		entry.SetAttemptId(attemptID)
		entry.SetTimeoutNodeCount(max(int32(1), nodeSet.GetSize()))
		if sourceVersion == targetVersion {
			entry.SetState(privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_SUCCEEDED)
		} else {
			entry.SetState(privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_PENDING)
		}
		entries = append(entries, entry)
	}
	appendClusterUpgradeHistory(candidate, entries)
}

// scheduleClusterNodeSetUpgradeBatch moves every participating node-set
// reference and history entry to one shared cluster attempt.
func scheduleClusterNodeSetUpgradeBatch(current, candidate *privatev1.Cluster,
	targetVersion, attemptID string, now *timestamppb.Timestamp) {
	names := sortedClusterNodeSetUpgradeNames(candidate.GetSpec().GetNodeSets())
	if len(names) == 0 {
		return
	}
	applyClusterNodeSetUpgradeTargets(candidate, names)
	appendClusterNodeSetUpgradeHistory(current, candidate, names, targetVersion, attemptID, now)
	candidate.GetStatus().SetUpgradeAttemptId(attemptID)
}

// mergeClusterUpgradeHistory accepts private status transitions by attempt and
// component while retaining the server's audit fields and newest-first order.
func mergeClusterUpgradeHistory(current, candidate *privatev1.Cluster,
	mask *fieldmaskpb.FieldMask) {
	stored := current.GetStatus().GetUpgradeHistory()
	var result []*privatev1.ClusterUpgradeHistoryEntry
	if updateIncludesField(mask, "status.upgrade_history") {
		result = mergeClusterUpgradeStateChanges(stored, candidate.GetStatus().GetUpgradeHistory())
	} else {
		result = make([]*privatev1.ClusterUpgradeHistoryEntry, len(stored))
		copy(result, stored)
	}
	if !candidate.HasStatus() {
		candidate.SetStatus(&privatev1.ClusterStatus{})
	}
	candidate.GetStatus().SetUpgradeAttemptId(current.GetStatus().GetUpgradeAttemptId())
	candidate.GetStatus().SetUpgradeHistory(result)
}

// mergeClusterUpgradeStateChanges merges only newer state
// transitions into stored history.
func mergeClusterUpgradeStateChanges(
	stored, incoming []*privatev1.ClusterUpgradeHistoryEntry,
) []*privatev1.ClusterUpgradeHistoryEntry {
	byAttemptAndComponent := make(map[clusterUpgradeHistoryKey]*privatev1.ClusterUpgradeHistoryEntry, len(incoming))
	for _, entry := range incoming {
		if entry.GetAttemptId() != "" && entry.GetComponent() != nil {
			byAttemptAndComponent[upgradeHistoryKey(entry)] = entry
		}
	}
	result := make([]*privatev1.ClusterUpgradeHistoryEntry, len(stored))
	for i, entry := range stored {
		result[i] = entry
		update := byAttemptAndComponent[upgradeHistoryKey(entry)]
		if update == nil || update.GetState() == privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_UNSPECIFIED ||
			update.GetState() == entry.GetState() || update.GetStateTransitionTime() == nil ||
			!update.GetStateTransitionTime().AsTime().After(entry.GetStateTransitionTime().AsTime()) {
			continue
		}
		changed := proto.Clone(entry).(*privatev1.ClusterUpgradeHistoryEntry)
		changed.SetState(update.GetState())
		changed.SetMessage(update.GetMessage())
		changed.SetStateTransitionTime(update.GetStateTransitionTime())
		result[i] = changed
	}
	return result
}

type clusterUpgradeHistoryKey struct {
	attemptID    string
	nodeSetID    string
	controlPlane bool
}

// upgradeHistoryKey distinguishes components that share an attempt ID.
func upgradeHistoryKey(entry *privatev1.ClusterUpgradeHistoryEntry) clusterUpgradeHistoryKey {
	return clusterUpgradeHistoryKey{
		attemptID:    entry.GetAttemptId(),
		nodeSetID:    entry.GetComponent().GetNodeSetId(),
		controlPlane: entry.GetComponent().GetControlPlane() != nil,
	}
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
// node-set batch when feedback completes the accepted control-plane component
// of a whole-cluster attempt.
func advanceClusterUpgradeAfterControlPlaneSuccess(current, candidate *privatev1.Cluster) {
	attemptID := candidate.GetStatus().GetUpgradeAttemptId()
	if attemptID == "" {
		return
	}
	targetVersion := ""
	for _, entry := range candidate.GetStatus().GetUpgradeHistory() {
		if entry.GetAttemptId() != attemptID || entry.GetComponent().GetControlPlane() == nil ||
			entry.GetState() != privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_SUCCEEDED {
			continue
		}
		targetVersion = entry.GetTargetVersion()
		break
	}
	if targetVersion == "" {
		return
	}
	for _, entry := range candidate.GetStatus().GetUpgradeHistory() {
		if entry.GetAttemptId() == attemptID && entry.GetComponent().GetNodeSetId() != "" {
			return
		}
	}
	scheduleClusterNodeSetUpgradeBatch(
		current, candidate, targetVersion, attemptID, controllers.NowTruncated())
}
