import { create } from '@bufbuild/protobuf';
import { screen, within } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import { ClusterSchema } from '@osac/types';

import ClusterNodeSetsSection from './ClusterNodeSetsSection';
import { renderWithProviders } from '../../../test-utils/TestProviders';

describe('ClusterNodeSetsSection', () => {
  it('preserves labeled host name fallbacks and zero sizes in accessible list rows', () => {
    renderWithProviders(
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

    const list = screen.getByRole('list', { name: 'Cluster node sets' });
    const row = within(list).getByRole('listitem');
    expect(
      within(row)
        .getAllByRole('term')
        .map((term) => term.textContent),
    ).toEqual(['Name', 'Host type', 'Size']);
    expect(
      within(row)
        .getAllByRole('definition')
        .map((value) => value.textContent),
    ).toEqual(['workers', '—', '0']);
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
