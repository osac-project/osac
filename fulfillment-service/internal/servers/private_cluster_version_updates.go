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

// prepareClusterVersionChange applies the version rules to the completed
// candidate before it can be persisted.
func (s *PrivateClustersServer) prepareClusterVersionChange(ctx context.Context,
	current, candidate *privatev1.Cluster, mask *fieldmaskpb.FieldMask) error {
	controlPlaneChanged := pinUnsetNodeSetVersionsForUpdate(current, candidate, mask)
	if err := validateExclusiveVersionUpdate(current, candidate, mask); err != nil {
		return err
	}
	now := controllers.NowTruncated()
	change := findClusterVersionChange(current, candidate, controlPlaneChanged)
	if change == nil || change.target == nil {
		return nil
	}
	version, err := s.clusterVersionSemver(ctx, change.target)
	if err != nil {
		return err
	}
	appendClusterUpgradeAttempt(candidate, change, version, now)
	return nil
}

type clusterVersionChange struct {
	component     *privatev1.ClusterUpgradeComponent
	sourceVersion string
	target        *privatev1.ClusterVersionReference
	nodeCount     int32
}

// findClusterVersionChange returns the first version change
// represented by the update.
func findClusterVersionChange(current, candidate *privatev1.Cluster, controlPlaneChanged bool) *clusterVersionChange {
	if controlPlaneChanged {
		component := &privatev1.ClusterUpgradeComponent{}
		component.SetControlPlane(&emptypb.Empty{})
		return &clusterVersionChange{
			component:     component,
			sourceVersion: current.GetStatus().GetObservedVersion(),
			target:        candidate.GetSpec().GetVersion(),
		}
	}
	for name, nodeSet := range candidate.GetSpec().GetNodeSets() {
		stored := current.GetSpec().GetNodeSets()[name]
		if stored == nil || nodeSet == nil ||
			!nodeSetVersionChanged(stored, nodeSet, current.GetSpec().GetVersion()) {
			continue
		}
		component := &privatev1.ClusterUpgradeComponent{}
		component.SetNodeSetId(name)
		return &clusterVersionChange{
			component:     component,
			sourceVersion: current.GetStatus().GetNodeSets()[name].GetObservedVersion(),
			target:        nodeSet.GetVersion(),
			nodeCount:     max(1, nodeSet.GetSize()),
		}
	}
	return nil
}

// appendClusterUpgradeAttempt records a pending history entry
// for the accepted change.
func appendClusterUpgradeAttempt(candidate *privatev1.Cluster, change *clusterVersionChange,
	targetVersion string, now *timestamppb.Timestamp) {
	if !candidate.HasStatus() {
		candidate.SetStatus(&privatev1.ClusterStatus{})
	}
	if change.component.GetControlPlane() != nil {
		// Metering splits control-plane version usage at acceptance time. An
		// unchanged selection does not create an attempt or re-stamp this time.
		candidate.GetStatus().SetVersionTransitionTime(now)
	}
	entry := &privatev1.ClusterUpgradeHistoryEntry{}
	entry.SetComponent(change.component)
	entry.SetSourceVersion(change.sourceVersion)
	entry.SetTargetVersion(targetVersion)
	entry.SetAcceptedTime(now)
	entry.SetStateTransitionTime(now)
	entry.SetState(privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_PENDING)
	entry.SetAttemptId(uuid.New())
	entry.SetTimeoutNodeCount(change.nodeCount)
	candidate.GetStatus().SetUpgradeHistory(append(
		[]*privatev1.ClusterUpgradeHistoryEntry{entry}, candidate.GetStatus().GetUpgradeHistory()...))
	if change.component.GetControlPlane() != nil {
		candidate.GetSpec().SetControlPlaneAttemptId(entry.GetAttemptId())
	} else {
		candidate.GetSpec().GetNodeSetAttemptIds()[change.component.GetNodeSetId()] = entry.GetAttemptId()
	}
}

