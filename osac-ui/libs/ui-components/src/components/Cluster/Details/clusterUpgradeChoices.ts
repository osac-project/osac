import { type SemVer, compare, eq, gt, parse } from 'semver';

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
  | 'unsupported-skew';

export interface ClusterUpgradeChoiceResult {
  eligibleTargets: ClusterVersion[];
  blockedReason?: ClusterUpgradeBlockedReason;
  blockedNodeSetId?: string;
  blockedObservedVersion?: string;
}

const MAX_MINOR_VERSION_SKEW = 3;

const parseStrictSemVer = (version?: string): SemVer | null => {
  if (!version) {
    return null;
  }
  const parsed = parse(version);
  const canonical = parsed
    ? `${parsed.version}${parsed.build.length ? `+${parsed.build.join('.')}` : ''}`
    : undefined;
  return canonical === version ? parsed : null;
};

const blocked = (
  blockedReason: ClusterUpgradeBlockedReason,
  details: Pick<ClusterUpgradeChoiceResult, 'blockedNodeSetId' | 'blockedObservedVersion'> = {},
): ClusterUpgradeChoiceResult => ({ eligibleTargets: [], blockedReason, ...details });

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

  const observedVersion = cluster.status?.observedVersion;
  const controlPlane = parseStrictSemVer(observedVersion);
  if (!controlPlane) {
    return blocked('control-plane-observation-unavailable');
  }
  if (!eq(controlPlane, configured)) {
    return blocked('components-not-converged', { blockedObservedVersion: observedVersion });
  }

  const observedNodeSets = cluster.status?.nodeSets ?? {};
  const nodeSetIds = new Set([
    ...Object.keys(cluster.spec?.nodeSets ?? {}),
    ...Object.keys(observedNodeSets),
  ]);
  for (const id of nodeSetIds) {
    const nodeSetVersion = observedNodeSets[id]?.observedVersion;
    const observed = parseStrictSemVer(nodeSetVersion);
    if (!observed) {
      return blocked('node-set-observation-unavailable', {
        blockedNodeSetId: id,
        blockedObservedVersion: nodeSetVersion,
      });
    }
    if (!eq(observed, configured)) {
      return blocked('components-not-converged', {
        blockedNodeSetId: id,
        blockedObservedVersion: nodeSetVersion,
      });
    }
  }

  const eligible: { target: ClusterVersion; version: SemVer }[] = [];
  let skewRejected = false;
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
    if (
      targetVersion.major !== configured.major ||
      targetVersion.minor - configured.minor > MAX_MINOR_VERSION_SKEW
    ) {
      skewRejected = true;
      continue;
    }
    eligible.push({ target, version: targetVersion });
  }

  if (eligible.length === 0) {
    return blocked(skewRejected ? 'unsupported-skew' : 'no-eligible-target');
  }
  return {
    eligibleTargets: eligible
      .sort((left, right) => compare(right.version, left.version))
      .map(({ target }) => target),
  };
};
