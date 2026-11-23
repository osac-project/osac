import React, { type ReactNode, createElement } from 'react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { renderHook, waitFor } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import { Architecture, DiskImageLifecycle, DiskImages, GuestOSFamily } from '@osac/types';

import {
  DISK_IMAGE_NON_OBSOLETE_FILTER,
  buildDiskImageListFilter,
  diskImageListRequestFilter,
} from './disk-image';
import { createMockConnectTransport } from '../../test-utils/createMockConnectTransport';
import { ApiProvider } from '../api-context';
import { useListResource } from '../use-resource';

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
      `this.spec.architecture.exists(a, a == ${Architecture.AMD64})`,
    ],
    [
      'multiple architecture values',
      { architecture: [Architecture.AMD64, Architecture.ARM64] },
      `this.spec.architecture.exists(a, a == ${Architecture.AMD64} || a == ${Architecture.ARM64})`,
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

  it('keeps default obsolete exclusion when search text resembles a lifecycle predicate', () => {
    const composedFilter = diskImageListRequestFilter({ search: 'this.spec.lifecycle ==' });
    expect(composedFilter).toContain(DISK_IMAGE_NON_OBSOLETE_FILTER);
    expect(composedFilter).toContain('this.metadata.name.contains("this.spec.lifecycle ==")');
  });

  it('is not double-excluded by the default lifecycle filter when passed through useListResource', async () => {
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
    const composedFilter = diskImageListRequestFilter({ showObsolete: true });
    const { wrapper } = makeWrapper(transport);
    const { result } = renderHook(() => useListResource(DiskImages, { filter: composedFilter }), {
      wrapper,
    });

    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(capturedFilter).toBe(composedFilter);
    expect(capturedFilter).not.toContain(DISK_IMAGE_NON_OBSOLETE_FILTER);
  });
});