func (s *PrivateClustersServer) clusterVersionSemver(ctx context.Context,
	ref *privatev1.ClusterVersionReference) (string, error) {
	version, err := s.clusterVersionsDao.Get().SetId(ref.GetId()).Do(ctx)
	if err != nil {
		return "", err
	}
	return version.GetObject().GetSpec().GetVersion(), nil
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

func populateUnsetNodeSetVersions(nodeSets map[string]*privatev1.ClusterNodeSet, version *privatev1.ClusterVersionReference) {
	if version == nil {
		return
	}
	for _, nodeSet := range nodeSets {
		if nodeSet != nil && refKey(nodeSet.GetVersion()) == "" {
			nodeSet.SetVersion(cloneMessage(version))
		}
	}
}

func (s *PrivateClustersServer) validateCreatedNodeSetVersions(ctx context.Context, cluster *privatev1.Cluster) error {
	spec := cluster.GetSpec()
	for name, nodeSet := range spec.GetNodeSets() {
		version, err := s.validateNewNodeSetVersion(ctx, cluster.GetMetadata(), spec.GetVersion(), nodeSet.GetVersion(), name)
		if err != nil {
			return err
		}
		nodeSet.SetVersion(version)
	}
	return nil
}

// retainOmittedNodeSetVersion keeps the stored node-set version, or the
// control-plane version for a newly added node set, when no version is selected.
func retainOmittedNodeSetVersion(controlPlaneVersion *privatev1.ClusterVersionReference,
	existing, nodeSet *privatev1.ClusterNodeSet) {
	if nodeSet != nil && refKey(nodeSet.GetVersion()) == "" {
		version := controlPlaneVersion
		if existing != nil {
			version = existing.GetVersion()
		}
		if version != nil {
			nodeSet.SetVersion(cloneMessage(version))
		}
	}
}

// pinUnsetNodeSetVersionsForUpdate preserves the established control-plane
// version for node sets that have no stored version of their own.
func pinUnsetNodeSetVersionsForUpdate(current, candidate *privatev1.Cluster, mask *fieldmaskpb.FieldMask) bool {
	versionOnlyNodeSet := len(mask.GetPaths()) == 1 &&
		strings.HasPrefix(mask.GetPaths()[0], "spec.node_sets.") &&
		strings.HasSuffix(mask.GetPaths()[0], ".version")
	controlPlaneChanged := updateIncludesField(mask, "spec.version") &&
		!sameClusterVersionReference(current.GetSpec().GetVersion(), candidate.GetSpec().GetVersion())
	if (updateIncludesField(mask, "spec.node_sets") && !versionOnlyNodeSet) || controlPlaneChanged {
		populateUnsetNodeSetVersions(candidate.GetSpec().GetNodeSets(), current.GetSpec().GetVersion())
	}
	return controlPlaneChanged
}

// validateExclusiveVersionUpdate allows one existing component's version to
// change per edit. A node-set version change cannot share an edit with a size
// change. A legacy node set without a stored version already follows the stored
// control-plane version; pinning that baseline is not an upgrade.
func validateExclusiveVersionUpdate(current, candidate *privatev1.Cluster, mask *fieldmaskpb.FieldMask) error {
	changes := 0
	sizeChanged := false
	changedNodeSet := ""
	if !sameClusterVersionReference(current.GetSpec().GetVersion(), candidate.GetSpec().GetVersion()) {
		changes++
	}
	for name, nodeSet := range candidate.GetSpec().GetNodeSets() {
		stored, exists := current.GetSpec().GetNodeSets()[name]
		if !exists || nodeSet == nil {
			continue
		}
		if stored.GetSize() != nodeSet.GetSize() || stored.HasSize() != nodeSet.HasSize() {
			sizeChanged = true
		}
		if nodeSetVersionChanged(stored, nodeSet, current.GetSpec().GetVersion()) {
			changes++
			changedNodeSet = name
			if changes > 1 {
				return grpcstatus.Errorf(grpccodes.InvalidArgument,
					"only one component version can be changed per edit: control plane or one node set")
			}
		}
	}
	if changedNodeSet != "" && sizeChanged {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"node-set size and node-set version cannot be changed in the same update")
	}
	if changedNodeSet != "" && (len(mask.GetPaths()) != 1 ||
		mask.GetPaths()[0] != "spec.node_sets."+changedNodeSet+".version") {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"node-set version changes require a mask containing only spec.node_sets.%s.version", changedNodeSet)
	}
	return nil
}

func nodeSetVersionChanged(stored, candidate *privatev1.ClusterNodeSet,
	controlPlaneVersion *privatev1.ClusterVersionReference) bool {
	baseline := stored.GetVersion()
	if refKey(baseline) == "" {
		baseline = controlPlaneVersion
	}
	target := candidate.GetVersion()
	return refKey(target) != "" && !sameClusterVersionReference(baseline, target)
}

func sameClusterVersionReference(a, b *privatev1.ClusterVersionReference) bool {
	if a.GetId() != "" && b.GetId() != "" {
		return a.GetId() == b.GetId()
	}
	return a.GetName() == b.GetName()
}

// validateVersionUpdate uses the existing control-plane catalog checks for
// changed control-plane and node-set selections. Unset legacy node-set versions
// use the stored control-plane version for comparison.
func (s *PrivateClustersServer) validateVersionUpdate(ctx context.Context,
	request *privatev1.ClustersUpdateRequest) error {
	mask := request.GetUpdateMask()
	controlPlaneUpdated := updateIncludesField(mask, "spec.version")
	nodeSetsUpdated := s.updateAffectsNodeSets(mask)
	if !controlPlaneUpdated && !nodeSetsUpdated {
		return nil
	}
	newSpec := request.GetObject().GetSpec()
	if newSpec == nil {
		return nil
	}
	existing, found, err := s.getExistingCluster(ctx, request)
	if err != nil || !found {
		return err
	}
	if err := validateNodeSetVersionMask(mask, existing.GetSpec().GetNodeSets()); err != nil {
		return err
	}
	if controlPlaneUpdated {
		version, err := s.validateVersionReferenceUpdate(ctx, existing.GetMetadata(),
			existing.GetSpec().GetVersion(), newSpec.GetVersion())
		if err != nil {
			return err
		}
		newSpec.SetVersion(version)
	}
	if !nodeSetsUpdated || (len(mask.GetPaths()) > 0 && s.isUpdatingOnlySizes(mask)) {
		return nil
	}
	return s.validateNodeSetVersionUpdates(ctx, existing, newSpec)
}

