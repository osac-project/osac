import { Route, Routes } from 'react-router-dom';
import { clone, create } from '@bufbuild/protobuf';
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
  it('offers the upgrade after resolving the configured version and observed components', async () => {
    renderPage(readyCluster, [currentVersion, targetVersion]);

    await waitFor(() =>
      expect(screen.getByRole('button', { name: 'Upgrade cluster' })).toBeEnabled(),
    );
    expect(screen.getByText('Upgrade available')).toBeInTheDocument();
    expect(screen.getByText('4.17.0')).toBeInTheDocument();
  });

  it('explains why a provisioning cluster cannot upgrade yet', async () => {
    const provisioning = clone(ClusterSchema, readyCluster);
    if (!provisioning.status) {
      throw new Error('The cluster fixture needs status');
    }
    provisioning.status.state = ClusterState.PROGRESSING;
    provisioning.status.observedVersion = '';
    provisioning.status.nodeSets.workers.observedVersion = '';
    renderPage(provisioning, [currentVersion, targetVersion]);

    await waitFor(() =>
      expect(screen.getByRole('button', { name: 'Upgrade cluster' })).toHaveAccessibleDescription(
        'The current control plane version is not available yet.',
      ),
    );
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
          state,
          sourceVersion: '4.17.0',
          targetVersion: '4.18.0',
        }),
      ];
      renderPage(upgrading, [currentVersion, targetVersion]);

      await waitFor(() => expect(screen.getByText('Upgrade in progress')).toBeInTheDocument());
      expect(screen.getByRole('button', { name: 'Upgrade cluster' })).toBeDisabled();
    },
  );

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

  it('resolves by ID and offers tenant targets across projects and shared targets', async () => {
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
    expect(catalogFilter).toContain('this.metadata.tenant == "tenant-a"');
    expect(catalogFilter).toContain('this.metadata.tenant == "shared"');
    await user.click(screen.getByRole('button', { name: 'Upgrade cluster' }));
    const dialog = screen.getByRole('dialog', { name: 'Upgrade cluster' });
    await user.click(within(dialog).getByLabelText(/^Target version/));
    expect(within(dialog).getByRole('option', { name: '4.19.0' })).toBeInTheDocument();
    expect(within(dialog).getByRole('option', { name: '4.18.0' })).toBeInTheDocument();
    expect(within(dialog).queryByRole('option', { name: '4.20.0' })).not.toBeInTheDocument();
  });

  it('does not use a configured version from another tenant', async () => {
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
      expect(screen.getByRole('button', { name: 'Upgrade cluster' })).toHaveAccessibleDescription(
        'The configured cluster version could not be found.',
      ),
    );
  });

  it('disables an open dialog when a refreshed cluster is no longer converged', async () => {
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
    returnedCluster.status.observedVersion = '4.16.0';
    await user.click(screen.getByText('Refresh cluster'));

    await waitFor(() =>
      expect(within(dialog).getByRole('button', { name: 'Upgrade cluster' })).toBeDisabled(),
    );
    expect(updateCalls).toBe(0);
    await user.click(within(dialog).getByRole('button', { name: 'Cancel' }));
    expect(screen.getByRole('button', { name: 'Upgrade cluster' })).toHaveAccessibleDescription(
      'The control plane is at 4.16.0; wait for 4.17.0.',
    );
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
    await waitFor(() =>
      expect(screen.getByRole('button', { name: 'Upgrade cluster' })).toHaveAccessibleDescription(
        'The control plane is at 4.17.0; wait for 4.18.0.',
      ),
    );
    expect(screen.queryByRole('dialog', { name: 'Upgrade cluster' })).not.toBeInTheDocument();
  });
});
