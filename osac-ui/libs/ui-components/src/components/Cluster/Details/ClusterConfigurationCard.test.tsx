import { clone, create } from '@bufbuild/protobuf';
import { TimestampSchema } from '@bufbuild/protobuf/wkt';
import { Code, ConnectError, createRouterTransport } from '@connectrpc/connect';
import { screen, waitFor, within } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import {
  ClusterNodeSetSchema,
  ClusterSchema,
  ClusterState,
  ClusterUpgradeHistoryEntrySchema,
  ClusterUpgradeState,
  ClusterVersionReferenceSchema,
  ClusterVersionSchema,
  ClusterVersionState,
  ClusterVersions,
} from '@osac/types';

import { ClusterConfigurationCard } from './ClusterConfigurationCard';
import { useGetResource } from '../../../api/use-resource';
import { renderWithProviders } from '../../../test-utils/TestProviders';

const currentVersion = create(ClusterVersionSchema, {
  id: 'version-4-17',
  metadata: { name: 'ocp-4-17', tenant: 'shared', project: 'project-a' },
  spec: { version: '4.17.0', state: ClusterVersionState.ACTIVE, enabled: true },
});
const targetVersion = create(ClusterVersionSchema, {
  id: 'version-4-18',
  metadata: { name: 'ocp-4-18', tenant: 'shared', project: 'project-a' },
  spec: { version: '4.18.0', state: ClusterVersionState.ACTIVE, enabled: true },
});
const cluster = create(ClusterSchema, {
  id: 'cl-1',
  metadata: { creator: 'alice', tenant: 'tenant-a', project: 'project-a' },
  spec: {
    catalogItem: { id: 'catalog-1', name: 'Standard' },
    version: { id: currentVersion.id, name: 'ocp-4-17', project: 'project-a' },
  },
  status: { state: ClusterState.READY, observedVersion: '4.17.0' },
});

const RefreshVersion = () => {
  const { refetch } = useGetResource(ClusterVersions, { id: currentVersion.id });
  return <button onClick={() => void refetch()}>Refresh version</button>;
};

