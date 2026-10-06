import React, { type ReactNode, createElement } from 'react';
import { create } from '@bufbuild/protobuf';
import { Code, ConnectError } from '@connectrpc/connect';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, renderHook, waitFor } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import {
  StorageProtocol,
  StorageTier,
  StorageTierSchema,
  StorageTierState,
  StorageTiersCreateResponseSchema,
  StorageTiersListResponseSchema,
  StorageTiersUpdateResponseSchema,
} from '@osac/types/private';

import {
  invalidateStorageTiersQueries,
  useCreateStorageTier,
  useDeleteStorageTier,
  usePrivateStorageTier,
  usePrivateStorageTiers,
  useUpdateStorageTier,
} from './storage-tiers';
import { createMockConnectTransport } from '../../../test-utils/createMockConnectTransport';
import { ApiProvider } from '../../api-context';
import { cel } from '../../cel';

const makeStorageTier = (id: string, state: StorageTierState = StorageTierState.ACTIVE) =>
  create(StorageTierSchema, {
    id,
    metadata: { name: `tier-${id}` },
    spec: {
      description: '',
      protocol: StorageProtocol.NFS,
      backends: [
        {
          backendId: 'b-1',
          maxReadBandwidthMbs: 100,
          maxWriteBandwidthMbs: 100,
          encryptionEnabled: false,
        },
      ],
    },
    status: { state },
  });

const makeWrapper = (transport: ReturnType<typeof createMockConnectTransport>) => {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  const wrapper = ({ children }: { children: ReactNode }) =>
    createElement(
      ApiProvider,
      { transport } as React.ComponentProps<typeof ApiProvider>,
      createElement(QueryClientProvider, { client: queryClient }, children),
    );
  return { wrapper, queryClient };
};

describe('usePrivateStorageTiers', () => {
  it('returns tier items from the list response', async () => {
    const transport = createMockConnectTransport({
      storageTiers: [makeStorageTier('t-1'), makeStorageTier('t-2')],
    });
    const { wrapper } = makeWrapper(transport);
    const { result } = renderHook(() => usePrivateStorageTiers(), { wrapper });

    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(result.current.data).toHaveLength(2);
    expect(result.current.data?.map((t) => t.id)).toEqual(['t-1', 't-2']);
  });

  it('forwards filter, limit, offset, and order to the list request', async () => {
    let captured: Record<string, unknown> | undefined;
    const transport = createMockConnectTransport(
      {},
      {
        onStorageTierList: (req) => {
          captured = req as unknown as Record<string, unknown>;
          return create(StorageTiersListResponseSchema, { items: [] });
        },
      },
    );
    const { wrapper } = makeWrapper(transport);
    const { result } = renderHook(
      () =>
        usePrivateStorageTiers({
          filter: cel<StorageTier>((filter) =>
            filter.field('status.state').equals(StorageTierState.ACTIVE),
          ),
          limit: 10,
          offset: 5,
          order: 'metadata.name',
        }),
      { wrapper },
    );

    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(captured).toMatchObject({
      filter: 'this.status.state == 1',
      limit: 10,
      offset: 5,
      order: 'metadata.name',
    });
  });
});

describe('usePrivateStorageTier', () => {
  it('returns a single tier from the get response', async () => {
    const transport = createMockConnectTransport({
      storageTiers: [makeStorageTier('t-1')],
    });
    const { wrapper } = makeWrapper(transport);
    const { result } = renderHook(() => usePrivateStorageTier('t-1'), { wrapper });

    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(result.current.data?.id).toBe('t-1');
  });

  it('does not fetch when id is falsy', () => {
    const transport = createMockConnectTransport({
      storageTiers: [makeStorageTier('t-1')],
    });
    const { wrapper } = makeWrapper(transport);
    const { result } = renderHook(() => usePrivateStorageTier(''), { wrapper });

    expect(result.current.isFetching).toBe(false);
    expect(result.current.isSuccess).toBe(false);
  });
});

describe('useCreateStorageTier', () => {
  it('submits metadata.name and the full spec on create', async () => {
    let captured: Record<string, unknown> | undefined;
    const transport = createMockConnectTransport(
      {},
      {
        onStorageTierCreate: (req) => {
          captured = req as unknown as Record<string, unknown>;
          return create(StorageTiersCreateResponseSchema, {
            object: makeStorageTier('new-1'),
          });
        },
      },
    );
    const { wrapper } = makeWrapper(transport);
    const { result } = renderHook(() => useCreateStorageTier(), { wrapper });

    act(() => {
      result.current.mutate({
        metadata: { name: 'tier-1' },
        spec: {
          description: 'fast tier',
          protocol: StorageProtocol.NFS,
          backends: [
            {
              backendId: 'b-1',
              maxReadBandwidthMbs: 100,
              maxWriteBandwidthMbs: 100,
              encryptionEnabled: false,
            },
          ],
        },
      });
    });

    await waitFor(() => expect(result.current.isSuccess || result.current.isError).toBe(true));
    expect(result.current.isSuccess).toBe(true);
    expect(captured?.object).toMatchObject({
      metadata: { name: 'tier-1' },
      spec: {
        description: 'fast tier',
        protocol: StorageProtocol.NFS,
        backends: [{ backendId: 'b-1' }],
      },
    });
  });
});

