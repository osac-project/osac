import type { TFunction } from 'i18next';
import { type SemVer, compare, gt, parse } from 'semver';

import { type ClusterVersion, ClusterVersionState } from '@osac/types';

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
  configuredVersion: ClusterVersion | undefined,
  candidateCatalog: readonly ClusterVersion[],
  t: TFunction,
  sourceVersion = configuredVersion?.spec?.version,
): ClusterUpgradeChoiceResult => {
  if (!configuredVersion) {
    return blocked(t('The configured cluster version could not be found.'));
  }
  const configured = parseStrictSemVer(configuredVersion.spec?.version);
  if (!configured) {
    return blocked(t('The configured cluster version is invalid.'));
  }
  const source = parseStrictSemVer(sourceVersion);
  if (!source) {
    return blocked(t('Upgrade eligibility is unavailable.'));
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
    if (!targetVersion || !gt(targetVersion, source) || compare(targetVersion, configured) === 0) {
      continue;
    }
    if (
      targetVersion.major !== source.major ||
      targetVersion.minor - source.minor > MAX_MINOR_VERSION_SKEW
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
