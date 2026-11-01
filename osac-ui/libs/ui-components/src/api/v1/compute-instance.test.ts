import React, { type ReactNode, createElement } from 'react';
import { createRouterTransport } from '@connectrpc/connect';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, renderHook, waitFor } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import { ComputeInstanceState, ComputeInstances } from '@osac/types';

import { invalidateComputeInstancesQueries, usePatchComputeInstance } from './compute-instance';
import { ApiProvider } from '../api-context';

const makeVm = (id: string, state: ComputeInstanceState) => ({
  id,
  status: { state },
  metadata: { name: `vm-${id}` },
  spec: {},
});

describe('usePatchComputeInstance', () => {
  const createTestTransport = (updateFn: (req: unknown) => void) =>
    createRouterTransport((router) => {
      router.service(ComputeInstances, {
        list: () => ({
          items: [makeVm('vm-1', ComputeInstanceState.RUNNING)],
        }),
        get: () => ({
          object: makeVm('vm-1', ComputeInstanceState.RUNNING),
        }),
        update: (req) => {
          updateFn(req);
          return { object: makeVm('vm-1', ComputeInstanceState.STOPPING) };
        },
      });
    });

  const renderUsePatchComputeInstance = (transport: ReturnType<typeof createRouterTransport>) => {
    const queryClient = new QueryClient({
      defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
    });
    const wrapper = ({ children }: { children: ReactNode }) =>
      createElement(
        ApiProvider,
        { transport } as React.ComponentProps<typeof ApiProvider>,
        createElement(QueryClientProvider, { client: queryClient }, children),
      );
    return { ...renderHook(() => usePatchComputeInstance(), { wrapper }), queryClient };
  };

  const mutateAndCapture = async (powerAction: 'start' | 'stop' | 'restart') => {
    let captured: Record<string, unknown> | undefined;
    const transport = createTestTransport((req) => {
      captured = req as Record<string, unknown>;
    });
    const { result } = renderUsePatchComputeInstance(transport);

    act(() => {
      result.current.mutate({ id: 'vm-1', powerAction });
    });

    await waitFor(() => expect(result.current.isSuccess || result.current.isError).toBe(true));
    expect(result.current.isSuccess).toBe(true);
    return captured;
  };

  it('sends updateMask with spec and status paths for stop action', async () => {
    const req = await mutateAndCapture('stop');
    expect(req).toBeDefined();
    const paths = (req as { updateMask?: { paths: string[] } }).updateMask?.paths;
    expect(paths).toContain('spec.run_strategy');
    expect(paths).toContain('status.state');
  });

  it('sends updateMask with spec and status paths for start action', async () => {
    const req = await mutateAndCapture('start');
    expect(req).toBeDefined();
    const paths = (req as { updateMask?: { paths: string[] } }).updateMask?.paths;
    expect(paths).toContain('spec.run_strategy');
    expect(paths).toContain('status.state');
  });

  it('sends updateMask with spec.restart_requested_at for restart action', async () => {
    const req = await mutateAndCapture('restart');
    expect(req).toBeDefined();
    const paths = (req as { updateMask?: { paths: string[] } }).updateMask?.paths;
    expect(paths).toContain('spec.restart_requested_at');
  });

  it('resolves successfully after a stop action', async () => {
    const req = await mutateAndCapture('stop');
    expect(req).toBeDefined();
  });
});

describe('invalidateComputeInstancesQueries', () => {
  const asApiQueryClient = (qc: QueryClient) =>
    ({
      invalidateQueries: (filters) => qc.invalidateQueries(filters),
      invalidateServiceQueries: (service) => qc.invalidateQueries({ queryKey: [service.typeName] }),
    }) as Parameters<typeof invalidateComputeInstancesQueries>[0];

  it('invalidates useListResource list queries and legacy get query keys', async () => {
    const qc = new QueryClient();
    const listKey = [ComputeInstances.typeName, 'list', {}] as const;
    qc.setQueryData(listKey, { items: [] });
    qc.setQueryData(['v1/compute_instances'], { items: [] });

    await invalidateComputeInstancesQueries(asApiQueryClient(qc));

    expect(qc.getQueryState(listKey)?.isInvalidated).toBe(true);
    expect(qc.getQueryState(['v1/compute_instances'])?.isInvalidated).toBe(true);
  });
});
