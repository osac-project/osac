import type { TFunction } from 'i18next';
import { type SemVer, compare, eq, gt, parse } from 'semver';

import { type Cluster, ClusterState, type ClusterVersion, ClusterVersionState } from '@osac/types';

export interface ClusterUpgradeChoiceResult {
  eligibleTargets: ClusterVersion[];
  blockedReason?: string;
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

const blocked = (blockedReason: string): ClusterUpgradeChoiceResult => ({
  eligibleTargets: [],
  blockedReason,
});

export const resolveClusterUpgradeChoices = (
  cluster: Cluster,
  configuredVersion: ClusterVersion | undefined,
  candidateCatalog: readonly ClusterVersion[],
  t: TFunction,
): ClusterUpgradeChoiceResult => {
  switch (cluster.status?.state) {
    case ClusterState.FAILED:
      return blocked(t('The cluster is in a failed state.'));
    case ClusterState.DELETING:
      return blocked(t('The cluster is being deleted.'));
    case ClusterState.DELETE_FAILED:
      return blocked(t('The cluster deletion failed.'));
  }

  if (!configuredVersion) {
    return blocked(t('The configured cluster version could not be found.'));
  }
  const configured = parseStrictSemVer(configuredVersion.spec?.version);
  if (!configured) {
    return blocked(t('The configured cluster version is invalid.'));
  }

  const observedVersion = cluster.status?.observedVersion;
  const controlPlane = parseStrictSemVer(observedVersion);
  if (!controlPlane) {
    return blocked(t('The current control plane version is not available yet.'));
  }
  if (!eq(controlPlane, configured)) {
    return blocked(
      t('The control plane is at {{observedVersion}}; wait for {{desiredVersion}}.', {
        observedVersion,
        desiredVersion: configuredVersion.spec?.version,
      }),
    );
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
      return blocked(
        nodeSetVersion
          ? t('Node set {{nodeSetId}} reported an invalid observed version: {{observedVersion}}.', {
              nodeSetId: id,
              observedVersion: nodeSetVersion,
            })
          : t('Node set {{nodeSetId}} has not reported an observed version.', { nodeSetId: id }),
      );
    }
    if (!eq(observed, configured)) {
      return blocked(
        t('Node set {{nodeSetId}} is at {{observedVersion}}; wait for {{desiredVersion}}.', {
          nodeSetId: id,
          observedVersion: nodeSetVersion,
          desiredVersion: configuredVersion.spec?.version,
        }),
      );
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
    return blocked(
      skewRejected
        ? t('Available targets exceed the supported node set version skew.')
        : t('No eligible upgrade target is available.'),
    );
  }
  return {
    eligibleTargets: eligible
      .sort((left, right) => compare(right.version, left.version))
      .map(({ target }) => target),
  };
};
