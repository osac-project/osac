import React, { type ReactNode, createElement } from 'react';
import { MemoryRouter } from 'react-router-dom';
import { createRouterTransport } from '@connectrpc/connect';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, renderHook, waitFor } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import {
  type BareMetalInstance,
  BareMetalInstanceRunStrategy,
  BareMetalInstances,
  Capabilities,
} from '@osac/types';

import {
  type PatchBareMetalInstanceInput,
  useBareMetalInstances,
  usePatchBareMetalInstance,
} from './baremetal-instance';
import { SessionProvider } from '../../hooks/use-session';
import { ApiProvider } from '../api-context';
import { type CelFilter } from '../cel';

const makeBmi = (id: string) => ({
  id,
  metadata: { name: `bmi-${id}` },
  spec: {
    catalogItem: { id: 'catalog-1' },
    runStrategy: BareMetalInstanceRunStrategy.ALWAYS,
    restartTrigger: 0n,
  },
  status: {},
});

describe('useBareMetalInstances', () => {
  it('returns filtered items and unfiltered total from separate list requests', async () => {
    const listCalls: { filter?: string; limit?: number }[] = [];
    const transport = createRouterTransport((router) => {
      router.service(Capabilities, {
        get: () => ({ enabledServices: [] }),
      });
      router.service(BareMetalInstances, {
        list: (req) => {
          listCalls.push({ filter: req.filter, limit: req.limit });
          if (req.limit === 0) {
            return { items: [], total: 2 };
          }
          return { items: [makeBmi('bmi-1')], total: 1 };
        },
      });
    });

    const queryClient = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    });
    const wrapper = ({ children }: { children: ReactNode }) =>
      createElement(
        ApiProvider,
        { transport } as React.ComponentProps<typeof ApiProvider>,
        createElement(
          QueryClientProvider,
          { client: queryClient },
          createElement(
            MemoryRouter,
            null,
            // eslint-disable-next-line react/no-children-prop
            createElement(SessionProvider, {
              role: 'tenant-user',
              username: 'test-user',
              tenantId: 'test-tenant',
              children,
            }),
          ),
        ),
      );

    const filter = 'this.status.state == 1' as CelFilter<BareMetalInstance>;
    const { result } = renderHook(() => useBareMetalInstances(filter), { wrapper });

    await waitFor(() => expect(result.current.isLoading).toBe(false));

    expect(result.current.instances).toHaveLength(1);
    expect(result.current.totalItems).toBe(2);
    expect(listCalls).toContainEqual({ filter, limit: undefined });
    expect(listCalls).toContainEqual({ filter: undefined, limit: 0 });
  });
});

describe('usePatchBareMetalInstance', () => {
  const createTestTransport = (updateFn: (req: unknown) => void) =>
    createRouterTransport((router) => {
      router.service(BareMetalInstances, {
        list: () => ({ items: [makeBmi('bmi-1')] }),
        get: () => ({ object: makeBmi('bmi-1') }),
        update: (req) => {
          updateFn(req);
          return { object: makeBmi('bmi-1') };
        },
      });
    });

  const renderUsePatch = (transport: ReturnType<typeof createRouterTransport>) => {
    const queryClient = new QueryClient({
      defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
    });
    const wrapper = ({ children }: { children: ReactNode }) =>
      createElement(
        ApiProvider,
        { transport } as React.ComponentProps<typeof ApiProvider>,
        createElement(QueryClientProvider, { client: queryClient }, children),
      );
    return { ...renderHook(() => usePatchBareMetalInstance(), { wrapper }), queryClient };
  };

  const mutateAndCapture = async (input: PatchBareMetalInstanceInput) => {
    let captured: Record<string, unknown> | undefined;
    const transport = createTestTransport((req) => {
      captured = req as Record<string, unknown>;
    });
    const { result } = renderUsePatch(transport);

    act(() => {
      result.current.mutate(input);
    });

    await waitFor(() => expect(result.current.isSuccess || result.current.isError).toBe(true));
    expect(result.current.isSuccess).toBe(true);
    return captured;
  };

  it('sends updateMask with spec.run_strategy for stop action', async () => {
    const req = await mutateAndCapture({ id: 'bmi-1', action: 'stop' });
    expect(req).toBeDefined();
    expect((req as { updateMask?: { paths: string[] } }).updateMask?.paths).toEqual([
      'spec.run_strategy',
    ]);
  });

  it('sends updateMask with spec.run_strategy for start action', async () => {
    const req = await mutateAndCapture({ id: 'bmi-1', action: 'start' });
    expect(req).toBeDefined();
    expect((req as { updateMask?: { paths: string[] } }).updateMask?.paths).toEqual([
      'spec.run_strategy',
    ]);
  });

  it('sends updateMask with spec.restart_trigger for restart action', async () => {
    const req = await mutateAndCapture({ id: 'bmi-1', action: 'restart', currentTrigger: 0n });
    expect(req).toBeDefined();
    expect((req as { updateMask?: { paths: string[] } }).updateMask?.paths).toEqual([
      'spec.restart_trigger',
    ]);
  });

  it('sets run_strategy to HALTED for stop action', async () => {
    const req = await mutateAndCapture({ id: 'bmi-1', action: 'stop' });
    const object = (req as { object?: { spec?: { runStrategy?: number } } }).object;
    expect(object?.spec?.runStrategy).toBe(BareMetalInstanceRunStrategy.HALTED);
  });

  it('sets run_strategy to ALWAYS for start action', async () => {
    const req = await mutateAndCapture({ id: 'bmi-1', action: 'start' });
    const object = (req as { object?: { spec?: { runStrategy?: number } } }).object;
    expect(object?.spec?.runStrategy).toBe(BareMetalInstanceRunStrategy.ALWAYS);
  });

  it('increments restart_trigger for restart action', async () => {
    const req = await mutateAndCapture({ id: 'bmi-1', action: 'restart', currentTrigger: 3n });
    const object = (req as { object?: { spec?: { restartTrigger?: bigint } } }).object;
    expect(object?.spec?.restartTrigger).toBe(4n);
  });
});
