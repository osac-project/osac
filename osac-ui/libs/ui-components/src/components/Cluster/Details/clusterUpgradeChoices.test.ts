import { create } from '@bufbuild/protobuf';
import type { TFunction } from 'i18next';
import { describe, expect, it } from 'vitest';

import { type ClusterVersion, ClusterVersionSchema, ClusterVersionState } from '@osac/types';

import { resolveClusterUpgradeChoices } from './clusterUpgradeChoices';

const t = ((key: string) => key) as TFunction;

const makeVersion = (
  id: string,
  version: string,
  state = ClusterVersionState.ACTIVE,
  enabled = true,
): ClusterVersion =>
  create(ClusterVersionSchema, {
    id,
    metadata: { name: id },
    spec: { version, state, enabled },
  });

const currentVersion = () => makeVersion('current', '4.16.0', ClusterVersionState.OBSOLETE, false);
const nextVersion = () => makeVersion('next', '4.17.0');

describe('resolveClusterUpgradeChoices', () => {
  it('offers enabled active and deprecated newer targets in descending semver order', () => {
    const catalog = [
      makeVersion('patch', '4.16.1'),
      makeVersion('older', '4.15.9'),
      makeVersion('minor-rc', '4.17.0-rc.1', ClusterVersionState.DEPRECATED),
      makeVersion('equal', '4.16.0+build.8'),
      makeVersion('disabled', '4.18.0', ClusterVersionState.ACTIVE, false),
      makeVersion('obsolete', '4.18.1', ClusterVersionState.OBSOLETE),
      makeVersion('invalid', 'not-semver'),
      makeVersion('minor', '4.17.0'),
    ];

    const result = resolveClusterUpgradeChoices(currentVersion(), catalog, t);

    expect(result.eligibleTargets.map((version) => version.id)).toEqual([
      'minor',
      'minor-rc',
      'patch',
    ]);
    expect(result.blockedReason).toBeUndefined();
  });

  it('offers different targets above the observed version after a failed request', () => {
    const failedTarget = makeVersion('failed-target', '4.18.0');
    const catalog = [
      makeVersion('patch', '4.17.1'),
      failedTarget,
      makeVersion('later', '4.19.0'),
      makeVersion('older', '4.16.9'),
    ];

    const result = resolveClusterUpgradeChoices(failedTarget, catalog, t, '4.17.0');

    expect(result.eligibleTargets.map((version) => version.id)).toEqual(['later', 'patch']);
    expect(result.blockedReason).toBeUndefined();
  });

  it('accepts a disabled obsolete configured version', () => {
    const result = resolveClusterUpgradeChoices(currentVersion(), [nextVersion()], t);

    expect(result.eligibleTargets.map((version) => version.id)).toEqual(['next']);
    expect(result.blockedReason).toBeUndefined();
  });

  it('blocks a missing or invalid configured version', () => {
    expect(resolveClusterUpgradeChoices(undefined, [nextVersion()], t).blockedReason).toBe(
      'The configured cluster version could not be found.',
    );
    const invalid = makeVersion('invalid', '4.16');
    expect(resolveClusterUpgradeChoices(invalid, [nextVersion()], t).blockedReason).toBe(
      'The configured cluster version is invalid.',
    );
  });

  it('rejects noncanonical configured and target version strings', () => {
    const configured = makeVersion('current', 'v4.16.0');
    expect(resolveClusterUpgradeChoices(configured, [nextVersion()], t).blockedReason).toBe(
      'The configured cluster version is invalid.',
    );
    expect(
      resolveClusterUpgradeChoices(currentVersion(), [makeVersion('prefixed', 'v4.17.0')], t)
        .blockedReason,
    ).toBe('No eligible upgrade target is available.');
  });

  it('accepts N-3 and nonadjacent targets, rejecting N-4 and another major', () => {
    const catalog = [
      makeVersion('n-four', '4.20.0'),
      makeVersion('next-major', '5.0.0'),
      makeVersion('n-three', '4.19.0'),
    ];
    const result = resolveClusterUpgradeChoices(currentVersion(), catalog, t);

    expect(result.eligibleTargets.map((version) => version.id)).toEqual(['n-three']);
  });

  it('explains when every newer target exceeds the supported node-set skew', () => {
    const result = resolveClusterUpgradeChoices(
      currentVersion(),
      [makeVersion('n-four', '4.20.0')],
      t,
    );

    expect(result.eligibleTargets).toEqual([]);
    expect(result.blockedReason).toBe(
      'Available targets exceed the supported node set version skew.',
    );
  });

  it('reports no eligible target when the catalog has no usable newer entry', () => {
    const catalog = [
      makeVersion('older', '4.15.0'),
      makeVersion('disabled', '4.18.0', ClusterVersionState.ACTIVE, false),
      makeVersion('obsolete', '4.18.0', ClusterVersionState.OBSOLETE),
    ];
    const result = resolveClusterUpgradeChoices(currentVersion(), catalog, t);

    expect(result.eligibleTargets).toEqual([]);
    expect(result.blockedReason).toBe('No eligible upgrade target is available.');
  });
});
