import { create } from '@bufbuild/protobuf';
import { render, screen, within } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import { ClusterSchema } from '@osac/types';

import ClusterNodeSetsSection from './ClusterNodeSetsSection';

describe('ClusterNodeSetsSection', () => {
  it('preserves bare-metal host name fallbacks and zero sizes in a full-width compact table', () => {
    render(
      <ClusterNodeSetsSection
        cluster={create(ClusterSchema, {
          id: 'cluster-1',
          spec: {
            nodeSets: {
              workers: { size: 0, baremetalInstanceType: { id: 'host-id', name: ' ' } },
            },
          },
        })}
      />,
    );

    const table = screen.getByRole('grid', { name: 'Cluster node sets' });
    expect(table).toHaveClass('pf-m-compact');
    expect(table).toHaveClass('cluster-details__node-sets');
    expect(within(table).getByRole('row', { name: 'workers — 0' })).toBeInTheDocument();
    expect(screen.queryByText('host-id')).not.toBeInTheDocument();
    expect(table.closest('.pf-v6-c-card')).toBeNull();
    expect(table.closest('.pf-v6-l-grid__item')).toBeNull();
  });
});
