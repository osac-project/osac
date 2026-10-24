import { create } from '@bufbuild/protobuf';
import { screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import { ClusterSchema, ClusterState, ClusterVersionSchema } from '@osac/types';

import ClusterCard from './ClusterCard';
import { formatClusterCreatedAt } from './clusterDisplay';
import { renderWithProviders } from '../../../test-utils/TestProviders';

const cluster = create(ClusterSchema, {
  id: 'cluster-1',
  metadata: {
    name: 'ocp-cluster-05',
    project: 'Root',
    creationTimestamp: { seconds: 1727798760n, nanos: 0 },
  },
  spec: {
    catalogItem: { name: 'cluster-node-sets-object' },
    version: { name: 'v4.19' },
    nodeSets: {
      worker: { size: 3 },
    },
  },
  status: {
    state: ClusterState.READY,
    consoleUrl: 'https://console.example.com',
    nodeSets: {
      worker: { size: 3 },
    },
  },
});

const clusterVersion = create(ClusterVersionSchema, {
  id: 'v4.19',
  metadata: { name: 'v4.19' },
  spec: { version: '4.19.0' },
});

describe('ClusterCard', () => {
  it('renders cluster details in a card', () => {
    renderWithProviders(<ClusterCard cluster={cluster} clusterVersion={clusterVersion} />);

    expect(screen.getByRole('link', { name: 'ocp-cluster-05' })).toHaveAttribute(
      'href',
      '/clusters/cluster-1',
    );
    expect(screen.getByText('cluster-node-sets-object')).toBeInTheDocument();
    expect(screen.getByText('Ready')).toBeInTheDocument();
    expect(screen.getByText('OpenShift 4.19.0')).toBeInTheDocument();
    expect(screen.getByText('Node sets')).toBeInTheDocument();
    expect(screen.getByText('1 pool · 3 of 3 nodes ready')).toBeInTheDocument();
    expect(screen.getByText('Root')).toBeInTheDocument();
    expect(screen.getByText(formatClusterCreatedAt(cluster.metadata))).toBeInTheDocument();

    const consoleLink = screen.getByRole('link', { name: 'Console' });
    expect(consoleLink).toHaveAttribute('href', 'https://console.example.com');
    expect(consoleLink).toHaveAttribute('target', '_blank');
  });

  it('disables console when cluster is not ready', () => {
    const provisioningCluster = create(ClusterSchema, {
      id: 'cluster-1',
      metadata: { name: 'ocp-cluster-05' },
      spec: cluster.spec,
      status: { state: ClusterState.PROGRESSING },
    });

    renderWithProviders(
      <ClusterCard cluster={provisioningCluster} clusterVersion={clusterVersion} />,
    );

    const consoleAction = screen.getByText('Console').closest('a');
    expect(consoleAction).toHaveAttribute('aria-disabled', 'true');
  });
});
