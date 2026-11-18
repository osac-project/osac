import { Route, Routes } from 'react-router-dom';
import { clone, create } from '@bufbuild/protobuf';
import { TimestampSchema } from '@bufbuild/protobuf/wkt';
import { Code, ConnectError } from '@connectrpc/connect';
import { screen, waitFor, within } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import {
  type Cluster,
  ClusterSchema,
  ClusterState,
  ClusterUpgradeHistoryEntrySchema,
  ClusterUpgradeState,
  type ClusterVersion,
  ClusterVersionReferenceSchema,
  ClusterVersionSchema,
  ClusterVersionState,
  Clusters,
  type ClustersUpdateRequest,
} from '@osac/types';

import { ClusterDetailsPage } from './ClusterDetailsPage';
import { useInvalidateServiceQueries } from '../../api/use-resource';
import type { MockTransportOverrides } from '../../test-utils/createMockConnectTransport';
import { renderWithProviders } from '../../test-utils/TestProviders';

const currentVersion = create(ClusterVersionSchema, {
  id: 'current-version',
  metadata: { name: 'ocp-4-17', tenant: 'tenant-a', project: 'project-a' },
  spec: { version: '4.17.0', state: ClusterVersionState.ACTIVE, enabled: true },
});
const targetVersion = create(ClusterVersionSchema, {
  id: 'target-version',
  metadata: { name: 'ocp-4-18', tenant: 'tenant-a', project: 'project-a' },
  spec: { version: '4.18.0', state: ClusterVersionState.ACTIVE, enabled: true },
});
const readyCluster = create(ClusterSchema, {
  id: 'cluster-1',
  metadata: { name: 'cluster-one', tenant: 'tenant-a', project: 'project-a' },
  spec: {
    version: { id: currentVersion.id, name: currentVersion.metadata?.name, project: 'project-a' },
    nodeSets: { workers: { size: 2 } },
  },
  status: {
    state: ClusterState.READY,
    observedVersion: '4.17.0',
    nodeSets: { workers: { size: 2, observedVersion: '4.17.0' } },
  },
});

const RefreshClusterButton = () => {
  const invalidateServiceQueries = useInvalidateServiceQueries();
  return <button onClick={() => void invalidateServiceQueries(Clusters)}>Refresh cluster</button>;
};

const renderPage = (
  cluster: Cluster,
  versions: ClusterVersion[],
  transportOverrides?: MockTransportOverrides,
) =>
  renderWithProviders(
    <>
      <RefreshClusterButton />
      <Routes>
        <Route path="/clusters/:clusterId" element={<ClusterDetailsPage />} />
      </Routes>
    </>,
    {
      routerEntries: ['/clusters/cluster-1'],
      apiFixtures: { clusters: [cluster], clusterVersions: versions, externalIpAttachments: [] },
      transportOverrides,
    },
  );

