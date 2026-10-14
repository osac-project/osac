import React, { type ReactNode, createElement } from 'react';
import { create } from '@bufbuild/protobuf';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, renderHook, waitFor } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import {
  BareMetalInstanceTypeSchema,
  type BareMetalInstanceType as PrivateBareMetalInstanceType,
} from '@osac/types/private';

import {
  invalidateBareMetalInstanceTypesQueries,
  useAdminBareMetalInstanceTypes,
  useCreateBareMetalInstanceType,
  useDeleteBareMetalInstanceType,
  useUpdateBareMetalInstanceType,
} from './baremetal-instance-type';
import { createMockConnectTransport } from '../../../test-utils/createMockConnectTransport';
import { ApiProvider } from '../../api-context';
import { cel } from '../../cel';

const makeBareMetalInstanceType = (id: string): PrivateBareMetalInstanceType =>
  create(BareMetalInstanceTypeSchema, {
    id,
    metadata: { name: `bm-type-${id}` },
    spec: {
      hardware: {
        cpu: { cores: 64, architecture: 'x86_64' },
        memory: { totalGb: 256n },
        accelerators: [],
      },
      description: `${id} description`,
    },
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

describe('useAdminBareMetalInstanceTypes', () => {
  it('returns all bare metal instance type items from the list response', async () => {
    const transport = createMockConnectTransport({
      privateBaremetalInstanceTypes: [
        makeBareMetalInstanceType('gpu-1'),
        makeBareMetalInstanceType('cpu-1'),
      ],
    });
    const { wrapper } = makeWrapper(transport);
    const { result } = renderHook(() => useAdminBareMetalInstanceTypes(), { wrapper });

    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(result.current.data?.map((item) => item.id)).toEqual(['gpu-1', 'cpu-1']);
  });

  it('stores query results under the private bare metal instance type cache key', async () => {
    const transport = createMockConnectTransport({
      privateBaremetalInstanceTypes: [makeBareMetalInstanceType('gpu-1')],
    });
    const { wrapper, queryClient } = makeWrapper(transport);
    const { result } = renderHook(() => useAdminBareMetalInstanceTypes(), { wrapper });

    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(queryClient.getQueryData(['v1/private/baremetal_instance_types'])).toBeDefined();
    expect(queryClient.getQueryData(['v1/instance_types'])).toBeUndefined();
  });

  it('forwards the server-side filter parameter to the list request', async () => {
    let captured: Record<string, unknown> | undefined;
    const transport = createMockConnectTransport(
      {},
      {
        onBaremetalInstanceTypeList: (req) => {
          captured = req;
          return { items: [], size: 0, total: 0 };
        },
      },
    );
    const { wrapper } = makeWrapper(transport);
    const { result } = renderHook(
      () =>
        useAdminBareMetalInstanceTypes({
          filter: cel<PrivateBareMetalInstanceType>((filter) =>
            filter.field('metadata.name').equals('gpu-1'),
          ),
        }),
      { wrapper },
    );

    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(captured?.filter).toBe('this.metadata.name == "gpu-1"');
  });
});

describe('useDeleteBareMetalInstanceType', () => {
  it('deletes a bare metal instance type by id', async () => {
    const transport = createMockConnectTransport({
      privateBaremetalInstanceTypes: [makeBareMetalInstanceType('gpu-1')],
    });
    const { wrapper } = makeWrapper(transport);
    const { result } = renderHook(() => useDeleteBareMetalInstanceType(), { wrapper });

    act(() => {
      result.current.mutate('gpu-1');
    });

    await waitFor(() => expect(result.current.isSuccess || result.current.isError).toBe(true));
    expect(result.current.isSuccess).toBe(true);
  });

  it('invalidates the list query after a successful delete', async () => {
    const transport = createMockConnectTransport({
      privateBaremetalInstanceTypes: [makeBareMetalInstanceType('gpu-1')],
    });
    const { wrapper, queryClient } = makeWrapper(transport);
    queryClient.setQueryData(['v1/private/baremetal_instance_types'], { items: [] });
    const { result } = renderHook(() => useDeleteBareMetalInstanceType(), { wrapper });

    act(() => {
      result.current.mutate('gpu-1');
    });

    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(queryClient.getQueryState(['v1/private/baremetal_instance_types'])?.isInvalidated).toBe(
      true,
    );
  });
});

describe('useCreateBareMetalInstanceType', () => {
  it('sends the object to the create RPC and returns the created type', async () => {
    let captured: Record<string, unknown> | undefined;
    const transport = createMockConnectTransport(
      {},
      {
        onBaremetalInstanceTypeCreate: (req) => {
          captured = req;
          return { object: { ...req.object, id: 'created-1' } };
        },
      },
    );
    const { wrapper } = makeWrapper(transport);
    const { result } = renderHook(() => useCreateBareMetalInstanceType(), { wrapper });

    let created: { id: string } | undefined;
    await act(async () => {
      created = await result.current.mutateAsync({
        metadata: { name: 'bm-new' },
        spec: {
          description: 'new type',
          hardware: { cpu: { cores: 64, architecture: 'x86_64' }, memory: { totalGb: 256n } },
          hostLabelSelector: { matchLabels: { tier: 'gpu' } },
        },
      });
    });

    expect((captured?.object as { metadata?: { name?: string } })?.metadata?.name).toBe('bm-new');
    expect(created?.id).toBe('created-1');
  });

  it('invalidates the list query after a successful create', async () => {
    const transport = createMockConnectTransport({});
    const { wrapper, queryClient } = makeWrapper(transport);
    queryClient.setQueryData(['v1/private/baremetal_instance_types'], { items: [] });
    const { result } = renderHook(() => useCreateBareMetalInstanceType(), { wrapper });

    await act(async () => {
      await result.current.mutateAsync({ metadata: { name: 'bm-new' } });
    });

    expect(queryClient.getQueryState(['v1/private/baremetal_instance_types'])?.isInvalidated).toBe(
      true,
    );
  });
});

describe('useUpdateBareMetalInstanceType', () => {
  it('sends the id, body, and a field mask derived from the body', async () => {
    let captured: { object?: { id?: string }; updateMask?: { paths?: string[] } } | undefined;
    const transport = createMockConnectTransport(
      {},
      {
        onBaremetalInstanceTypeUpdate: (req) => {
          captured = req;
          return { object: req.object };
        },
      },
    );
    const { wrapper } = makeWrapper(transport);
    const { result } = renderHook(() => useUpdateBareMetalInstanceType(), { wrapper });

    await act(async () => {
      await result.current.mutateAsync({
        id: 'gpu-1',
        body: {
          metadata: { name: 'bm-type-gpu-1' },
          spec: {
            description: 'updated',
            hardware: { capabilities: { 'sr-iov': 'true' } },
            hostLabelSelector: { matchLabels: { tier: 'gpu' } },
          },
        },
      });
    });

    expect(captured?.object?.id).toBe('gpu-1');
    expect(captured?.updateMask?.paths).toContain('metadata.name');
    expect(captured?.updateMask?.paths).toContain('spec.description');
    expect(captured?.updateMask?.paths).toContain('spec.hardware.capabilities');
    expect(captured?.updateMask?.paths).toContain('spec.host_label_selector.match_labels');
    expect(captured?.updateMask?.paths).not.toContain('spec.hardware.capabilities.sr-iov');
  });

  it('invalidates the list query after a successful update', async () => {
    const transport = createMockConnectTransport({});
    const { wrapper, queryClient } = makeWrapper(transport);
    queryClient.setQueryData(['v1/private/baremetal_instance_types'], { items: [] });
    const { result } = renderHook(() => useUpdateBareMetalInstanceType(), { wrapper });

    await act(async () => {
      await result.current.mutateAsync({ id: 'gpu-1', body: { spec: { description: 'x' } } });
    });

    expect(queryClient.getQueryState(['v1/private/baremetal_instance_types'])?.isInvalidated).toBe(
      true,
    );
  });
});

describe('invalidateBareMetalInstanceTypesQueries', () => {
  const asApiQueryClient = (qc: QueryClient) =>
    qc as unknown as Parameters<typeof invalidateBareMetalInstanceTypesQueries>[0];

  it('invalidates the private bare metal instance types list query', async () => {
    const qc = new QueryClient();
    qc.setQueryData(['v1/private/baremetal_instance_types'], { items: [] });

    await invalidateBareMetalInstanceTypesQueries(asApiQueryClient(qc));

    expect(qc.getQueryState(['v1/private/baremetal_instance_types'])?.isInvalidated).toBe(true);
  });
});
