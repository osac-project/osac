import { create } from '@bufbuild/protobuf';
import { screen, within } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import { ClusterSchema } from '@osac/types';

import ClusterNodeSetsSection from './ClusterNodeSetsSection';
import { renderWithProviders } from '../../../test-utils/TestProviders';

describe('ClusterNodeSetsSection', () => {
  it('shows bold names and subtle summaries using only the matching observed node set version', () => {
    renderWithProviders(
      <ClusterNodeSetsSection
        cluster={create(ClusterSchema, {
          spec: {
            nodeSets: {
              workers: {
                size: 3,
                baremetalInstanceType: { name: 'standard-host' },
                version: { name: 'desired-version', id: 'desired-id' },
              },
              gpu: { size: 2, baremetalInstanceType: { name: 'accelerated-host' } },
            },
          },
          status: {
            observedVersion: 'global-version',
            nodeSets: {
              gpu: { observedVersion: '4.18.1' },
              workers: { observedVersion: '4.17.2' },
            },
          },
        })}
      />,
    );

    const list = screen.getByRole('list', { name: 'Cluster node sets' });
    expect(list).toHaveClass('pf-v6-c-list', 'pf-m-plain');
    expect(list).not.toHaveClass('pf-m-bordered');
    const rows = within(list).getAllByRole('listitem');
    expect(within(rows[0]).getByText('workers').tagName).toBe('STRONG');
    expect(within(rows[1]).getByText('gpu').tagName).toBe('STRONG');
    expect(within(rows[0]).getByText('standard-host-3 Nodes-4.17.2')).toHaveClass(
      'pf-v6-u-text-color-subtle',
      'pf-v6-u-font-size-sm',
    );
    expect(within(rows[1]).getByText('accelerated-host-2 Nodes-4.18.1')).toBeInTheDocument();
    expect(screen.queryByText(/global-version|desired-version|desired-id/)).not.toBeInTheDocument();
    for (const label of ['Name', 'Host type', 'Size']) {
      expect(screen.queryByText(label)).not.toBeInTheDocument();
    }
    expect(screen.queryByRole('term')).not.toBeInTheDocument();
    expect(screen.queryByRole('columnheader')).not.toBeInTheDocument();
  });

  it('preserves host/count fallbacks and zero without borrowing a different node set version', () => {
    renderWithProviders(
      <ClusterNodeSetsSection
        cluster={create(ClusterSchema, {
          id: 'cluster-1',
          spec: {
            nodeSets: {
              workers: { size: 0, baremetalInstanceType: { id: 'host-id', name: ' ' } },
              empty: {},
            },
          },
          status: { nodeSets: { other: { observedVersion: '4.18.0' } } },
        })}
      />,
    );

    const list = screen.getByRole('list', { name: 'Cluster node sets' });
    const rows = within(list).getAllByRole('listitem');
    expect(within(rows[0]).getByText('—-0 Nodes-—')).toBeInTheDocument();
    expect(within(rows[1]).getByText('—-— Nodes-—')).toBeInTheDocument();
    expect(screen.queryByText(/4.18.0/)).not.toBeInTheDocument();
    expect(screen.queryByRole('grid')).not.toBeInTheDocument();
    expect(screen.queryByRole('table')).not.toBeInTheDocument();
    expect(screen.queryByText('host-id')).not.toBeInTheDocument();
    expect(list.closest('.pf-v6-c-card')).toBeNull();
  });

  it('keeps the empty fallback without an empty list', () => {
    renderWithProviders(<ClusterNodeSetsSection cluster={create(ClusterSchema)} />);
    expect(screen.getByText('No node sets configured.')).toBeInTheDocument();
    expect(screen.queryByRole('list', { name: 'Cluster node sets' })).not.toBeInTheDocument();
  });
});