describe('ClusterConfigurationCard', () => {
  it('shows configuration and an inline available upgrade beside the observed version', async () => {
    renderWithProviders(<ClusterConfigurationCard cluster={cluster} />, {
      apiFixtures: { clusterVersions: [currentVersion, targetVersion] },
    });

    expect(screen.getByText('Cluster configuration')).toBeInTheDocument();
    expect(screen.getByText('Standard')).toBeInTheDocument();
    expect(screen.getByText('Creator')).toBeInTheDocument();
    expect(screen.getByText('alice')).toBeInTheDocument();
    expect(screen.getByText('Current version')).toBeInTheDocument();
    await waitFor(() =>
      expect(screen.getByRole('button', { name: 'Upgrade cluster' })).toBeEnabled(),
    );
    const availability = screen.getByText('Upgrade available');
    const versionRow = screen.getByText('4.17.0').closest('dd');
    expect(versionRow).toContainElement(availability);
    expect(availability.closest('.pf-v6-c-label')).toHaveClass('pf-m-orange');
    expect(screen.queryByText('Desired version')).not.toBeInTheDocument();

    const upgradeButton = screen.getByRole('button', { name: 'Upgrade cluster' });
    expect(upgradeButton).toHaveClass('pf-m-link', 'pf-m-inline');
    expect(versionRow).toContainElement(upgradeButton);
    expect(upgradeButton.querySelector('.pf-v6-c-button__icon')).toBeNull();
  });

  it('shows a reason when upgrade eligibility is unavailable', () => {
    renderWithProviders(
      <ClusterConfigurationCard cluster={create(ClusterSchema, { id: 'cl-2' })} />,
    );

    const unavailable = screen.getByText('No upgrade available');
    expect(unavailable.closest('dd')).toHaveTextContent('—');
    expect(unavailable.closest('.pf-v6-c-label')).not.toHaveClass('pf-m-orange');
    const upgradeButton = screen.getByRole('button', { name: 'Upgrade cluster' });
    expect(upgradeButton).toBeDisabled();
    expect(upgradeButton).toHaveAccessibleDescription('Upgrade eligibility is unavailable.');
  });

  it.each([ClusterUpgradeState.PENDING, ClusterUpgradeState.PROGRESSING])(
    'shows an ongoing upgrade from observed to target in state %s',
    (state) => {
      const upgrading = clone(ClusterSchema, cluster);
      if (!upgrading.status) {
        throw new Error('The cluster fixture needs status');
      }
      upgrading.status.upgradeHistory = [
        create(ClusterUpgradeHistoryEntrySchema, {
          component: { target: { case: 'controlPlane', value: {} } },
          sourceVersion: '4.17.0',
          targetVersion: '4.18.0',
          state,
          message: 'Updating the control plane',
        }),
      ];
      renderWithProviders(<ClusterConfigurationCard cluster={upgrading} />);

      const label = screen.getByText(
        state === ClusterUpgradeState.PENDING ? 'Upgrade pending' : 'Upgrade in progress',
      );
      expect(screen.getByText('4.17.0 → 4.18.0').closest('dd')).toContainElement(label);
      expect(label.closest('.pf-v6-c-label')).toHaveClass('pf-m-blue');
      expect(screen.queryByRole('button', { name: 'Upgrade cluster' })).not.toBeInTheDocument();
      expect(screen.getByText('Updating the control plane')).toBeInTheDocument();
    },
  );

  it('uses only the latest upgrade history entry', () => {
    const upgraded = clone(ClusterSchema, cluster);
    if (!upgraded.status) {
      throw new Error('The cluster fixture needs status');
    }
    upgraded.status.upgradeHistory = [
      create(ClusterUpgradeHistoryEntrySchema, {
        component: { target: { case: 'controlPlane', value: {} } },
        targetVersion: '4.18.0',
        state: ClusterUpgradeState.SUCCEEDED,
      }),
      create(ClusterUpgradeHistoryEntrySchema, {
        component: { target: { case: 'controlPlane', value: {} } },
        targetVersion: '4.16.0',
        state: ClusterUpgradeState.PROGRESSING,
        message: 'An older upgrade is still running',
      }),
    ];
    upgraded.status.observedVersion = '4.18.0';
    if (!upgraded.spec) {
      throw new Error('The cluster fixture needs spec');
    }
    upgraded.spec.version = create(ClusterVersionReferenceSchema, {
      id: targetVersion.id,
      name: 'ocp-4-18',
      project: 'project-a',
    });
    renderWithProviders(<ClusterConfigurationCard cluster={upgraded} />, {
      apiFixtures: { clusterVersions: [currentVersion, targetVersion] },
    });

    expect(screen.getByText('4.18.0')).toBeInTheDocument();
    expect(screen.queryByText('Upgrade succeeded')).not.toBeInTheDocument();
    expect(screen.queryByText('An older upgrade is still running')).not.toBeInTheDocument();
    expect(screen.queryByText(/→/)).not.toBeInTheDocument();
  });

  it('shows a concise timeout description without offering another upgrade', async () => {
    const timedOut = clone(ClusterSchema, cluster);
    if (!timedOut.status) {
      throw new Error('The cluster fixture needs status');
    }
    timedOut.status.upgradeHistory = [
      create(ClusterUpgradeHistoryEntrySchema, {
        component: { target: { case: 'controlPlane', value: {} } },
        targetVersion: '4.18.0',
        state: ClusterUpgradeState.TIMED_OUT,
        message: 'Control plane upgrade timed out',
      }),
    ];
    const { user } = renderWithProviders(<ClusterConfigurationCard cluster={timedOut} />);

    const label = screen.getByText('Upgrade timed out');
    expect(label.closest('.pf-v6-c-label')).toHaveClass('pf-m-compact');
    expect(label.closest('.pf-v6-c-label')).not.toHaveClass('pf-m-warning', 'pf-m-danger');
    expect(screen.queryByRole('button', { name: 'Upgrade cluster' })).not.toBeInTheDocument();
    expect(screen.getByText('HyperShift may still complete the upgrade.')).toBeInTheDocument();
    expect(screen.queryByText('Control plane upgrade timed out')).not.toBeInTheDocument();
    expect(screen.queryByText(/→/)).not.toBeInTheDocument();

    await user.click(screen.getByRole('button', { name: 'Upgrade history' }));
    expect(
      within(screen.getByRole('dialog', { name: 'Upgrade history' })).getByText(
        'Control plane upgrade timed out',
      ),
    ).toBeInTheDocument();
  });

  it('offers a different target after a failed upgrade', async () => {
    const failed = clone(ClusterSchema, cluster);
    if (!failed.spec || !failed.status) {
      throw new Error('The cluster fixture needs spec and status');
    }
    failed.spec.version = create(ClusterVersionReferenceSchema, { id: targetVersion.id });
    failed.status.upgradeHistory = [
      create(ClusterUpgradeHistoryEntrySchema, {
        component: { target: { case: 'controlPlane', value: {} } },
        sourceVersion: '4.17.0',
        targetVersion: '4.18.0',
        state: ClusterUpgradeState.FAILED,
        message: 'The attempted upgrade failed',
      }),
      create(ClusterUpgradeHistoryEntrySchema, {
        component: { target: { case: 'nodeSetId', value: 'workers' } },
        targetVersion: '4.19.0',
        state: ClusterUpgradeState.FAILED,
      }),
      create(ClusterUpgradeHistoryEntrySchema, {
        component: { target: { case: 'controlPlane', value: {} } },
        sourceVersion: '4.17.0',
        targetVersion: '4.17.1',
        state: ClusterUpgradeState.FAILED,
      }),
    ];
    const previouslyFailedVersion = create(ClusterVersionSchema, {
      id: 'version-4-17-1',
      metadata: { name: 'ocp-4-17-1', tenant: 'shared', project: 'project-a' },
      spec: { version: '4.17.1', state: ClusterVersionState.ACTIVE, enabled: true },
    });
    const alternateVersion = create(ClusterVersionSchema, {
      id: 'version-4-19',
      metadata: { name: 'ocp-4-19', tenant: 'shared', project: 'project-a' },
      spec: { version: '4.19.0', state: ClusterVersionState.ACTIVE, enabled: true },
    });
    const { user } = renderWithProviders(<ClusterConfigurationCard cluster={failed} />, {
      apiFixtures: {
        clusterVersions: [currentVersion, targetVersion, previouslyFailedVersion, alternateVersion],
      },
    });

    await waitFor(() =>
      expect(screen.getByRole('button', { name: 'Upgrade cluster' })).toBeEnabled(),
    );
    expect(screen.getByText('Upgrade failed')).toBeInTheDocument();
    expect(screen.getByText('The attempted upgrade failed')).toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: 'Upgrade cluster' }));
    await user.click(screen.getByLabelText(/^Target version/));
    expect(screen.getByRole('option', { name: '4.19.0' })).toBeInTheDocument();
    expect(screen.queryByRole('option', { name: '4.17.1' })).not.toBeInTheDocument();
    expect(screen.queryByRole('option', { name: '4.18.0' })).not.toBeInTheDocument();
  });

  it('shows control-plane upgrade history with current state, times, and messages', async () => {
    const upgrading = clone(ClusterSchema, cluster);
    if (!upgrading.status) {
      throw new Error('The cluster fixture needs status');
    }
    upgrading.status.upgradeHistory = [
      create(ClusterUpgradeHistoryEntrySchema, {
        component: { target: { case: 'nodeSetId', value: 'workers' } },
        state: ClusterUpgradeState.FAILED,
        message: 'Workers failed',
      }),
      create(ClusterUpgradeHistoryEntrySchema, {
        component: { target: { case: 'controlPlane', value: {} } },
        state: ClusterUpgradeState.PROGRESSING,
        sourceVersion: '4.17.0',
        targetVersion: '4.18.0',
        acceptedTime: create(TimestampSchema, { seconds: 1_700_000_000n }),
        message: 'Updating the control plane',
      }),
      create(ClusterUpgradeHistoryEntrySchema, {
        component: { target: { case: 'controlPlane', value: {} } },
        state: ClusterUpgradeState.SUCCEEDED,
        sourceVersion: '4.16.0',
        targetVersion: '4.17.0',
        acceptedTime: create(TimestampSchema, { seconds: 1_600_000_000n }),
        stateTransitionTime: create(TimestampSchema, { seconds: 1_600_001_000n }),
      }),
    ];
    const { user } = renderWithProviders(<ClusterConfigurationCard cluster={upgrading} />);

    const historyButton = screen.getByRole('button', { name: 'Upgrade history' });
    expect(screen.getByText('Current version').closest('dt')).toContainElement(historyButton);
    expect(screen.getByText('4.17.0 → 4.18.0').closest('dd')).not.toContainElement(historyButton);
    await user.click(historyButton);
    const history = screen.getByRole('dialog', { name: 'Upgrade history' });
    expect(within(history).getAllByRole('row')).toHaveLength(3);
    expect(within(history).getByText('4.17.0 → 4.18.0')).toBeInTheDocument();
    expect(within(history).getByText('4.16.0 → 4.17.0')).toBeInTheDocument();
    const succeededStatus = within(history).getByText('Succeeded');
    expect(succeededStatus).toBeInTheDocument();
    expect(succeededStatus.closest('.pf-v6-c-label')).toBeNull();
    expect(succeededStatus.querySelector('.pf-v6-c-icon')).toBeInTheDocument();
    expect(within(history).getByText('Updating the control plane')).toBeInTheDocument();
    expect(within(history).queryByText('Workers failed')).not.toBeInTheDocument();
    expect(within(history).getAllByRole('time')).toHaveLength(3);
    await user.click(within(history).getByRole('button', { name: 'Close' }));
    expect(screen.queryByRole('dialog', { name: 'Upgrade history' })).not.toBeInTheDocument();
  });

  it('keeps the history link visible when cluster actions are hidden', () => {
    const failed = clone(ClusterSchema, cluster);
    if (!failed.status) {
      throw new Error('The cluster fixture needs status');
    }
    failed.status.state = ClusterState.FAILED;
    failed.status.upgradeHistory = [
      create(ClusterUpgradeHistoryEntrySchema, {
        component: { target: { case: 'controlPlane', value: {} } },
        state: ClusterUpgradeState.FAILED,
      }),
    ];
    renderWithProviders(<ClusterConfigurationCard cluster={failed} />);

    expect(screen.getByText('Current version').closest('dt')).toContainElement(
      screen.getByRole('button', { name: 'Upgrade history' }),
    );
    expect(screen.getByText('Upgrade failed')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Upgrade cluster' })).not.toBeInTheDocument();
  });

  it('uses the observed worker count for the downtime warning', async () => {
    const observedSingleWorker = clone(ClusterSchema, cluster);
    if (!observedSingleWorker.spec || !observedSingleWorker.status) {
      throw new Error('The cluster fixture needs spec and status');
    }
    observedSingleWorker.spec.nodeSets.workers = create(ClusterNodeSetSchema, { size: 2 });
    observedSingleWorker.status.nodeSets.workers = create(ClusterNodeSetSchema, {
      size: 1,
      observedVersion: '4.17.0',
    });
    const { user } = renderWithProviders(
      <ClusterConfigurationCard cluster={observedSingleWorker} />,
      { apiFixtures: { clusterVersions: [currentVersion, targetVersion] } },
    );

    await waitFor(() =>
      expect(screen.getByRole('button', { name: 'Upgrade cluster' })).toBeEnabled(),
    );
    await user.click(screen.getByRole('button', { name: 'Upgrade cluster' }));
    const dialog = screen.getByRole('dialog', { name: 'Upgrade cluster' });
    expect(
      within(dialog).getByRole('checkbox', { name: 'I understand this may cause downtime' }),
    ).toBeRequired();
  });

  it.each([ClusterState.FAILED, ClusterState.DELETING, ClusterState.DELETE_FAILED])(
    'hides upgrade controls for cluster state %s',
    (state) => {
      const unavailable = clone(ClusterSchema, cluster);
      if (!unavailable.status) {
        throw new Error('The cluster fixture needs status');
      }
      unavailable.status.state = state;
      renderWithProviders(<ClusterConfigurationCard cluster={unavailable} />);

      expect(screen.getByText('4.17.0')).toBeInTheDocument();
      expect(screen.queryByRole('button', { name: 'Upgrade cluster' })).not.toBeInTheDocument();
      expect(screen.queryByText('No upgrade available')).not.toBeInTheDocument();
    },
  );

  it('blocks an upgrade when a version refetch fails after a successful fetch', async () => {
    let getCalls = 0;
    const transport = createRouterTransport((router) => {
      router.service(ClusterVersions, {
        get: () => {
          getCalls += 1;
          if (getCalls > 1) {
            throw new ConnectError('version removed', Code.NotFound);
          }
          return { object: currentVersion };
        },
        list: () => ({ items: [currentVersion, targetVersion], size: 2, total: 2 }),
      });
    });
    const { user } = renderWithProviders(
      <>
        <ClusterConfigurationCard cluster={cluster} />
        <RefreshVersion />
      </>,
      { transport },
    );

    await waitFor(() =>
      expect(screen.getByRole('button', { name: 'Upgrade cluster' })).toBeEnabled(),
    );
    await user.click(screen.getByRole('button', { name: 'Refresh version' }));
    await waitFor(() =>
      expect(screen.getByRole('button', { name: 'Upgrade cluster' })).toHaveAccessibleDescription(
        'Could not load upgrade options.',
      ),
    );
    expect(screen.getByRole('button', { name: 'Upgrade cluster' })).toBeDisabled();
  });

  it('closes an open upgrade dialog when the cluster identity changes', async () => {
    const { user, rerender } = renderWithProviders(<ClusterConfigurationCard cluster={cluster} />, {
      apiFixtures: { clusterVersions: [currentVersion, targetVersion] },
    });

    await waitFor(() =>
      expect(screen.getByRole('button', { name: 'Upgrade cluster' })).toBeEnabled(),
    );
    await user.click(screen.getByRole('button', { name: 'Upgrade cluster' }));
    expect(screen.getByRole('dialog', { name: 'Upgrade cluster' })).toBeInTheDocument();

    const anotherCluster = clone(ClusterSchema, cluster);
    anotherCluster.id = 'cl-2';
    rerender(<ClusterConfigurationCard cluster={anotherCluster} />);

    expect(screen.queryByRole('dialog', { name: 'Upgrade cluster' })).not.toBeInTheDocument();
  });
});