describe('ClusterDetailsPage upgrade availability', () => {
  it('offers the upgrade after resolving the configured version and catalog targets', async () => {
    renderPage(readyCluster, [currentVersion, targetVersion]);

    await waitFor(() =>
      expect(screen.getByRole('button', { name: 'Upgrade cluster' })).toBeEnabled(),
    );
    expect(screen.getByText('Upgrade available')).toBeInTheDocument();
    expect(screen.getByText('4.17.0')).toBeInTheDocument();
    expect(screen.queryByLabelText('Upgrade progress steps')).not.toBeInTheDocument();
  });

  it.each([ClusterUpgradeState.PENDING, ClusterUpgradeState.PROGRESSING])(
    'shows an ongoing upgrade for history state %s',
    async (state) => {
      const upgrading = clone(ClusterSchema, readyCluster);
      if (!upgrading.spec || !upgrading.status) {
        throw new Error('The cluster fixture needs spec and status');
      }
      upgrading.spec.version = create(ClusterVersionReferenceSchema, { id: targetVersion.id });
      upgrading.status.state = ClusterState.PROGRESSING;
      upgrading.status.upgradeHistory = [
        create(ClusterUpgradeHistoryEntrySchema, {
          component: { target: { case: 'controlPlane', value: {} } },
          state,
          sourceVersion: '4.17.0',
          targetVersion: '4.18.0',
          message: 'Upgrade accepted and running',
        }),
      ];
      renderPage(upgrading, [currentVersion, targetVersion]);

      const label =
        state === ClusterUpgradeState.PENDING ? 'Upgrade pending' : 'Upgrade in progress';
      await waitFor(() => expect(screen.getByText(label)).toBeInTheDocument());
      expect(screen.getByText('4.17.0 → 4.18.0')).toBeInTheDocument();
      expect(screen.queryByRole('button', { name: 'Upgrade cluster' })).not.toBeInTheDocument();
      expect(screen.getByText('Upgrade accepted and running')).toBeInTheDocument();
      expect(screen.queryByLabelText('Upgrade progress steps')).not.toBeInTheDocument();
    },
  );

  it('shows the new observed version without a success status after an upgrade', async () => {
    const upgraded = clone(ClusterSchema, readyCluster);
    if (!upgraded.spec || !upgraded.status) {
      throw new Error('The cluster fixture needs spec and status');
    }
    upgraded.spec.version = create(ClusterVersionReferenceSchema, { id: targetVersion.id });
    upgraded.status.observedVersion = '4.18.0';
    upgraded.status.upgradeHistory = [
      create(ClusterUpgradeHistoryEntrySchema, {
        component: { target: { case: 'controlPlane', value: {} } },
        state: ClusterUpgradeState.SUCCEEDED,
        sourceVersion: '4.17.0',
        targetVersion: '4.18.0',
      }),
    ];
    renderPage(upgraded, [currentVersion, targetVersion]);

    expect(await screen.findByText('4.18.0')).toBeInTheDocument();
    expect(screen.queryByText('Upgrade succeeded')).not.toBeInTheDocument();
    expect(screen.queryByText('Upgrade in progress')).not.toBeInTheDocument();
    expect(screen.queryByLabelText('Upgrade progress steps')).not.toBeInTheDocument();
  });

  it('ignores node-set attempts for progress and history', async () => {
    const upgrading = clone(ClusterSchema, readyCluster);
    if (!upgrading.status) {
      throw new Error('The cluster fixture needs status');
    }
    upgrading.status.upgradeHistory = [
      create(ClusterUpgradeHistoryEntrySchema, {
        component: { target: { case: 'nodeSetId', value: 'workers' } },
        state: ClusterUpgradeState.PROGRESSING,
        sourceVersion: '4.17.0',
        targetVersion: '4.18.0',
        message: 'Workers are upgrading',
      }),
    ];
    renderPage(upgrading, [currentVersion, targetVersion]);

    await waitFor(() => expect(screen.getByText('Upgrade in progress')).toBeInTheDocument());
    expect(screen.queryByRole('button', { name: 'Upgrade cluster' })).not.toBeInTheDocument();
    expect(screen.queryByLabelText('Upgrade progress steps')).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Upgrade history' })).not.toBeInTheDocument();
  });

  it('uses the newest control-plane attempt when a node-set entry comes first', async () => {
    const upgrading = clone(ClusterSchema, readyCluster);
    if (!upgrading.status) {
      throw new Error('The cluster fixture needs status');
    }
    upgrading.status.upgradeHistory = [
      create(ClusterUpgradeHistoryEntrySchema, {
        component: { target: { case: 'nodeSetId', value: 'workers' } },
        state: ClusterUpgradeState.FAILED,
        targetVersion: '4.18.0',
        message: 'Workers failed',
      }),
      create(ClusterUpgradeHistoryEntrySchema, {
        component: { target: { case: 'controlPlane', value: {} } },
        state: ClusterUpgradeState.PROGRESSING,
        sourceVersion: '4.17.0',
        targetVersion: '4.18.0',
        message: 'Control plane is upgrading',
      }),
    ];
    const { user } = renderPage(upgrading, [currentVersion, targetVersion]);

    await waitFor(() => expect(screen.getByText('Upgrade in progress')).toBeInTheDocument());
    expect(screen.getByText('4.17.0 → 4.18.0')).toBeInTheDocument();
    expect(screen.getByText('Control plane is upgrading')).toBeInTheDocument();
    expect(screen.queryByText('Workers failed')).not.toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: 'Upgrade history' }));
    const history = screen.getByRole('dialog', { name: 'Upgrade history' });
    expect(within(history).getAllByRole('row')).toHaveLength(2);
    expect(within(history).getByText('4.17.0 → 4.18.0')).toBeInTheDocument();
    expect(within(history).queryByText('Workers failed')).not.toBeInTheDocument();
  });

  it('shows the latest server state after a timed-out attempt succeeds', async () => {
    let returnedCluster = clone(ClusterSchema, readyCluster);
    if (!returnedCluster.status) {
      throw new Error('The cluster fixture needs status');
    }
    returnedCluster.status.upgradeHistory = [
      create(ClusterUpgradeHistoryEntrySchema, {
        component: { target: { case: 'controlPlane', value: {} } },
        state: ClusterUpgradeState.TIMED_OUT,
        sourceVersion: '4.17.0',
        targetVersion: '4.18.0',
        acceptedTime: create(TimestampSchema, { seconds: 1_700_000_000n }),
        stateTransitionTime: create(TimestampSchema, { seconds: 1_700_000_300n }),
        message: 'Still observing the control plane',
      }),
    ];
    const { user } = renderPage(returnedCluster, [currentVersion, targetVersion], {
      onClusterGet: () => ({ object: returnedCluster }),
    });

    await waitFor(() => expect(screen.getByText('Upgrade timed out')).toBeInTheDocument());
    expect(screen.getByText('HyperShift may still complete the upgrade.')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Upgrade cluster' })).not.toBeInTheDocument();
    const timeoutLabel = screen.getByText('Upgrade timed out').closest('.pf-v6-c-label');
    expect(timeoutLabel).toHaveClass('pf-m-compact');
    expect(timeoutLabel).not.toHaveClass('pf-m-warning', 'pf-m-danger');
    expect(screen.getByText('4.17.0')).toBeInTheDocument();
    expect(screen.queryByLabelText('Upgrade progress steps')).not.toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: 'Upgrade history' }));
    expect(
      within(screen.getByRole('dialog', { name: 'Upgrade history' })).getByText('Timed out'),
    ).toBeInTheDocument();
    expect(
      within(screen.getByRole('dialog', { name: 'Upgrade history' })).getByText(
        'Still observing the control plane',
      ),
    ).toBeInTheDocument();
    expect(
      within(screen.getByRole('dialog', { name: 'Upgrade history' })).getAllByRole('time'),
    ).toHaveLength(1);

    returnedCluster = clone(ClusterSchema, returnedCluster);
    if (!returnedCluster.spec || !returnedCluster.status) {
      throw new Error('The cluster fixture needs spec and status');
    }
    returnedCluster.spec.version = create(ClusterVersionReferenceSchema, { id: targetVersion.id });
    returnedCluster.status.observedVersion = '4.18.0';
    returnedCluster.status.upgradeHistory[0].state = ClusterUpgradeState.SUCCEEDED;
    returnedCluster.status.upgradeHistory[0].stateTransitionTime = create(TimestampSchema, {
      seconds: 1_700_000_600n,
    });
    returnedCluster.status.upgradeHistory[0].message = '';
    await user.click(screen.getByText('Refresh cluster'));

    await waitFor(() =>
      expect(
        within(screen.getByRole('dialog', { name: 'Upgrade history' })).getByText('Succeeded'),
      ).toBeInTheDocument(),
    );
    expect(screen.queryByText('Upgrade timed out')).not.toBeInTheDocument();
    expect(
      screen.queryByText('HyperShift may still complete the upgrade.'),
    ).not.toBeInTheDocument();
    expect(screen.queryByText('Still observing the control plane')).not.toBeInTheDocument();
    expect(screen.getByText('4.18.0')).toBeInTheDocument();
    expect(
      within(screen.getByRole('dialog', { name: 'Upgrade history' })).getAllByRole('time'),
    ).toHaveLength(2);
  });

  it('shows a failed control-plane attempt as an error', async () => {
    const failed = clone(ClusterSchema, readyCluster);
    if (!failed.spec || !failed.status) {
      throw new Error('The cluster fixture needs spec and status');
    }
    failed.spec.version = create(ClusterVersionReferenceSchema, { id: targetVersion.id });
    failed.status.upgradeHistory = [
      create(ClusterUpgradeHistoryEntrySchema, {
        component: { target: { case: 'controlPlane', value: {} } },
        state: ClusterUpgradeState.FAILED,
        sourceVersion: '4.17.0',
        targetVersion: '4.18.0',
        message: 'Control plane could not upgrade',
      }),
    ];
    renderPage(failed, [currentVersion, targetVersion]);

    await waitFor(() => expect(screen.getByText('Upgrade failed')).toBeInTheDocument());
    expect(screen.getByText('Upgrade failed').closest('.pf-v6-c-label')).toHaveClass(
      'pf-m-red',
      'pf-m-compact',
    );
    expect(screen.getByText('4.17.0')).toBeInTheDocument();
    expect(screen.getByText('Control plane could not upgrade')).toBeInTheDocument();
    expect(screen.queryByLabelText('Upgrade progress steps')).not.toBeInTheDocument();
  });

  it('shows a disabled action when the version catalog request fails', async () => {
    renderPage(readyCluster, [currentVersion, targetVersion], {
      onClusterVersionList: () => {
        throw new ConnectError('catalog unavailable', Code.Unavailable);
      },
    });

    await waitFor(() =>
      expect(screen.getByRole('button', { name: 'Upgrade cluster' })).toHaveAccessibleDescription(
        'Could not load upgrade options.',
      ),
    );
  });

  it('resolves by ID and offers server-visible targets across projects', async () => {
    const cluster = clone(ClusterSchema, readyCluster);
    const foreignConfigured = clone(ClusterVersionSchema, currentVersion);
    const foreignTarget = clone(ClusterVersionSchema, targetVersion);
    const crossProjectTarget = clone(ClusterVersionSchema, targetVersion);
    const sharedTarget = clone(ClusterVersionSchema, targetVersion);
    if (
      !foreignConfigured.metadata ||
      !foreignTarget.metadata ||
      !foreignTarget.spec ||
      !crossProjectTarget.metadata ||
      !sharedTarget.metadata
    ) {
      throw new Error('The fixtures need metadata');
    }
    foreignConfigured.id = 'foreign-current-version';
    foreignConfigured.metadata.tenant = 'tenant-b';
    foreignTarget.metadata.tenant = 'tenant-b';
    foreignTarget.spec.version = '4.20.0';
    crossProjectTarget.id = 'cross-project-target-version';
    crossProjectTarget.metadata.project = 'project-b';
    sharedTarget.id = 'shared-target-version';
    sharedTarget.metadata.tenant = 'shared';
    sharedTarget.metadata.name = 'ocp-4-19-shared';
    if (!sharedTarget.spec) {
      throw new Error('The shared version fixture needs a spec');
    }
    sharedTarget.spec.version = '4.19.0';
    const versions = [
      currentVersion,
      foreignConfigured,
      foreignTarget,
      crossProjectTarget,
      sharedTarget,
    ];
    let catalogFilter = '';
    const { user } = renderPage(cluster, versions, {
      onClusterVersionList: (request) => {
        catalogFilter = request.filter ?? '';
        return {
          items: versions.filter(
            (version) =>
              version.metadata?.tenant === cluster.metadata?.tenant ||
              version.metadata?.tenant === 'shared',
          ),
        };
      },
    });

    await waitFor(() =>
      expect(screen.getByRole('button', { name: 'Upgrade cluster' })).toBeEnabled(),
    );
    expect(catalogFilter).toBe('');
    await user.click(screen.getByRole('button', { name: 'Upgrade cluster' }));
    const dialog = screen.getByRole('dialog', { name: 'Upgrade cluster' });
    await user.click(within(dialog).getByLabelText(/^Target version/));
    expect(within(dialog).getByRole('option', { name: '4.19.0' })).toBeInTheDocument();
    expect(within(dialog).getByRole('option', { name: '4.18.0' })).toBeInTheDocument();
    expect(within(dialog).queryByRole('option', { name: '4.20.0' })).not.toBeInTheDocument();
  });

  it('uses the configured version returned by ID without a client tenant check', async () => {
    const cluster = clone(ClusterSchema, readyCluster);
    const foreignConfigured = clone(ClusterVersionSchema, currentVersion);
    if (!cluster.spec || !foreignConfigured.metadata) {
      throw new Error('The fixtures need spec and metadata');
    }
    foreignConfigured.id = 'foreign-current-version';
    foreignConfigured.metadata.tenant = 'tenant-b';
    cluster.spec.version = create(ClusterVersionReferenceSchema, {
      id: foreignConfigured.id,
      name: foreignConfigured.metadata.name,
      project: foreignConfigured.metadata.project,
    });
    renderPage(cluster, [foreignConfigured, targetVersion]);

    await waitFor(() =>
      expect(screen.getByRole('button', { name: 'Upgrade cluster' })).toBeEnabled(),
    );
  });

  it('disables an open dialog when the latest status reports an upgrade in progress', async () => {
    let returnedCluster = clone(ClusterSchema, readyCluster);
    let updateCalls = 0;
    const { user } = renderPage(readyCluster, [currentVersion, targetVersion], {
      onClusterGet: () => ({ object: returnedCluster }),
      onClusterUpdate: () => {
        updateCalls += 1;
        return { object: returnedCluster };
      },
    });

    await waitFor(() =>
      expect(screen.getByRole('button', { name: 'Upgrade cluster' })).toBeEnabled(),
    );
    await user.click(screen.getByRole('button', { name: 'Upgrade cluster' }));
    const dialog = screen.getByRole('dialog', { name: 'Upgrade cluster' });
    await user.click(within(dialog).getByLabelText(/^Target version/));
    await user.click(within(dialog).getByRole('option', { name: '4.18.0' }));
    expect(within(dialog).getByRole('button', { name: 'Upgrade cluster' })).toBeEnabled();

    returnedCluster = clone(ClusterSchema, returnedCluster);
    if (!returnedCluster.status) {
      throw new Error('The cluster fixture needs status');
    }
    returnedCluster.status.upgradeHistory = [
      create(ClusterUpgradeHistoryEntrySchema, {
        component: { target: { case: 'controlPlane', value: {} } },
        state: ClusterUpgradeState.PENDING,
        message: 'Waiting for the control plane',
      }),
    ];
    await user.click(screen.getByText('Refresh cluster'));

    await waitFor(() =>
      expect(within(dialog).getByRole('button', { name: 'Upgrade cluster' })).toBeDisabled(),
    );
    expect(updateCalls).toBe(0);
    await user.click(within(dialog).getByRole('button', { name: 'Cancel' }));
    expect(screen.getByText('Upgrade pending')).toBeInTheDocument();
    expect(screen.getByText('Waiting for the control plane')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Upgrade cluster' })).not.toBeInTheDocument();
  });

  it('refreshes the real cluster detail after one accepted upgrade request', async () => {
    let returnedCluster = clone(ClusterSchema, readyCluster);
    let getCalls = 0;
    let updateRequest: ClustersUpdateRequest | undefined;
    const { user } = renderPage(readyCluster, [currentVersion, targetVersion], {
      onClusterGet: () => {
        getCalls += 1;
        return { object: returnedCluster };
      },
      onClusterUpdate: (request) => {
        updateRequest = request;
        returnedCluster = clone(ClusterSchema, returnedCluster);
        if (!returnedCluster.spec || !returnedCluster.status) {
          throw new Error('The cluster fixture needs spec and status');
        }
        returnedCluster.spec.version = create(ClusterVersionReferenceSchema, {
          id: targetVersion.id,
          name: targetVersion.metadata?.name,
          project: 'project-a',
        });
        returnedCluster.status.state = ClusterState.PROGRESSING;
        returnedCluster.status.upgradeHistory = [
          create(ClusterUpgradeHistoryEntrySchema, {
            component: { target: { case: 'controlPlane', value: {} } },
            state: ClusterUpgradeState.PENDING,
            message: 'Waiting for the control plane',
          }),
        ];
        return { object: returnedCluster };
      },
    });

    await waitFor(() =>
      expect(screen.getByRole('button', { name: 'Upgrade cluster' })).toBeEnabled(),
    );
    await user.click(screen.getByRole('button', { name: 'Upgrade cluster' }));
    const dialog = screen.getByRole('dialog', { name: 'Upgrade cluster' });
    await user.click(within(dialog).getByLabelText(/^Target version/));
    await user.click(within(dialog).getByRole('option', { name: '4.18.0' }));
    await user.click(within(dialog).getByRole('button', { name: 'Upgrade cluster' }));

    await waitFor(() => expect(updateRequest).toBeDefined());
    expect(updateRequest?.object?.spec?.version?.id).toBe(targetVersion.id);
    expect(updateRequest?.updateMask?.paths).toEqual(['spec.version']);
    await waitFor(() => expect(getCalls).toBeGreaterThan(1));
    await waitFor(() => expect(screen.getByText('Upgrade pending')).toBeInTheDocument());
    expect(screen.queryByRole('dialog', { name: 'Upgrade cluster' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Upgrade cluster' })).not.toBeInTheDocument();
  });
});
