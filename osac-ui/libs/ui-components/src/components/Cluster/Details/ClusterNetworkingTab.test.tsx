import { clone, create } from '@bufbuild/protobuf';
import { screen, within } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import { ClusterSchema, ClusterUpgradeHistoryEntrySchema, ClusterUpgradeState } from '@osac/types';

import ClusterNodeSetsTab from './ClusterNetworkingTab';
import { renderWithProviders } from '../../../test-utils/TestProviders';

const cluster = create(ClusterSchema, {
  id: 'cluster-1',
  spec: {
    version: { name: 'desired-4.18' },
    nodeSets: {
      workerA: { size: 2, baremetalInstanceType: { name: 'host-a' } },
      workerB: { size: 3, baremetalInstanceType: { name: 'host-b' } },
      workerC: { size: 1, baremetalInstanceType: { name: 'host-c' } },
    },
  },
  status: {
    nodeSets: {
      workerA: { observedVersion: '4.17.0' },
      workerB: { observedVersion: '4.16.0' },
    },
  },
});

const nodeRow = (name: string) => {
  const row = screen.getByText(name).closest('tr');
  if (!row) {
    throw new Error('Expected a node-set row');
  }
  return within(row);
};

describe('ClusterNodeSetsTab', () => {
  it('shows each node set observed version and a dash for missing observations', () => {
    renderWithProviders(<ClusterNodeSetsTab cluster={cluster} />);

    expect(screen.getByRole('columnheader', { name: 'Current version' })).toBeInTheDocument();
    expect(nodeRow('workerA').getByText('4.17.0')).toBeInTheDocument();
    expect(nodeRow('workerB').getByText('4.16.0')).toBeInTheDocument();
    expect(nodeRow('workerC').getByText('—')).toBeInTheDocument();
    expect(screen.queryByText('desired-4.18')).not.toBeInTheDocument();
    expect(screen.queryByRole('columnheader', { name: 'Desired version' })).not.toBeInTheDocument();
  });

  it('refreshes observed versions after timeout without showing an Updating label', () => {
    const timedOut = clone(ClusterSchema, cluster);
    if (!timedOut.status) {
      throw new Error('Expected cluster status');
    }
    timedOut.status.upgradeHistory = [
      create(ClusterUpgradeHistoryEntrySchema, { state: ClusterUpgradeState.TIMED_OUT }),
    ];
    const { rerender } = renderWithProviders(<ClusterNodeSetsTab cluster={timedOut} />);

    expect(nodeRow('workerA').getByText('4.17.0')).toBeInTheDocument();
    const converged = clone(ClusterSchema, timedOut);
    if (!converged.status) {
      throw new Error('Expected cluster status');
    }
    converged.status.nodeSets.workerA.observedVersion = '4.18.0';
    rerender(<ClusterNodeSetsTab cluster={converged} />);

    expect(nodeRow('workerA').getByText('4.18.0')).toBeInTheDocument();
    expect(nodeRow('workerA').queryByText('Updating')).not.toBeInTheDocument();
  });

  it('keeps the empty state when there are no node sets', () => {
    renderWithProviders(<ClusterNodeSetsTab cluster={create(ClusterSchema, { id: 'empty' })} />);

    expect(screen.getByText('No node sets configured.')).toBeInTheDocument();
    expect(screen.queryByRole('table', { name: 'Cluster node sets' })).not.toBeInTheDocument();
  });
});
