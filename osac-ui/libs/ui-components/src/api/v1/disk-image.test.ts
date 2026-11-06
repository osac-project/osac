import React, { type ReactNode, createElement } from 'react';
import { create } from '@bufbuild/protobuf';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, renderHook, waitFor } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import {
  Architecture,
  type DiskImage,
  DiskImageLifecycle,
  DiskImageSchema,
  DiskImagesCreateResponseSchema,
  DiskImagesUpdateResponseSchema,
  GuestOSFamily,
} from '@osac/types';

import {
  DISK_IMAGE_NON_OBSOLETE_FILTER,
  buildDiskImageListFilter,
  invalidateDiskImagesQueries,
  useCreateDiskImage,
  useDiskImage,
  useDiskImages,
  useUpdateDiskImage,
} from './disk-image';
import { createMockConnectTransport } from '../../test-utils/createMockConnectTransport';
import { ApiProvider } from '../api-context';
import { cel } from '../cel';

const makeDiskImage = (
  id: string,
  lifecycle: DiskImageLifecycle = DiskImageLifecycle.AVAILABLE,
): DiskImage =>
  create(DiskImageSchema, {
    id,
    metadata: { name: `disk-image-${id}` },
    spec: {
      sourceRef: `quay.io/example/${id}:latest`,
      lifecycle,
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

describe('DISK_IMAGE_NON_OBSOLETE_FILTER', () => {
  it('excludes OBSOLETE via a single-enum != comparison', () => {
    expect(DISK_IMAGE_NON_OBSOLETE_FILTER).toBe(
      `this.spec.lifecycle != ${DiskImageLifecycle.OBSOLETE}`,
    );
  });
});

describe('useDiskImages', () => {
  it('unwraps the list response into items', async () => {
    const transport = createMockConnectTransport({
      diskImages: [makeDiskImage('a'), makeDiskImage('b')],
    });
    const { wrapper } = makeWrapper(transport);
    const { result } = renderHook(() => useDiskImages(), { wrapper });

    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(result.current.data?.map((item) => item.id)).toEqual(['a', 'b']);
  });

  it('appends the default OBSOLETE-exclusion filter when no caller filter is given', async () => {
    let capturedFilter: string | undefined;
    const transport = createMockConnectTransport(
      {},
      {
        onDiskImageList: (req) => {
          capturedFilter = req.filter;
          return { items: [] };
        },
      },
    );
    const { wrapper } = makeWrapper(transport);
    const { result } = renderHook(() => useDiskImages(), { wrapper });

    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(capturedFilter).toBe(DISK_IMAGE_NON_OBSOLETE_FILTER);
  });

  it('leaves a caller filter that already references spec.lifecycle unmodified', async () => {
    let capturedFilter: string | undefined;
    const transport = createMockConnectTransport(
      {},
      {
        onDiskImageList: (req) => {
          capturedFilter = req.filter;
          return { items: [] };
        },
      },
    );
    const callerFilter = cel<DiskImage>((filter) =>
      filter.field('spec.lifecycle').equals(DiskImageLifecycle.AVAILABLE),
    );
    const { wrapper } = makeWrapper(transport);
    const { result } = renderHook(() => useDiskImages({ filter: callerFilter }), { wrapper });

    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(capturedFilter).toBe(callerFilter);
  });

  it('still appends the default filter when the caller filter merely contains the lifecycle field name as text, not a predicate', async () => {
    let capturedFilter: string | undefined;
    const transport = createMockConnectTransport(
      {},
      {
        onDiskImageList: (req) => {
          capturedFilter = req.filter;
          return { items: [] };
        },
      },
    );
    const callerFilter = cel<DiskImage>((filter) =>
      filter.field('metadata.name').contains('this.spec.lifecycle'),
    );
    const { wrapper } = makeWrapper(transport);
    const { result } = renderHook(() => useDiskImages({ filter: callerFilter }), { wrapper });

    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(capturedFilter).toBe(`(${callerFilter}) && ${DISK_IMAGE_NON_OBSOLETE_FILTER}`);
  });

  it('combines the default filter with a caller filter that does not reference lifecycle', async () => {
    let capturedFilter: string | undefined;
    const transport = createMockConnectTransport(
      {},
      {
        onDiskImageList: (req) => {
          capturedFilter = req.filter;
          return { items: [] };
        },
      },
    );
    const callerFilter = cel<DiskImage>((filter) =>
      filter.field('spec.architecture').includes(Architecture.AMD64),
    );
    const { wrapper } = makeWrapper(transport);
    const { result } = renderHook(() => useDiskImages({ filter: callerFilter }), { wrapper });

    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(capturedFilter).toBe(`(${callerFilter}) && ${DISK_IMAGE_NON_OBSOLETE_FILTER}`);
  });
});

describe('buildDiskImageListFilter', () => {
  it.each([
    ['no criteria', {}, undefined],
    ['search only', { search: 'fedora' }, 'this.metadata.name.contains("fedora")'],
    [
      'search escapes quotes and backslashes to stay inside the CEL string literal',
      { search: 'a"b\\c' },
      'this.metadata.name.contains("a\\"b\\\\c")',
    ],
    [
      'guestOsFamily only',
      { guestOsFamily: GuestOSFamily.GUEST_OS_FAMILY_LINUX },
      `this.spec.guest_os_family == ${GuestOSFamily.GUEST_OS_FAMILY_LINUX}`,
    ],
    [
      'single architecture value',
      { architecture: [Architecture.AMD64] },
      `${Architecture.AMD64} in this.spec.architecture`,
    ],
    [
      'multiple architecture values',
      { architecture: [Architecture.AMD64, Architecture.ARM64] },
      `(${Architecture.AMD64} in this.spec.architecture || ${Architecture.ARM64} in this.spec.architecture)`,
    ],
    ['scope global', { scope: 'global' as const }, 'this.metadata.tenant == "shared"'],
    ['scope tenant', { scope: 'tenant' as const }, 'this.metadata.tenant != "shared"'],
    [
      'single lifecycle value, no show-obsolete',
      { lifecycle: [DiskImageLifecycle.AVAILABLE] },
      `this.spec.lifecycle == ${DiskImageLifecycle.AVAILABLE}`,
    ],
    [
      'multiple lifecycle values, no show-obsolete',
      { lifecycle: [DiskImageLifecycle.AVAILABLE, DiskImageLifecycle.DEPRECATED] },
      `(this.spec.lifecycle == ${DiskImageLifecycle.AVAILABLE} || this.spec.lifecycle == ${DiskImageLifecycle.DEPRECATED})`,
    ],
    [
      'show-obsolete with an explicit lifecycle selection unions in OBSOLETE',
      { lifecycle: [DiskImageLifecycle.AVAILABLE], showObsolete: true },
      `(this.spec.lifecycle == ${DiskImageLifecycle.AVAILABLE} || this.spec.lifecycle == ${DiskImageLifecycle.OBSOLETE})`,
    ],
    [
      'show-obsolete with no lifecycle selection widens to every state',
      { showObsolete: true },
      `(this.spec.lifecycle == ${DiskImageLifecycle.UNSPECIFIED} || this.spec.lifecycle == ${DiskImageLifecycle.AVAILABLE} || this.spec.lifecycle == ${DiskImageLifecycle.DEPRECATED} || this.spec.lifecycle == ${DiskImageLifecycle.OBSOLETE})`,
    ],
    [
      'multiple dimensions combine with &&',
      {
        search: 'fedora',
        guestOsFamily: GuestOSFamily.GUEST_OS_FAMILY_LINUX,
        scope: 'global' as const,
      },
      `this.metadata.name.contains("fedora") && this.spec.guest_os_family == ${GuestOSFamily.GUEST_OS_FAMILY_LINUX} && this.metadata.tenant == "shared"`,
    ],
  ])('%s', (_name, criteria, expected) => {
    expect(buildDiskImageListFilter(criteria)).toBe(expected);
  });

  it('produces a backend-compatible filter when architecture is the only criterion (OSAC-6283)', () => {
    const filter = buildDiskImageListFilter({ architecture: [Architecture.S390X] });

    // The filter must use CEL `in` (not `exists()`) so the backend can
    // translate it without hitting "unsupported expression kind 2".
    expect(filter).toBe(`${Architecture.S390X} in this.spec.architecture`);
    expect(filter).not.toContain('.exists(');
  });

  it('returns an empty result rather than an error when no disk images match the architecture filter (OSAC-6283)', async () => {
    let capturedFilter: string | undefined;
    const transport = createMockConnectTransport(
      { diskImages: [] },
      {
        onDiskImageList: (req) => {
          capturedFilter = req.filter;
          return { items: [] };
        },
      },
    );
    const archFilter = buildDiskImageListFilter({ architecture: [Architecture.S390X] });
    const { wrapper } = makeWrapper(transport);
    const { result } = renderHook(() => useDiskImages({ filter: archFilter }), { wrapper });

    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(result.current.data).toEqual([]);
    expect(capturedFilter).toBeDefined();
    expect(capturedFilter).not.toContain('.exists(');
  });

  it('is not double-excluded by the default lifecycle filter when passed through useDiskImages', async () => {
    let capturedFilter: string | undefined;
    const transport = createMockConnectTransport(
      {},
      {
        onDiskImageList: (req) => {
          capturedFilter = req.filter;
          return { items: [] };
        },
      },
    );
    const composedFilter = buildDiskImageListFilter({ showObsolete: true });
    const { wrapper } = makeWrapper(transport);
    const { result } = renderHook(() => useDiskImages({ filter: composedFilter }), { wrapper });

    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(capturedFilter).toBe(composedFilter);
  });
});

describe('useDiskImage', () => {
  it('fetches a single disk image by id', async () => {
    const transport = createMockConnectTransport({
      diskImages: [makeDiskImage('a')],
    });
    const { wrapper } = makeWrapper(transport);
    const { result } = renderHook(() => useDiskImage('a'), { wrapper });

    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(result.current.data?.id).toBe('a');
  });

  it('is disabled when the id is falsy', () => {
    const transport = createMockConnectTransport({
      diskImages: [makeDiskImage('a')],
    });
    const { wrapper } = makeWrapper(transport);
    const { result } = renderHook(() => useDiskImage(undefined), { wrapper });

    expect(result.current.fetchStatus).toBe('idle');
    expect(result.current.data).toBeUndefined();
  });
});

describe('useCreateDiskImage', () => {
  it('creates a disk image and invalidates the list query', async () => {
    const transport = createMockConnectTransport(
      {},
      {
        onDiskImageCreate: () =>
          create(DiskImagesCreateResponseSchema, {
            object: makeDiskImage('new-di'),
          }),
      },
    );
    const { wrapper, queryClient } = makeWrapper(transport);
    queryClient.setQueryData(['v1/disk_images'], { items: [] });
    const { result } = renderHook(() => useCreateDiskImage(), { wrapper });

    act(() => {
      result.current.mutate({
        metadata: { name: 'new-di' },
        spec: { sourceRef: 'quay.io/example/new-di:latest' },
      });
    });

    await waitFor(() => expect(result.current.isSuccess || result.current.isError).toBe(true));
    expect(result.current.isSuccess).toBe(true);
    expect(result.current.data?.id).toBe('new-di');
    expect(queryClient.getQueryState(['v1/disk_images'])?.isInvalidated).toBe(true);
  });

  it('rejects when the create response is missing an object', async () => {
    const transport = createMockConnectTransport(
      {},
      { onDiskImageCreate: () => create(DiskImagesCreateResponseSchema, {}) },
    );
    const { wrapper } = makeWrapper(transport);
    const { result } = renderHook(() => useCreateDiskImage(), { wrapper });

    act(() => {
      result.current.mutate({ metadata: { name: 'new-di' } });
    });

    await waitFor(() => expect(result.current.isSuccess || result.current.isError).toBe(true));
    expect(result.current.isError).toBe(true);
  });
});

describe('useUpdateDiskImage', () => {
  it('sends the given body with a matching update mask and invalidates the list query', async () => {
    let captured: Record<string, unknown> | undefined;
    const transport = createMockConnectTransport(
      { diskImages: [makeDiskImage('di-1')] },
      {
        onDiskImageUpdate: (req) => {
          captured = req as unknown as Record<string, unknown>;
          return create(DiskImagesUpdateResponseSchema, {
            object: makeDiskImage('di-1', DiskImageLifecycle.DEPRECATED),
          });
        },
      },
    );
    const { wrapper, queryClient } = makeWrapper(transport);
    queryClient.setQueryData(['v1/disk_images'], { items: [] });
    const { result } = renderHook(() => useUpdateDiskImage(), { wrapper });

    act(() => {
      result.current.mutate({
        id: 'di-1',
        body: { spec: { lifecycle: DiskImageLifecycle.DEPRECATED } },
      });
    });

    await waitFor(() => expect(result.current.isSuccess || result.current.isError).toBe(true));
    expect(result.current.isSuccess).toBe(true);
    expect((captured?.updateMask as { paths?: string[] } | undefined)?.paths).toEqual([
      'spec.lifecycle',
    ]);
    const object = captured?.object as { id?: string };
    expect(object.id).toBe('di-1');
    expect(queryClient.getQueryState(['v1/disk_images'])?.isInvalidated).toBe(true);
  });
});

describe('invalidateDiskImagesQueries', () => {
  const asApiQueryClient = (qc: QueryClient) =>
    qc as unknown as Parameters<typeof invalidateDiskImagesQueries>[0];

  it('invalidates the disk images list query', async () => {
    const qc = new QueryClient();
    qc.setQueryData(['v1/disk_images'], { items: [] });

    await invalidateDiskImagesQueries(asApiQueryClient(qc));

    expect(qc.getQueryState(['v1/disk_images'])?.isInvalidated).toBe(true);
  });
});
