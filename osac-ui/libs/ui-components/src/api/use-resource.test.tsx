import React, { type ReactNode, createElement } from 'react';
import { create } from '@bufbuild/protobuf';
import { type Transport, createRouterTransport } from '@connectrpc/connect';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, renderHook, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import { IdentityProviderSchema, IdentityProviders } from '@osac/types';
import { TenantSchema, Tenants } from '@osac/types/private';

import { ApiProvider } from './api-context';
import {
  useCreateResource,
  useDeleteResource,
  useGetResource,
  useListAllResources,
  useListResource,
  useUpdateResource,
} from './use-resource';
import { createMockConnectTransport } from '../test-utils/createMockConnectTransport';

const makeTenant = (id: string) =>
  create(TenantSchema, {
    id,
    metadata: { name: `tenant-${id}` },
    spec: { domains: [`${id}.example.com`] },
  });

const makeIdentityProvider = (id: string) =>
  create(IdentityProviderSchema, {
    id,
    spec: { title: `Identity provider ${id}`, enabled: true },
  });

const makeWrapper = (transport: Transport) => {
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

describe('resource queries', () => {
  it('lists resources with an inferred response type', async () => {
    const transport = createMockConnectTransport({
      tenants: [makeTenant('tenant-1'), makeTenant('tenant-2')],
    });
    const { wrapper } = makeWrapper(transport);
    const { result } = renderHook(() => useListResource(Tenants), { wrapper });

    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(result.current.data?.items.map((tenant) => tenant.id)).toEqual(['tenant-1', 'tenant-2']);
  });

  it('fetches every page only when requested and keeps normal list queries separate', async () => {
    const tenants = Array.from({ length: 101 }, (_, index) => makeTenant(String(index)));
    const requests: { offset?: number; limit?: number; filter?: string; order?: string }[] = [];
    let releaseSecondPage: (() => void) | undefined;
    const secondPageGate = new Promise<void>((resolve) => {
      releaseSecondPage = resolve;
    });
    const transport = createRouterTransport((router) => {
      router.service(Tenants, {
        list: async (request) => {
          requests.push({
            offset: request.offset,
            limit: request.limit,
            filter: request.filter,
            order: request.order,
          });
          if (request.offset === 60) {
            await secondPageGate;
          }
          const offset = request.offset ?? 0;
          const items = tenants.slice(offset, offset + Math.min(request.limit ?? 25, 60));
          return { items, size: items.length, total: tenants.length };
        },
      });
    });
    const { wrapper, queryClient } = makeWrapper(transport);
    const filter = 'this.metadata.name != ""';
    const paginatedRequest = { filter, order: 'this.metadata.name asc', limit: 25, offset: 5 };

    const { result } = renderHook(
      () => ({
        normal: useListResource(Tenants, paginatedRequest),
        all: useListAllResources(Tenants, filter),
      }),
      { wrapper },
    );

    await waitFor(() => expect(requests).toHaveLength(3));
    await waitFor(() => expect(result.current.normal.isSuccess).toBe(true));
    expect(requests).toContainEqual({
      offset: paginatedRequest.offset,
      limit: paginatedRequest.limit,
      filter,
      order: paginatedRequest.order,
    });
    expect(requests.filter(({ limit }) => limit === 100)).toEqual([
      { offset: 0, limit: 100, filter, order: undefined },
      { offset: 60, limit: 100, filter, order: undefined },
    ]);
    expect(result.current.normal.data?.items).toHaveLength(25);
    expect(result.current.normal.data?.items[0]?.id).toBe('5');
    expect(result.current.all.isLoading).toBe(true);
    expect(result.current.all.data).toBeUndefined();

    act(() => {
      releaseSecondPage?.();
    });

    await waitFor(() => expect(result.current.all.isSuccess).toBe(true));
    expect(result.current.all.data?.map(({ id }) => id)).toEqual(tenants.map(({ id }) => id));
    expect(result.current.normal.data?.items).toHaveLength(25);
    expect(queryClient.getQueryCache().findAll({ queryKey: [Tenants.typeName] })).toHaveLength(2);
  });

  it('cancels an in-flight fetch-all page when its query is unobserved', async () => {
    const tenants = [makeTenant('0'), makeTenant('1')];
    const offsets: number[] = [];
    let releaseSecondPage: (() => void) | undefined;
    const secondPageGate = new Promise<void>((resolve) => {
      releaseSecondPage = resolve;
    });
    const transport = createRouterTransport((router) => {
      router.service(Tenants, {
        list: async (request) => {
          const offset = request.offset ?? 0;
          offsets.push(offset);
          if (offset === 1) {
            await secondPageGate;
          }
          const items = tenants.slice(offset, offset + 1);
          return { items, size: items.length, total: tenants.length };
        },
      });
    });
    const unarySpy = vi.spyOn(transport, 'unary');
    const { wrapper } = makeWrapper(transport);
    const { unmount } = renderHook(() => useListAllResources(Tenants), { wrapper });

    await waitFor(() => expect(offsets).toEqual([0, 1]));
    const firstSignal = unarySpy.mock.calls[0]?.[1];
    const secondSignal = unarySpy.mock.calls[1]?.[1];

    unmount();
    act(() => releaseSecondPage?.());

    expect(firstSignal).toBeDefined();
    expect(secondSignal).toBe(firstSignal);
    expect(secondSignal?.aborted).toBe(true);
  });

  it('gets a resource with inferred request and response types', async () => {
    const transport = createMockConnectTransport({
      identityProviders: [makeIdentityProvider('idp-1')],
    });
    const { wrapper } = makeWrapper(transport);
    const { result } = renderHook(() => useGetResource(IdentityProviders, { id: 'idp-1' }), {
      wrapper,
    });

    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(result.current.data?.object?.id).toBe('idp-1');
  });
});

describe('resource mutations', () => {
  it('calls create, update, and delete and invalidates every query for the service', async () => {
    const transport = createMockConnectTransport();
    const { wrapper, queryClient } = makeWrapper(transport);
    const listKey = [IdentityProviders.typeName, 'list', {}] as const;
    queryClient.setQueryData(listKey, { items: [] });
    const { result } = renderHook(
      () => ({
        create: useCreateResource(IdentityProviders),
        update: useUpdateResource(IdentityProviders),
        delete: useDeleteResource(IdentityProviders),
      }),
      { wrapper },
    );

    await act(async () => {
      await result.current.create.mutateAsync({ object: makeIdentityProvider('created') });
    });
    expect(queryClient.getQueryState(listKey)?.isInvalidated).toBe(true);

    await act(async () => {
      await result.current.update.mutateAsync({ object: makeIdentityProvider('updated') });
      await result.current.delete.mutateAsync({ id: 'updated' });
    });

    expect(result.current.create.data?.object?.id).toBe('created');
    expect(result.current.update.data?.object?.id).toBe('updated');
    expect(result.current.delete.isSuccess).toBe(true);
  });
});
