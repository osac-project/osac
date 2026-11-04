import { create } from '@bufbuild/protobuf';
import { describe, expect, it } from 'vitest';

import {
  type Cluster,
  ClusterNodeSetSchema,
  ClusterSchema,
  ClusterState,
  ClusterUpgradeHistoryEntrySchema,
  ClusterUpgradeState,
  type ClusterVersion,
  ClusterVersionReferenceSchema,
  ClusterVersionSchema,
  ClusterVersionState,
} from '@osac/types';

import { resolveClusterUpgradeChoices } from './clusterUpgradeChoices';

type ClusterFixture = Cluster & {
  spec: NonNullable<Cluster['spec']>;
  status: NonNullable<Cluster['status']>;
};
type VersionFixture = ClusterVersion & {
  metadata: NonNullable<ClusterVersion['metadata']>;
  spec: NonNullable<ClusterVersion['spec']>;
};

const makeVersion = (
  id: string,
  version: string,
  state = ClusterVersionState.ACTIVE,
  enabled = true,
): VersionFixture =>
  create(ClusterVersionSchema, {
    id,
    metadata: { name: id, project: 'project-a' },
    spec: { version, state, enabled },
  }) as VersionFixture;

const makeCluster = (): ClusterFixture =>
  create(ClusterSchema, {
    id: 'cluster-1',
    spec: {
      version: { id: 'current', name: 'current', project: 'project-a' },
      nodeSets: { workers: {}, gpu: {} },
    },
    status: {
      state: ClusterState.READY,
      observedVersion: '4.16.0',
      nodeSets: {
        workers: { observedVersion: '4.16.0' },
        gpu: { observedVersion: '4.16.0' },
      },
    },
  }) as ClusterFixture;

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

    const result = resolveClusterUpgradeChoices(makeCluster(), currentVersion(), catalog);

    expect(result.eligibleTargets.map((version) => version.id)).toEqual([
      'minor',
      'minor-rc',
      'patch',
    ]);
    expect(result.blockedReason).toBeUndefined();
  });

  it('accepts a disabled obsolete configured version for convergence', () => {
    const result = resolveClusterUpgradeChoices(makeCluster(), currentVersion(), [nextVersion()]);

    expect(result.eligibleTargets.map((version) => version.id)).toEqual(['next']);
    expect(result.blockedReason).toBeUndefined();
  });

  it('blocks an absent or mismatched configured version', () => {
    const cluster = makeCluster();
    const target = nextVersion();

    expect(resolveClusterUpgradeChoices(cluster, undefined, [target]).blockedReason).toBe(
      'configured-version-unresolved',
    );

    cluster.spec.version = undefined;
    expect(resolveClusterUpgradeChoices(cluster, currentVersion(), [target]).blockedReason).toBe(
      'configured-version-unresolved',
    );

    cluster.spec.version = create(ClusterVersionReferenceSchema, {
      id: 'different-id',
      name: 'current',
      project: 'project-a',
    });
    expect(resolveClusterUpgradeChoices(cluster, currentVersion(), [target]).blockedReason).toBe(
      'configured-version-unresolved',
    );

    cluster.spec.version = create(ClusterVersionReferenceSchema, {
      id: 'current',
      name: 'different-name',
      project: 'project-a',
    });
    expect(resolveClusterUpgradeChoices(cluster, currentVersion(), [target]).blockedReason).toBe(
      'configured-version-unresolved',
    );

    cluster.spec.version = makeCluster().spec.version;
    const wrongProject = currentVersion();
    wrongProject.metadata.project = 'project-b';
    expect(resolveClusterUpgradeChoices(cluster, wrongProject, [target]).blockedReason).toBe(
      'configured-version-unresolved',
    );
  });

  it('distinguishes an invalid configured semver from an unresolved version', () => {
    const cluster = makeCluster();
    const configured = currentVersion();
    configured.spec.version = '4.16';

    expect(resolveClusterUpgradeChoices(cluster, configured, [nextVersion()]).blockedReason).toBe(
      'configured-version-invalid',
    );
  });

  it.each([
    [ClusterState.FAILED, 'cluster-failed'],
    [ClusterState.DELETING, 'cluster-deleting'],
    [ClusterState.DELETE_FAILED, 'cluster-delete-failed'],
  ] as const)('blocks Cluster state %s with %s', (state, reason) => {
    const cluster = makeCluster();
    cluster.status.state = state;

    expect(
      resolveClusterUpgradeChoices(cluster, currentVersion(), [nextVersion()]).blockedReason,
    ).toBe(reason);
  });

  it('distinguishes missing and invalid control-plane observations', () => {
    const cluster = makeCluster();
    cluster.status.observedVersion = '';

    expect(
      resolveClusterUpgradeChoices(cluster, currentVersion(), [nextVersion()]).blockedReason,
    ).toBe('control-plane-observation-unavailable');

    cluster.status.observedVersion = 'not-semver';
    expect(
      resolveClusterUpgradeChoices(cluster, currentVersion(), [nextVersion()]).blockedReason,
    ).toBe('control-plane-observation-unavailable');
  });

  it('rejects noncanonical semver strings in configuration, observations, and targets', () => {
    const cluster = makeCluster();
    const configured = currentVersion();

    configured.spec.version = 'v4.16.0';
    expect(resolveClusterUpgradeChoices(cluster, configured, [nextVersion()]).blockedReason).toBe(
      'configured-version-invalid',
    );

    configured.spec.version = '4.16.0';
    cluster.status.observedVersion = 'v4.16.0';
    expect(resolveClusterUpgradeChoices(cluster, configured, [nextVersion()]).blockedReason).toBe(
      'control-plane-observation-unavailable',
    );

    cluster.status.observedVersion = '4.16.0';
    cluster.status.nodeSets.workers.observedVersion = '4.16.0 ';
    expect(resolveClusterUpgradeChoices(cluster, configured, [nextVersion()])).toMatchObject({
      blockedReason: 'node-set-observation-unavailable',
      blockedNodeSetId: 'workers',
      blockedObservedVersion: '4.16.0 ',
    });

    cluster.status.nodeSets.workers.observedVersion = '4.16.0';
    expect(
      resolveClusterUpgradeChoices(cluster, configured, [makeVersion('prefixed', 'v4.17.0')])
        .blockedReason,
    ).toBe('no-eligible-target');
  });

  it('requires valid observations for configured and extra current node sets', () => {
    const cluster = makeCluster();
    delete cluster.status.nodeSets.gpu;

    expect(resolveClusterUpgradeChoices(cluster, currentVersion(), [nextVersion()])).toMatchObject({
      blockedReason: 'node-set-observation-unavailable',
      blockedNodeSetId: 'gpu',
    });

    cluster.status.nodeSets.gpu = create(ClusterNodeSetSchema, {
      observedVersion: 'invalid',
    });
    expect(resolveClusterUpgradeChoices(cluster, currentVersion(), [nextVersion()])).toMatchObject({
      blockedReason: 'node-set-observation-unavailable',
      blockedNodeSetId: 'gpu',
      blockedObservedVersion: 'invalid',
    });

    cluster.status.nodeSets.gpu.observedVersion = '4.16.0';
    cluster.status.nodeSets.extra = create(ClusterNodeSetSchema, {
      observedVersion: '',
    });
    expect(
      resolveClusterUpgradeChoices(cluster, currentVersion(), [nextVersion()]).blockedReason,
    ).toBe('node-set-observation-unavailable');
  });

  it('compares desired and observed versions semantically across all components', () => {
    const cluster = makeCluster();
    const configured = currentVersion();
    configured.spec.version = '4.16.0+desired';
    cluster.status.observedVersion = '4.16.0+control';
    cluster.status.nodeSets.workers.observedVersion = '4.16.0+workers';

    expect(
      resolveClusterUpgradeChoices(cluster, configured, [nextVersion()]).blockedReason,
    ).toBeUndefined();

    cluster.status.nodeSets.gpu.observedVersion = '4.15.9';
    expect(resolveClusterUpgradeChoices(cluster, configured, [nextVersion()])).toMatchObject({
      blockedReason: 'components-not-converged',
      blockedNodeSetId: 'gpu',
      blockedObservedVersion: '4.15.9',
    });

    cluster.status.nodeSets.gpu.observedVersion = '4.16.0';
    cluster.status.observedVersion = '4.15.9';
    expect(resolveClusterUpgradeChoices(cluster, configured, [nextVersion()])).toMatchObject({
      blockedReason: 'components-not-converged',
      blockedObservedVersion: '4.15.9',
    });
  });

  it('does not block on PROGRESSING state alone', () => {
    const cluster = makeCluster();
    cluster.status.state = ClusterState.PROGRESSING;

    expect(
      resolveClusterUpgradeChoices(cluster, currentVersion(), [nextVersion()]).blockedReason,
    ).toBeUndefined();

    cluster.status.nodeSets.workers.observedVersion = '4.15.9';
    expect(
      resolveClusterUpgradeChoices(cluster, currentVersion(), [nextVersion()]).blockedReason,
    ).toBe('components-not-converged');
  });

  it('allows a later request after TimedOut only once every observation converges', () => {
    const cluster = makeCluster();
    cluster.status.upgradeHistory = [
      create(ClusterUpgradeHistoryEntrySchema, {
        state: ClusterUpgradeState.TIMED_OUT,
        sourceVersion: '4.15.0',
        targetVersion: '4.16.0',
      }),
    ];
    cluster.status.observedVersion = '4.15.0';

    expect(
      resolveClusterUpgradeChoices(cluster, currentVersion(), [nextVersion()]).blockedReason,
    ).toBe('components-not-converged');

    cluster.status.observedVersion = '4.16.0';
    expect(
      resolveClusterUpgradeChoices(cluster, currentVersion(), [nextVersion()]).blockedReason,
    ).toBeUndefined();
  });

  it('accepts N-3 and nonadjacent targets without a graph claim, rejecting N-4 and another major', () => {
    const catalog = [
      makeVersion('n-four', '4.20.0'),
      makeVersion('next-major', '5.0.0'),
      makeVersion('n-three', '4.19.0'),
    ];
    const result = resolveClusterUpgradeChoices(makeCluster(), currentVersion(), catalog);

    expect(result.eligibleTargets.map((version) => version.id)).toEqual(['n-three']);
    expect(result.blockedReason).toBeUndefined();
  });

  it('explains when every newer target exceeds the supported node-set skew', () => {
    const result = resolveClusterUpgradeChoices(makeCluster(), currentVersion(), [
      makeVersion('n-four', '4.20.0'),
    ]);

    expect(result.eligibleTargets).toEqual([]);
    expect(result.blockedReason).toBe('unsupported-skew');
  });

  it('blocks a diverged node set before inspecting catalog targets', () => {
    const cluster = makeCluster();
    cluster.status.nodeSets.workers.observedVersion = '4.17.0';
    const target = makeVersion('below-workers', '4.16.1');
    const result = resolveClusterUpgradeChoices(cluster, currentVersion(), [target]);

    expect(result.eligibleTargets).toEqual([]);
    expect(result.blockedReason).toBe('components-not-converged');
    expect(result.blockedNodeSetId).toBe('workers');
    expect(result.blockedObservedVersion).toBe('4.17.0');
  });

  it('reports no eligible target when the catalog has no usable newer entry', () => {
    const catalog = [
      makeVersion('older', '4.15.0'),
      makeVersion('disabled', '4.18.0', ClusterVersionState.ACTIVE, false),
      makeVersion('obsolete', '4.18.0', ClusterVersionState.OBSOLETE),
    ];
    const result = resolveClusterUpgradeChoices(makeCluster(), currentVersion(), catalog);

    expect(result.eligibleTargets).toEqual([]);
    expect(result.blockedReason).toBe('no-eligible-target');
  });
});