describe('useUpdateStorageTier', () => {
  const mutateAndCaptureUpdate = async (
    input: Parameters<ReturnType<typeof useUpdateStorageTier>['mutate']>[0],
  ) => {
    let captured: Record<string, unknown> | undefined;
    const transport = createMockConnectTransport(
      { storageTiers: [makeStorageTier(input.id)] },
      {
        onStorageTierUpdate: (req) => {
          captured = req as unknown as Record<string, unknown>;
          return create(StorageTiersUpdateResponseSchema, {
            object: makeStorageTier(input.id),
          });
        },
      },
    );
    const { wrapper } = makeWrapper(transport);
    const { result } = renderHook(() => useUpdateStorageTier(), { wrapper });

    act(() => {
      result.current.mutate(input);
    });

    await waitFor(() => expect(result.current.isSuccess || result.current.isError).toBe(true));
    expect(result.current.isSuccess).toBe(true);
    return captured;
  };

  it('sends a single spec.description mask entry, never masks metadata.name, and locks on the given version', async () => {
    const captured = await mutateAndCaptureUpdate({
      id: 't-1',
      metadata: { version: 3 },
      spec: { description: 'updated description' },
    });

    const paths = (captured?.updateMask as { paths?: string[] } | undefined)?.paths;
    expect(paths).toEqual(['spec.description']);
    expect(paths).not.toContain('metadata.name');
    const object = captured?.object as { metadata?: unknown };
    expect(object.metadata).toMatchObject({ version: 3 });
    expect(captured?.lock).toBe(true);
  });

  it('sends a single spec.backends mask entry with the complete array, never an indexed sub-path', async () => {
    const backends = [
      {
        backendId: 'b-2',
        maxReadBandwidthMbs: 200,
        maxWriteBandwidthMbs: 200,
        encryptionEnabled: true,
      },
    ];
    const captured = await mutateAndCaptureUpdate({
      id: 't-1',
      metadata: { version: 1 },
      spec: { backends },
    });

    const paths = (captured?.updateMask as { paths?: string[] } | undefined)?.paths;
    expect(paths).toEqual(['spec.backends']);
    expect(paths?.some((path) => path.startsWith('spec.backends.'))).toBe(false);
    const object = captured?.object as { spec?: { backends?: unknown } };
    expect(object.spec?.backends).toMatchObject(backends);
  });

  it('sends spec.description and spec.backends as separate mask entries when both change', async () => {
    const captured = await mutateAndCaptureUpdate({
      id: 't-1',
      metadata: { version: 1 },
      spec: {
        description: 'updated description',
        backends: [
          {
            backendId: 'b-1',
            maxReadBandwidthMbs: 100,
            maxWriteBandwidthMbs: 100,
            encryptionEnabled: false,
          },
        ],
      },
    });

    expect((captured?.updateMask as { paths?: string[] } | undefined)?.paths).toEqual([
      'spec.description',
      'spec.backends',
    ]);
  });

  it('surfaces a stale-version conflict as a mutation error', async () => {
    const transport = createMockConnectTransport(
      { storageTiers: [makeStorageTier('t-1')] },
      {
        onStorageTierUpdate: () => {
          throw new ConnectError(
            'Storage tier has been modified since it was read',
            Code.FailedPrecondition,
          );
        },
      },
    );
    const { wrapper } = makeWrapper(transport);
    const { result } = renderHook(() => useUpdateStorageTier(), { wrapper });

    act(() => {
      result.current.mutate({
        id: 't-1',
        metadata: { version: 1 },
        spec: { description: 'updated description' },
      });
    });

    await waitFor(() => expect(result.current.isError).toBe(true));
    expect(result.current.error?.message).toContain('modified since it was read');
  });
});

describe('useDeleteStorageTier', () => {
  it('deletes a tier by id', async () => {
    const transport = createMockConnectTransport({
      storageTiers: [makeStorageTier('t-1')],
    });
    const { wrapper } = makeWrapper(transport);
    const { result } = renderHook(() => useDeleteStorageTier(), { wrapper });

    act(() => {
      result.current.mutate('t-1');
    });

    await waitFor(() => expect(result.current.isSuccess || result.current.isError).toBe(true));
    expect(result.current.isSuccess).toBe(true);
  });
});

describe('invalidateStorageTiersQueries', () => {
  const asApiQueryClient = (qc: QueryClient) =>
    qc as unknown as Parameters<typeof invalidateStorageTiersQueries>[0];

  it('invalidates both the list and by-id storage tier queries', async () => {
    const qc = new QueryClient();
    qc.setQueryData(['v1/private/storage_tiers'], { items: [] });
    qc.setQueryData(['v1/private/storage_tiers', ['t-1']], { id: 't-1' });

    await invalidateStorageTiersQueries(asApiQueryClient(qc));

    expect(qc.getQueryState(['v1/private/storage_tiers'])?.isInvalidated).toBe(true);
    expect(qc.getQueryState(['v1/private/storage_tiers', ['t-1']])?.isInvalidated).toBe(true);
  });
});