func validateNodeSetVersionMask(mask *fieldmaskpb.FieldMask,
	existingNodeSets map[string]*privatev1.ClusterNodeSet) error {
	for _, path := range mask.GetPaths() {
		if !strings.HasPrefix(path, "spec.node_sets.") || !strings.HasSuffix(path, ".version") {
			continue
		}
		if len(mask.GetPaths()) != 1 {
			return grpcstatus.Error(grpccodes.InvalidArgument,
				"a node-set version update cannot include other fields")
		}
		name := strings.TrimSuffix(strings.TrimPrefix(path, "spec.node_sets."), ".version")
		if existingNodeSets[name] == nil {
			return grpcstatus.Errorf(grpccodes.InvalidArgument, "unknown node set '%s'", name)
		}
	}
	return nil
}

func (s *PrivateClustersServer) validateNodeSetVersionUpdates(ctx context.Context,
	existing *privatev1.Cluster, newSpec *privatev1.ClusterSpec) error {
	controlPlaneVersion := existing.GetSpec().GetVersion()
	for name, nodeSet := range newSpec.GetNodeSets() {
		if nodeSet == nil {
			continue
		}
		storedNodeSet := existing.GetSpec().GetNodeSets()[name]
		if storedNodeSet == nil {
			version, err := s.validateNewNodeSetVersion(ctx, existing.GetMetadata(), controlPlaneVersion, nodeSet.GetVersion(), name)
			if err != nil {
				return err
			}
			nodeSet.SetVersion(version)
			continue
		}
		if refKey(nodeSet.GetVersion()) == "" {
			continue
		}
		if err := s.validateExistingNodeSetVersionUpdate(ctx, existing, name, storedNodeSet, nodeSet); err != nil {
			return err
		}
	}
	return nil
}

func (s *PrivateClustersServer) validateExistingNodeSetVersionUpdate(ctx context.Context, existing *privatev1.Cluster,
	name string, storedNodeSet, candidateNodeSet *privatev1.ClusterNodeSet) error {
	stored := storedNodeSet.GetVersion()
	legacy := refKey(stored) == ""
	if legacy {
		stored = existing.GetSpec().GetVersion()
	}
	version, err := s.validateVersionReferenceUpdate(ctx, existing.GetMetadata(), stored, candidateNodeSet.GetVersion())
	if err != nil {
		return grpcstatus.Errorf(grpcstatus.Code(err), "node set '%s': %s", name, grpcstatus.Convert(err).Message())
	}
	if legacy && sameClusterVersionReference(stored, version) {
		candidateNodeSet.SetVersion(nil)
	} else {
		candidateNodeSet.SetVersion(version)
	}
	return nil
}

func (s *PrivateClustersServer) validateNewNodeSetVersion(ctx context.Context, owner *privatev1.Metadata,
	controlPlaneVersion, selected *privatev1.ClusterVersionReference, name string) (*privatev1.ClusterVersionReference, error) {
	version, err := s.validateVersionReferenceUpdate(ctx, owner, controlPlaneVersion, selected)
	if err != nil {
		return nil, grpcstatus.Errorf(grpcstatus.Code(err), "node set '%s': %s", name, grpcstatus.Convert(err).Message())
	}
	if !sameClusterVersionReference(controlPlaneVersion, version) {
		return nil, grpcstatus.Errorf(grpccodes.InvalidArgument,
			"node set '%s': version must match the control-plane version", name)
	}
	return version, nil
}

// validateVersionReferenceUpdate preserves an omitted or unchanged selection;
// changed selections use the same catalog lookup for control planes and node sets.
func (s *PrivateClustersServer) validateVersionReferenceUpdate(ctx context.Context,
	owner *privatev1.Metadata, stored, selected *privatev1.ClusterVersionReference,
) (*privatev1.ClusterVersionReference, error) {
	if refKey(selected) == "" || sameClusterVersionReference(stored, selected) {
		if stored == nil {
			return nil, nil
		}
		return cloneMessage(stored), nil
	}
	version, err := resolveAndCanonicalizeReference(ctx, s.clusterVersionsDao, owner,
		selected, "version", grpccodes.InvalidArgument)
	if err != nil {
		return nil, err
	}
	if err := validateResolvedClusterVersion(version, version.GetMetadata().GetName(), ""); err != nil {
		return nil, err
	}
	if sameClusterVersionReference(stored, selected) {
		return cloneMessage(stored), nil
	}
	return selected, nil
}
