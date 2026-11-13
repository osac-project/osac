import { create } from '@bufbuild/protobuf';
import { screen, within } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import {
  type Cluster,
  ClusterSchema,
  ClusterState,
  type ClusterUpgradeHistoryEntry,
  ClusterUpgradeHistoryEntrySchema,
  ClusterUpgradeState,
  type ClusterVersion,
  ClusterVersionSchema,
  ClusterVersionState,
} from '@osac/types';

import { ClustersTable } from './ClustersTable';
import { renderWithProviders } from '../../../test-utils/TestProviders';

const makeCluster = (
  id: string,
  versionName: string,
  observedVersion = '',
  upgradeHistory: ClusterUpgradeHistoryEntry[] = [],
): Cluster =>
  create(ClusterSchema, {
    id,
    metadata: { name: id, project: 'Root' },
    status: {
      state: ClusterState.READY,
      observedVersion,
      upgradeHistory,
      nodeSets: { worker: { size: 3 } },
    },
    spec: {
      version: { name: versionName },
      nodeSets: { worker: { size: 3 } },
      network: { podCidr: '10.128.0.0/24', serviceCidr: '10.1.0.0/24' },
    },
  });

const makeClusterVersion = (version: string, state: ClusterVersionState): ClusterVersion =>
  create(ClusterVersionSchema, {
    id: version,
    metadata: { name: version },
    spec: { version, state, enabled: true },
  });

const clusterVersions = [
  makeClusterVersion('4.17.0', ClusterVersionState.ACTIVE),
  makeClusterVersion('4.16.0', ClusterVersionState.DEPRECATED),
  makeClusterVersion('4.15.0', ClusterVersionState.OBSOLETE),
];

const makeAttempt = (
  component: 'controlPlane' | 'nodeSetId',
  state: ClusterUpgradeState,
  targetVersion = '4.18.0',
): ClusterUpgradeHistoryEntry =>
  create(ClusterUpgradeHistoryEntrySchema, {
    component: {
      target:
        component === 'controlPlane'
          ? { case: 'controlPlane', value: {} }
          : { case: 'nodeSetId', value: 'worker' },
    },
    sourceVersion: '4.17.0',
    targetVersion,
    state,
  });

const renderTable = (clusters: Cluster[], versions: ClusterVersion[] = clusterVersions) =>
  renderWithProviders(
    <ClustersTable
      clusters={clusters}
      resolveClusterVersion={(cluster) =>
        versions.find((version) => version.spec?.version === cluster.status?.observedVersion)
      }
    />,
  );

const clusterRow = (name: string) => {
  const row = screen.getByText(name).closest('tr');
  if (!row) {
    throw new Error('Expected a cluster table row');
  }
  return within(row);
};

