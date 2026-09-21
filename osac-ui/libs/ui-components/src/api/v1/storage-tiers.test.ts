import React, { type ReactNode, createElement } from 'react';
import { create } from '@bufbuild/protobuf';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { renderHook, waitFor } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import {
  StorageProtocol,
  StorageTierSchema,
  StorageTierState,
  StorageTiersListResponseSchema,
} from '@osac/types';

import { STORAGE_TIER_ACTIVE_LIST_FILTER, useStorageTier, useStorageTiers } from './storage-tiers';
import { createMockConnectTransport } from '../../test-utils/createMockConnectTransport';
import { ApiProvider } from '../api-context';

const makeStorageTier = (id: string, state: StorageTierState = StorageTierState.ACTIVE) =>
  create(StorageTierSchema, {
    id,
    metadata: { name: `tier-${id}` },
    spec: {
      description: '',
      protocol: StorageProtocol.NFS,
    },
    status: { state },
  });

const makeWrapper = (transport: ReturnType<typeof createMockConnectTransport>) => {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  const wrapper = ({ children }: { children: ReactNode }) =>
    createElement(
      ApiProvider,
      { transport } as React.ComponentProps<typeof ApiProvider>,
      createElement(QueryClientProvider, { client: queryClient }, children),
    );
  return { wrapper };
};

describe('useStorageTiers', () => {
  it('returns tier items from the list response', async () => {
    const transport = createMockConnectTransport({
      publicStorageTiers: [makeStorageTier('t-1'), makeStorageTier('t-2')],
    });
    const { wrapper } = makeWrapper(transport);
    const { result } = renderHook(() => useStorageTiers(), { wrapper });

    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(result.current.data).toHaveLength(2);
    expect(result.current.data?.map((t) => t.id)).toEqual(['t-1', 't-2']);
  });

  it('forwards filter, limit, offset, and order to the list request', async () => {
    let captured: Record<string, unknown> | undefined;
    const transport = createMockConnectTransport(
      {},
      {
        onPublicStorageTierList: (req) => {
          captured = req as unknown as Record<string, unknown>;
          return create(StorageTiersListResponseSchema, { items: [] });
        },
      },
    );
    const { wrapper } = makeWrapper(transport);
    const { result } = renderHook(
      () =>
        useStorageTiers({
          filter: STORAGE_TIER_ACTIVE_LIST_FILTER,
          limit: 10,
          offset: 5,
          order: 'metadata.name',
        }),
      { wrapper },
    );

    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(captured).toMatchObject({
      filter: STORAGE_TIER_ACTIVE_LIST_FILTER,
      limit: 10,
      offset: 5,
      order: 'metadata.name',
    });
  });
});

describe('useStorageTier', () => {
  it('returns a single tier from the get response', async () => {
    const transport = createMockConnectTransport({
      publicStorageTiers: [makeStorageTier('t-1')],
    });
    const { wrapper } = makeWrapper(transport);
    const { result } = renderHook(() => useStorageTier('t-1'), { wrapper });

    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(result.current.data?.id).toBe('t-1');
  });

  it('does not fetch when id is falsy', () => {
    const transport = createMockConnectTransport({
      publicStorageTiers: [makeStorageTier('t-1')],
    });
    const { wrapper } = makeWrapper(transport);
    const { result } = renderHook(() => useStorageTier(''), { wrapper });

    expect(result.current.isFetching).toBe(false);
    expect(result.current.isSuccess).toBe(false);
  });
});
