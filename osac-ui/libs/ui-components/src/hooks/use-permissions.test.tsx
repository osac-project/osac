import React, { type ReactNode, createElement } from 'react';
import { Code, ConnectError, createRouterTransport } from '@connectrpc/connect';
import {
  QueryClient,
  QueryClientProvider,
  focusManager,
  onlineManager,
} from '@tanstack/react-query';
import { act, renderHook, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { BareMetalInstances, SelfSubjectAccessReviews, VirtualNetworks } from '@osac/types';
import { StorageBackends } from '@osac/types/private';

import { usePermissions } from './use-permissions';
import { ApiProvider } from '../api/api-context';

const mockSession = vi.hoisted(() => ({ role: 'tenant-user' as 'admin' | 'tenant-user' }));

vi.mock('./use-session', () => ({
  useSession: () => mockSession,
}));

const makeWrapper = (transport: ReturnType<typeof createRouterTransport>) => {
  const queryClient = new QueryClient({
    defaultOptions: {
      queries: {
        retry: 3,
        retryDelay: 0,
        refetchOnMount: true,
        refetchOnWindowFocus: true,
        refetchOnReconnect: true,
      },
      mutations: { retry: false },
    },
  });
  const Wrapper = ({ children }: { children: ReactNode }) =>
    createElement(
      ApiProvider,
      { transport } as React.ComponentProps<typeof ApiProvider>,
      createElement(QueryClientProvider, { client: queryClient }, children),
    );
  return Wrapper;
};

afterEach(() => {
  mockSession.role = 'tenant-user';
  focusManager.setFocused(undefined);
  onlineManager.setOnline(true);
  vi.clearAllMocks();
});

describe('usePermissions', () => {
  it('reports loading separately while the permission request is pending', async () => {
    let resolveResponse: (() => void) | undefined;
    const transport = createRouterTransport((router) => {
      router.service(SelfSubjectAccessReviews, {
        create: () =>
          new Promise((resolve) => {
            resolveResponse = () => resolve({ object: { status: { allowed: true } } });
          }),
      });
    });
    const { result } = renderHook(() => usePermissions(BareMetalInstances, 'create'), {
      wrapper: makeWrapper(transport),
    });

    expect(result.current).toEqual({ allowed: false, error: null, isLoading: true });

    await waitFor(() => expect(resolveResponse).toBeDefined());
    act(() => resolveResponse?.());
    await waitFor(() => expect(result.current.isLoading).toBe(false));

    expect(result.current).toEqual({ allowed: true, error: null, isLoading: false });
  });

  it('checks the fully-qualified service and RPC method', async () => {
    const requests: string[] = [];
    const transport = createRouterTransport((router) => {
      router.service(SelfSubjectAccessReviews, {
        create: (request) => {
          requests.push(`${request.object?.spec?.service}/${request.object?.spec?.method}`);
          return { object: { status: { allowed: true } } };
        },
      });
    });
    const { result } = renderHook(() => usePermissions(VirtualNetworks, 'create'), {
      wrapper: makeWrapper(transport),
    });

    await waitFor(() => expect(result.current.isLoading).toBe(false));

    expect(result.current).toEqual({ allowed: true, error: null, isLoading: false });
    expect(requests).toEqual(['osac.public.v1.VirtualNetworks/Create']);
  });

  it('reports denied permissions', async () => {
    const transport = createRouterTransport((router) => {
      router.service(SelfSubjectAccessReviews, {
        create: () => ({ object: { status: { allowed: false } } }),
      });
    });
    const { result } = renderHook(() => usePermissions(BareMetalInstances, 'delete'), {
      wrapper: makeWrapper(transport),
    });

    await waitFor(() => expect(result.current.isLoading).toBe(false));

    expect(result.current.allowed).toBe(false);
    expect(result.current.error).toBeNull();
  });

  it('deduplicates equivalent permission checks', async () => {
    let requests = 0;
    let resolveResponse: (() => void) | undefined;
    const transport = createRouterTransport((router) => {
      router.service(SelfSubjectAccessReviews, {
        create: () => {
          requests += 1;
          return new Promise((resolve) => {
            resolveResponse = () => resolve({ object: { status: { allowed: true } } });
          });
        },
      });
    });
    const { result } = renderHook(
      () => ({
        first: usePermissions(BareMetalInstances, 'create'),
        second: usePermissions(BareMetalInstances, 'create'),
      }),
      { wrapper: makeWrapper(transport) },
    );

    await waitFor(() => expect(resolveResponse).toBeDefined());
    expect(requests).toBe(1);
    expect(result.current.first.isLoading).toBe(true);
    expect(result.current.second.isLoading).toBe(true);

    act(() => resolveResponse?.());
    await waitFor(() => {
      expect(result.current.first.isLoading).toBe(false);
      expect(result.current.second.isLoading).toBe(false);
    });

    expect(requests).toBe(1);
    expect(result.current.first).toEqual({ allowed: true, error: null, isLoading: false });
    expect(result.current.second).toEqual({ allowed: true, error: null, isLoading: false });
  });

  it('partitions cached permission checks by service and RPC method', async () => {
    const requests: string[] = [];
    const transport = createRouterTransport((router) => {
      router.service(SelfSubjectAccessReviews, {
        create: (request) => {
          const service = request.object?.spec?.service ?? '';
          const method = request.object?.spec?.method ?? '';
          requests.push(`${service}/${method}`);
          return {
            object: {
              status: {
                allowed: service === BareMetalInstances.typeName && method === 'Create',
              },
            },
          };
        },
      });
    });
    const { result } = renderHook(
      () => ({
        bareMetalCreate: usePermissions(BareMetalInstances, 'create'),
        bareMetalDelete: usePermissions(BareMetalInstances, 'delete'),
        virtualNetworkCreate: usePermissions(VirtualNetworks, 'create'),
      }),
      { wrapper: makeWrapper(transport) },
    );

    await waitFor(() => {
      expect(result.current.bareMetalCreate.isLoading).toBe(false);
      expect(result.current.bareMetalDelete.isLoading).toBe(false);
      expect(result.current.virtualNetworkCreate.isLoading).toBe(false);
    });

    expect(requests).toHaveLength(3);
    expect(requests).toEqual(
      expect.arrayContaining([
        `${BareMetalInstances.typeName}/Create`,
        `${BareMetalInstances.typeName}/Delete`,
        `${VirtualNetworks.typeName}/Create`,
      ]),
    );
    expect(result.current.bareMetalCreate.allowed).toBe(true);
    expect(result.current.bareMetalDelete.allowed).toBe(false);
    expect(result.current.virtualNetworkCreate.allowed).toBe(false);
  });

  it('reuses a successful permission result when remounted', async () => {
    let requests = 0;
    const transport = createRouterTransport((router) => {
      router.service(SelfSubjectAccessReviews, {
        create: () => {
          requests += 1;
          return { object: { status: { allowed: true } } };
        },
      });
    });
    const wrapper = makeWrapper(transport);
    const first = renderHook(() => usePermissions(BareMetalInstances, 'create'), { wrapper });

    await waitFor(() => expect(first.result.current.isLoading).toBe(false));
    first.unmount();

    const second = renderHook(() => usePermissions(BareMetalInstances, 'create'), { wrapper });
    await waitFor(() => expect(second.result.current.isLoading).toBe(false));

    expect(second.result.current).toEqual({ allowed: true, error: null, isLoading: false });
    expect(requests).toBe(1);
    second.unmount();
  });

  it('retries transient transport failures at most twice', async () => {
    let attempts = 0;
    const transport = createRouterTransport((router) => {
      router.service(SelfSubjectAccessReviews, {
        create: () => {
          attempts += 1;
          if (attempts < 3) {
            throw new ConnectError('temporary failure', Code.Unavailable);
          }
          return { object: { status: { allowed: true } } };
        },
      });
    });
    const { result } = renderHook(() => usePermissions(BareMetalInstances, 'create'), {
      wrapper: makeWrapper(transport),
    });

    await waitFor(() => expect(result.current.allowed).toBe(true));

    expect(attempts).toBe(3);
    expect(result.current.error).toBeNull();
  });

  it('caches the terminal error after exhausting transient retries', async () => {
    let attempts = 0;
    const transport = createRouterTransport((router) => {
      router.service(SelfSubjectAccessReviews, {
        create: () => {
          attempts += 1;
          throw new ConnectError('service unavailable', Code.Unavailable);
        },
      });
    });
    const wrapper = makeWrapper(transport);
    const first = renderHook(() => usePermissions(BareMetalInstances, 'create'), { wrapper });

    await waitFor(() => expect(first.result.current.error).toBeInstanceOf(Error));
    expect(first.result.current).toMatchObject({ allowed: false, isLoading: false });
    expect(attempts).toBe(3);
    first.unmount();

    const second = renderHook(() => usePermissions(BareMetalInstances, 'create'), { wrapper });
    await waitFor(() => expect(second.result.current.error).toBeInstanceOf(Error));
    await act(async () => {
      await Promise.resolve();
    });

    expect(attempts).toBe(3);
    second.unmount();
  });

  it('keeps terminal errors cached across remount, focus, and reconnect', async () => {
    let attempts = 0;
    const transport = createRouterTransport((router) => {
      router.service(SelfSubjectAccessReviews, {
        create: () => {
          attempts += 1;
          throw new ConnectError('invalid service', Code.InvalidArgument);
        },
      });
    });
    const wrapper = makeWrapper(transport);
    const { result, unmount } = renderHook(() => usePermissions(BareMetalInstances, 'create'), {
      wrapper,
    });

    await waitFor(() => expect(result.current.error).toBeInstanceOf(Error));
    expect(attempts).toBe(1);
    expect(result.current.allowed).toBe(false);
    expect(result.current.isLoading).toBe(false);

    await act(async () => {
      focusManager.setFocused(false);
      focusManager.setFocused(true);
      onlineManager.setOnline(false);
      onlineManager.setOnline(true);
      await Promise.resolve();
    });
    expect(attempts).toBe(1);

    unmount();
    const remounted = renderHook(() => usePermissions(BareMetalInstances, 'create'), {
      wrapper,
    });
    await waitFor(() => expect(remounted.result.current.error).toBeInstanceOf(Error));
    await act(async () => {
      await Promise.resolve();
    });

    expect(remounted.result.current.allowed).toBe(false);
    expect(remounted.result.current.isLoading).toBe(false);
    expect(attempts).toBe(1);
    remounted.unmount();
  });

  it.each([
    ['missing response object', {}],
    ['missing response status', { object: {} }],
  ])('reports %s as a contract error', async (_description, response) => {
    let attempts = 0;
    const transport = createRouterTransport((router) => {
      router.service(SelfSubjectAccessReviews, {
        create: () => {
          attempts += 1;
          return response;
        },
      });
    });
    const { result } = renderHook(() => usePermissions(BareMetalInstances, 'create'), {
      wrapper: makeWrapper(transport),
    });

    await waitFor(() => expect(result.current.error).toBeInstanceOf(Error));

    expect(result.current.allowed).toBe(false);
    expect(result.current.isLoading).toBe(false);
    expect(attempts).toBe(1);
  });

  it('bypasses SSAR checks for private services when the user is an admin', () => {
    mockSession.role = 'admin';
    let requests = 0;
    const transport = createRouterTransport((router) => {
      router.service(SelfSubjectAccessReviews, {
        create: () => {
          requests += 1;
          return { object: { status: { allowed: false } } };
        },
      });
    });
    const { result } = renderHook(() => usePermissions(StorageBackends, 'create'), {
      wrapper: makeWrapper(transport),
    });

    expect(result.current).toEqual({ allowed: true, error: null, isLoading: false });
    expect(requests).toBe(0);
  });

  it('checks public services through SSAR for admins', async () => {
    mockSession.role = 'admin';
    let requests = 0;
    const transport = createRouterTransport((router) => {
      router.service(SelfSubjectAccessReviews, {
        create: (request) => {
          expect(request.object?.spec?.service).toBe(VirtualNetworks.typeName);
          expect(request.object?.spec?.method).toBe('Create');
          requests += 1;
          return { object: { status: { allowed: false } } };
        },
      });
    });
    const { result } = renderHook(() => usePermissions(VirtualNetworks, 'create'), {
      wrapper: makeWrapper(transport),
    });

    await waitFor(() => expect(result.current.isLoading).toBe(false));

    expect(result.current).toEqual({ allowed: false, error: null, isLoading: false });
    expect(requests).toBe(1);
  });

  it('denies private services locally for non-admins', () => {
    let requests = 0;
    const transport = createRouterTransport((router) => {
      router.service(SelfSubjectAccessReviews, {
        create: () => {
          requests += 1;
          return { object: { status: { allowed: true } } };
        },
      });
    });
    const { result } = renderHook(() => usePermissions(StorageBackends, 'create'), {
      wrapper: makeWrapper(transport),
    });

    expect(result.current).toEqual({ allowed: false, error: null, isLoading: false });
    expect(requests).toBe(0);
  });
});