describe('ClustersTable observed versions', () => {
  it('keeps the original columns and shows observed versions with lifecycle in Configuration', () => {
    renderTable([
      makeCluster('c-active', 'desired-4.18', '4.17.0'),
      makeCluster('c-obsolete', 'desired-4.18', '4.15.0'),
    ]);

    expect(screen.queryByRole('columnheader', { name: 'Current version' })).not.toBeInTheDocument();
    expect(screen.getByRole('columnheader', { name: 'Configuration' })).toBeInTheDocument();
    expect(clusterRow('c-active').getByText('OpenShift 4.17.0')).toBeInTheDocument();
    expect(clusterRow('c-active').getByText('Active')).toBeInTheDocument();
    expect(clusterRow('c-obsolete').getByText('OpenShift 4.15.0')).toBeInTheDocument();
    expect(clusterRow('c-obsolete').getByText('Obsolete')).toBeInTheDocument();
    expect(screen.queryByRole('columnheader', { name: 'Desired version' })).not.toBeInTheDocument();
    expect(screen.getAllByText('Cluster version')).toHaveLength(2);
    expect(screen.getAllByText('Node sets')).toHaveLength(2);
    expect(screen.getAllByText('1 pool · 3 of 3 nodes ready')).toHaveLength(2);
  });

  it('does not infer a current version or lifecycle from the desired version', () => {
    renderTable([makeCluster('c-missing', '4.17.0')]);

    const configurationCell = screen
      .getByText('c-missing')
      .closest('tr')
      ?.querySelector('td[data-label="Configuration"]');
    expect(configurationCell).toHaveTextContent('Cluster version');
    expect(configurationCell).toHaveTextContent('—');
    expect(clusterRow('c-missing').queryByText('Active')).not.toBeInTheDocument();
    expect(clusterRow('c-missing').queryByText('4.17.0')).not.toBeInTheDocument();
  });

  it('hides the lifecycle badge when the observed version has no catalog match', () => {
    renderTable([makeCluster('c-unknown', '4.17.0', '4.19.0')]);

    expect(clusterRow('c-unknown').getByText('OpenShift 4.19.0')).toBeInTheDocument();
    expect(clusterRow('c-unknown').queryByText('Active')).not.toBeInTheDocument();
  });

  it.each([ClusterUpgradeState.PENDING, ClusterUpgradeState.PROGRESSING])(
    'shows observed to target for an active control-plane attempt in state %s',
    (state) => {
      renderTable([
        makeCluster('c-upgrading', 'desired-4.18', '4.17.0', [
          makeAttempt('nodeSetId', ClusterUpgradeState.PROGRESSING),
          makeAttempt('controlPlane', state),
        ]),
      ]);

      expect(clusterRow('c-upgrading').getByText('OpenShift 4.17.0 → 4.18.0')).toBeInTheDocument();
      const status = clusterRow('c-upgrading').getByText('Upgrade in progress');
      expect(status.closest('.pf-v6-c-label')).toHaveClass('pf-m-blue');
      expect(clusterRow('c-upgrading').queryByText('Active')).not.toBeInTheDocument();
    },
  );

  it('does not show a control-plane transition for a node-set-only attempt', () => {
    renderTable([
      makeCluster('c-node-only', 'desired-4.18', '4.17.0', [
        makeAttempt('nodeSetId', ClusterUpgradeState.PROGRESSING),
      ]),
    ]);

    expect(clusterRow('c-node-only').getByText('OpenShift 4.17.0')).toBeInTheDocument();
    expect(clusterRow('c-node-only').queryByText(/→/)).not.toBeInTheDocument();
    expect(clusterRow('c-node-only').queryByText('Upgrade in progress')).not.toBeInTheDocument();
  });

  it.each([
    ClusterUpgradeState.TIMED_OUT,
    ClusterUpgradeState.SUCCEEDED,
    ClusterUpgradeState.FAILED,
  ])('uses the latest control-plane state and hides the transition in state %s', (state) => {
    renderTable(
      [
        makeCluster(
          'c-done',
          'desired-4.18',
          state === ClusterUpgradeState.SUCCEEDED ? '4.18.0' : '4.17.0',
          [
            makeAttempt('controlPlane', state),
            makeAttempt('controlPlane', ClusterUpgradeState.PROGRESSING, '4.16.0'),
          ],
        ),
      ],
      [...clusterVersions, makeClusterVersion('4.18.0', ClusterVersionState.ACTIVE)],
    );

    expect(
      clusterRow('c-done').getByText(
        state === ClusterUpgradeState.SUCCEEDED ? 'OpenShift 4.18.0' : 'OpenShift 4.17.0',
      ),
    ).toBeInTheDocument();
    expect(clusterRow('c-done').queryByText(/→/)).not.toBeInTheDocument();
    expect(clusterRow('c-done').queryByText('Upgrade in progress')).not.toBeInTheDocument();
    expect(clusterRow('c-done').getByText('Active')).toBeInTheDocument();
    expect(clusterRow('c-done').queryByText('Upgrade failed')).not.toBeInTheDocument();
    expect(clusterRow('c-done').queryByText('Upgrade timed out')).not.toBeInTheDocument();
  });
});
