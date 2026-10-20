import type { ReactNode } from 'react';
import { Route, Routes } from 'react-router-dom';
import { create } from '@bufbuild/protobuf';
import { QueryClientProvider } from '@tanstack/react-query';
import { act, render, screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import {
  type Cluster,
  ClusterConditionType,
  ClusterSchema,
  ClusterState,
  Clusters,
} from '@osac/types';
import { ClusterDetailsPage } from '@osac/ui-components/components/Cluster/ClusterDetailsPage';
import { ClustersPage } from '@osac/ui-components/components/Cluster/ClustersPage';
import { SessionProvider } from '@osac/ui-components/hooks/use-session';
import { createMockConnectTransport } from '@osac/ui-components/test-utils/createMockConnectTransport';
import { TestProviders } from '@osac/ui-components/test-utils/TestProviders';

import { createQueryClient } from './queryClient';

const makeCluster = (id: string, reason: string): Cluster =>
  create(ClusterSchema, {
    id,
    metadata: { name: id, project: 'project-a' },
    status: {
      state: ClusterState.PROGRESSING,
      conditions: [{ type: ClusterConditionType.PROGRESSING, reason }],
    },
  });

const createObservedTransport = (clusters: Cluster[]) => {
  const transport = createMockConnectTransport({ clusters });
  const unary = vi.spyOn(transport, 'unary');
  return {
    transport,
    clusterMethods: () =>
      unary.mock.calls
        .map(([method]) => method)
        .filter((method) => method.parent.typeName === Clusters.typeName)
        .map((method) => method.name),
  };
};

const renderClusterView = (
  page: ReactNode,
  transport: ReturnType<typeof createMockConnectTransport>,
  path: string,
) => {
  const queryClient = createQueryClient();
  const view = render(
    <TestProviders transport={transport} routerEntries={[path]}>
      <QueryClientProvider client={queryClient}>
        <SessionProvider role="tenant-user" username="test-user" tenantId="tenant-1">
          {page}
        </SessionProvider>
      </QueryClientProvider>
    </TestProviders>,
  );
  return { ...view, queryClient };
};

describe('production QueryClient cluster refresh', () => {
  it('keeps the production query options', () => {
    const queryClient = createQueryClient();
    expect(queryClient.getDefaultOptions().queries).toMatchObject({
      retry: 1,
      staleTime: 5_000,
      refetchOnMount: true,
      refetchInterval: 10_000,
    });
    queryClient.clear();
  });

  it('replaces list activity after ten seconds with one List and no per-row Get', async () => {
    vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval'] });
    const clusters = [
      makeCluster('cluster-a', 'AllocatingWorkers'),
      makeCluster('cluster-b', 'ImagePulling'),
    ];
    const { transport, clusterMethods } = createObservedTransport(clusters);
    const { unmount, queryClient } = renderClusterView(<ClustersPage />, transport, '/clusters');

    try {
      expect(await screen.findByText('Provisioning - AllocatingWorkers')).toBeInTheDocument();
      expect(screen.getByText('Provisioning - ImagePulling')).toBeInTheDocument();
      expect(clusterMethods()).toEqual(['List']);

      clusters[0] = makeCluster('cluster-a', 'WorkersJoining');
      expect(screen.queryByText('Provisioning - WorkersJoining')).not.toBeInTheDocument();

      await act(async () => vi.advanceTimersByTimeAsync(9_999));
      expect(clusterMethods()).toEqual(['List']);
      expect(screen.queryByText('Provisioning - WorkersJoining')).not.toBeInTheDocument();

      await act(async () => vi.advanceTimersByTimeAsync(1));
      expect(await screen.findByText('Provisioning - WorkersJoining')).toBeInTheDocument();
      expect(screen.queryByText('Provisioning - AllocatingWorkers')).not.toBeInTheDocument();
      expect(screen.getByText('Provisioning - ImagePulling')).toBeInTheDocument();
      expect(clusterMethods()).toEqual(['List', 'List']);
    } finally {
      unmount();
      queryClient.clear();
      vi.useRealTimers();
    }
  });

  it('replaces detail header activity after ten seconds with one Get per response', async () => {
    vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval'] });
    const clusters = [makeCluster('cluster-a', 'AllocatingWorkers')];
    const { transport, clusterMethods } = createObservedTransport(clusters);
    const { unmount, queryClient } = renderClusterView(
      <Routes>
        <Route path="/clusters/:clusterId" element={<ClusterDetailsPage />} />
      </Routes>,
      transport,
      '/clusters/cluster-a',
    );
    const header = () =>
      screen.getByRole('heading', { name: 'cluster-a' }).closest('.pf-v6-l-flex');

    try {
      expect(await screen.findByRole('heading', { name: 'cluster-a' })).toBeInTheDocument();
      expect(header()).toHaveTextContent('AllocatingWorkers');
      expect(clusterMethods()).toEqual(['Get']);

      clusters[0] = makeCluster('cluster-a', 'WorkersJoining');
      expect(header()).not.toHaveTextContent('WorkersJoining');

      await act(async () => vi.advanceTimersByTimeAsync(9_999));
      expect(clusterMethods()).toEqual(['Get']);
      expect(header()).toHaveTextContent('AllocatingWorkers');

      await act(async () => vi.advanceTimersByTimeAsync(1));
      await waitFor(() => expect(header()).toHaveTextContent('WorkersJoining'));
      expect(header()).not.toHaveTextContent('AllocatingWorkers');
      expect(clusterMethods()).toEqual(['Get', 'Get']);
    } finally {
      unmount();
      queryClient.clear();
      vi.useRealTimers();
    }
  });
});
