import type { ReactNode } from 'react';
import { create } from '@bufbuild/protobuf';
import { type Transport, createRouterTransport } from '@connectrpc/connect';
import { act, renderHook, waitFor } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import { type Cluster, ClusterSchema, Clusters } from '@osac/types';

import { useCluster, useClusters, useDeleteCluster, useProvisionCluster } from './cluster';
import { useSession } from '../../hooks/use-session';
import { createMockConnectTransport } from '../../test-utils/createMockConnectTransport';
import { TestProviders } from '../../test-utils/TestProviders';
import { useUpdateResource } from '../use-resource';

const makeCluster = (id: string, name = id): Cluster =>
  create(ClusterSchema, {
    id,
    metadata: { name, project: 'project-a' },
  });

const makeWrapper = (transport: Transport, routerEntries = ['/?project=']) => {
  const wrapper = ({ children }: { children: ReactNode }) => (
    <TestProviders
      transport={transport}
      routerEntries={routerEntries}
      session={{ username: 'cluster-hook-test' }}
    >
      {children}
    </TestProviders>
  );
  return wrapper;
};

describe('Cluster resource hooks', () => {
  it('returns unwrapped list items and preserves the selected project filter', async () => {
    const cluster = makeCluster('cluster-1');
    let capturedFilter: string | undefined;
    const transport = createRouterTransport((router) => {
      router.service(Clusters, {
        list: (request) => {
          capturedFilter = request.filter;
          return { items: [cluster], size: 1, total: 1 };
        },
      });
    });
    const { result } = renderHook(() => useClusters(), {
      wrapper: makeWrapper(transport, ['/?project=project-a']),
    });

    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(result.current.data?.map((item) => item.id)).toEqual(['cluster-1']);
    expect(capturedFilter).toContain('this.metadata.project in ["project-a"]');
  });

  it('keeps previous list data while a new project filter loads', async () => {
    const first = makeCluster('cluster-1');
    const second = makeCluster('cluster-2');
    let releaseSecond: (() => void) | undefined;
    const secondGate = new Promise<void>((resolve) => {
      releaseSecond = resolve;
    });
    const transport = createRouterTransport((router) => {
      router.service(Clusters, {
        list: async (request) => {
          if (request.filter?.includes('project-b')) {
            await secondGate;
            return { items: [second], size: 1, total: 1 };
          }
          return { items: [first], size: 1, total: 1 };
        },
      });
    });
    const { result } = renderHook(() => ({ clusters: useClusters(), session: useSession() }), {
      wrapper: makeWrapper(transport, ['/?project=project-a']),
    });

    await waitFor(() => expect(result.current.clusters.data?.[0]?.id).toBe('cluster-1'));
    act(() => result.current.session.setProjects(['project-b']));

    await waitFor(() => expect(result.current.clusters.isPlaceholderData).toBe(true));
    expect(result.current.clusters.data?.[0]?.id).toBe('cluster-1');

    act(() => releaseSecond?.());
    await waitFor(() => expect(result.current.clusters.data?.[0]?.id).toBe('cluster-2'));
  });

  it('trims Get IDs and leaves a blank ID idle', async () => {
    const transport = createMockConnectTransport({ clusters: [makeCluster('cluster-1')] });
    const { result } = renderHook(
      () => ({
        found: useCluster('  cluster-1  '),
        blank: useCluster('   '),
      }),
      { wrapper: makeWrapper(transport) },
    );

    await waitFor(() => expect(result.current.found.isSuccess).toBe(true));
    expect(result.current.found.data?.id).toBe('cluster-1');
    expect(result.current.blank.fetchStatus).toBe('idle');
    expect(result.current.blank.data).toBeUndefined();
  });

  it('refreshes migrated list data after a generic Cluster update', async () => {
    let clusters = [makeCluster('cluster-1', 'before')];
    const transport = createRouterTransport((router) => {
      router.service(Clusters, {
        list: () => ({ items: clusters, size: clusters.length, total: clusters.length }),
        update: () => {
          clusters = [makeCluster('cluster-1', 'after')];
          return { object: clusters[0] };
        },
      });
    });
    const { result } = renderHook(
      () => ({ clusters: useClusters(), update: useUpdateResource(Clusters) }),
      { wrapper: makeWrapper(transport) },
    );

    await waitFor(() => expect(result.current.clusters.data?.[0]?.metadata?.name).toBe('before'));
    await act(async () => {
      await result.current.update.mutateAsync({ object: makeCluster('cluster-1', 'after') });
    });
    await waitFor(() => expect(result.current.clusters.data?.[0]?.metadata?.name).toBe('after'));
  });

  it('refreshes migrated list data after existing create and delete mutations', async () => {
    let clusters = [makeCluster('cluster-1')];
    const transport = createRouterTransport((router) => {
      router.service(Clusters, {
        list: () => ({ items: clusters, size: clusters.length, total: clusters.length }),
        create: () => {
          const added = makeCluster('cluster-2');
          clusters = [...clusters, added];
          return { object: added };
        },
        delete: (request) => {
          clusters = clusters.filter((cluster) => cluster.id !== request.id);
          return {};
        },
      });
    });
    const { result } = renderHook(
      () => ({
        clusters: useClusters(),
        provision: useProvisionCluster(),
        remove: useDeleteCluster(),
      }),
      { wrapper: makeWrapper(transport) },
    );

    await waitFor(() =>
      expect(result.current.clusters.data?.map((item) => item.id)).toEqual(['cluster-1']),
    );
    await act(async () => {
      await result.current.provision.mutateAsync(makeCluster('cluster-2'));
    });
    await waitFor(() =>
      expect(result.current.clusters.data?.map((item) => item.id)).toEqual([
        'cluster-1',
        'cluster-2',
      ]),
    );

    await act(async () => {
      await result.current.remove.mutateAsync('cluster-1');
    });
    await waitFor(() =>
      expect(result.current.clusters.data?.map((item) => item.id)).toEqual(['cluster-2']),
    );
  });
});
