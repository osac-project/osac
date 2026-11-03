import { type SemVer, compare, eq, gt, lt, parse } from 'semver';

import { type Cluster, ClusterState, type ClusterVersion, ClusterVersionState } from '@osac/types';

import { matchesClusterVersionReference } from '../../../api/v1/cluster-version-reference';

export type ClusterUpgradeBlockedReason =
  | 'cluster-failed'
  | 'cluster-deleting'
  | 'cluster-delete-failed'
  | 'configured-version-unresolved'
  | 'configured-version-invalid'
  | 'control-plane-observation-unavailable'
  | 'node-set-observation-unavailable'
  | 'components-not-converged'
  | 'no-eligible-target'
  | 'node-set-ahead'
  | 'unsupported-skew';

export interface ClusterUpgradeTargetRejection {
  target: ClusterVersion;
  reason: 'node-set-ahead' | 'unsupported-skew';
}

export interface ClusterUpgradeChoiceResult {
  eligibleTargets: ClusterVersion[];
  rejectedTargets: ClusterUpgradeTargetRejection[];
  blockedReason?: ClusterUpgradeBlockedReason;
}

const SEMVER_2_FORMAT = /^\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$/;

const parseStrictSemVer = (version?: string): SemVer | null => {
  if (!version || version !== version.trim() || !SEMVER_2_FORMAT.test(version)) {
    return null;
  }
  return parse(version);
};

const blocked = (blockedReason: ClusterUpgradeBlockedReason): ClusterUpgradeChoiceResult => ({
  eligibleTargets: [],
  rejectedTargets: [],
  blockedReason,
});

export const resolveClusterUpgradeChoices = (
  cluster: Cluster,
  configuredVersion: ClusterVersion | undefined,
  candidateCatalog: readonly ClusterVersion[],
): ClusterUpgradeChoiceResult => {
  switch (cluster.status?.state) {
    case ClusterState.FAILED:
      return blocked('cluster-failed');
    case ClusterState.DELETING:
      return blocked('cluster-deleting');
    case ClusterState.DELETE_FAILED:
      return blocked('cluster-delete-failed');
  }

  const reference = cluster.spec?.version;
  if (
    !reference ||
    !configuredVersion ||
    !matchesClusterVersionReference(reference, configuredVersion)
  ) {
    return blocked('configured-version-unresolved');
  }
  const configured = parseStrictSemVer(configuredVersion.spec?.version);
  if (!configured) {
    return blocked('configured-version-invalid');
  }

  const controlPlane = parseStrictSemVer(cluster.status?.observedVersion);
  if (!controlPlane) {
    return blocked('control-plane-observation-unavailable');
  }

  const observedNodeSets = cluster.status?.nodeSets ?? {};
  const nodeSetIds = new Set([
    ...Object.keys(cluster.spec?.nodeSets ?? {}),
    ...Object.keys(observedNodeSets),
  ]);
  const nodeSetVersions: SemVer[] = [];
  for (const id of nodeSetIds) {
    const observed = parseStrictSemVer(observedNodeSets[id]?.observedVersion);
    if (!observed) {
      return blocked('node-set-observation-unavailable');
    }
    nodeSetVersions.push(observed);
  }

  const eligible: { target: ClusterVersion; version: SemVer }[] = [];
  const rejectedTargets: ClusterUpgradeTargetRejection[] = [];
  for (const target of candidateCatalog) {
    const spec = target.spec;
    if (
      spec?.enabled !== true ||
      (spec.state !== ClusterVersionState.ACTIVE && spec.state !== ClusterVersionState.DEPRECATED)
    ) {
      continue;
    }
    const targetVersion = parseStrictSemVer(spec.version);
    if (!targetVersion || !gt(targetVersion, controlPlane)) {
      continue;
    }
    if (nodeSetVersions.some((observed) => lt(targetVersion, observed))) {
      rejectedTargets.push({ target, reason: 'node-set-ahead' });
      continue;
    }
    if (
      nodeSetVersions.some(
        (observed) =>
          targetVersion.major !== observed.major || targetVersion.minor - observed.minor > 3,
      )
    ) {
      rejectedTargets.push({ target, reason: 'unsupported-skew' });
      continue;
    }
    eligible.push({ target, version: targetVersion });
  }

  const result: ClusterUpgradeChoiceResult = {
    eligibleTargets: eligible
      .sort((left, right) => compare(right.version, left.version))
      .map(({ target }) => target),
    rejectedTargets,
  };

  if (
    !eq(controlPlane, configured) ||
    nodeSetVersions.some((observed) => !eq(observed, configured))
  ) {
    return { ...result, blockedReason: 'components-not-converged' };
  }
  if (result.eligibleTargets.length > 0) {
    return result;
  }
  const rejectedReasons = new Set(rejectedTargets.map(({ reason }) => reason));
  if (rejectedReasons.size === 1) {
    return { ...result, blockedReason: rejectedTargets[0].reason };
  }
  return { ...result, blockedReason: 'no-eligible-target' };
};
