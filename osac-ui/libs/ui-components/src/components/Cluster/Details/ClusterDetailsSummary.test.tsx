import { create } from '@bufbuild/protobuf';
import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import { type Cluster, ClusterSchema } from '@osac/types';

import ClusterDetailsSummary from './ClusterDetailsSummary';

const renderSummary = (cluster: Cluster) => render(<ClusterDetailsSummary cluster={cluster} />);

describe('ClusterDetailsSummary', () => {
  it('shows worker count from spec node sets', () => {
    const cluster = create(ClusterSchema, {
      id: 'cl-1',
      spec: {
        nodeSets: {
          compute: { baremetalInstanceType: { id: 'acme_1tb' }, size: 3 },
          gpu: { baremetalInstanceType: { id: 'ibm_mi300x' }, size: 2 },
        },
      },
      status: {
        state: 2, // CLUSTER_STATE_READY
      },
    });

    renderSummary(cluster);

    expect(screen.getByText('5')).toBeInTheDocument();
  });

  it('shows API and console URLs from status', () => {
    const cluster = create(ClusterSchema, {
      id: 'cl-1',
      spec: {
        nodeSets: {
          compute: { baremetalInstanceType: { id: 'acme_1tb' }, size: 1 },
        },
      },
      status: {
        state: 2,
        apiUrl: 'https://api.example.com:6443',
        consoleUrl: 'https://console.example.com',
      },
    });

    renderSummary(cluster);

    expect(screen.getByText('https://api.example.com:6443')).toBeInTheDocument();
    expect(screen.getByText('https://console.example.com')).toBeInTheDocument();
    expect(screen.getByText('https://api.example.com:6443').closest('a')).toBeNull();
    expect(screen.getByText('https://console.example.com').closest('a')).toBeNull();
  });

  it('keeps catalog ID and empty value fallbacks without card wrappers', () => {
    const { container } = renderSummary(
      create(ClusterSchema, {
        id: 'empty',
        spec: { catalogItem: { id: 'catalog-id' } },
      }),
    );

    expect(screen.getByText('catalog-id')).toBeInTheDocument();
    expect(screen.getByText('0')).toBeInTheDocument();
    expect(screen.getAllByText('—')).toHaveLength(2);
    expect(container.querySelector('.pf-v6-c-card')).toBeNull();
  });
});
