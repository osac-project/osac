/* eslint-disable @typescript-eslint/no-unsafe-assignment -- vi.mock breaks ESLint type resolution */
import React, { type ReactNode, createElement } from 'react';
import { initReactI18next } from 'react-i18next';
import { Code, ConnectError, createRouterTransport } from '@connectrpc/connect';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, renderHook, waitFor } from '@testing-library/react';
import i18n from 'i18next';
import { type Mock, beforeEach, describe, expect, it, vi } from 'vitest';

import { ComputeInstanceState, ComputeInstances } from '@osac/types';

import { useVmPowerAction } from './useVmPowerAction';
import { ApiProvider } from '../../api/api-context';

i18n.use(initReactI18next).init({
  initImmediate: false,
  lng: 'en',
  fallbackLng: 'en',
  resources: { en: { translation: {} } },
  interpolation: { escapeValue: false },
});

let mockAddToast: Mock;

vi.mock('../Toast/useToast', () => ({
  useToast: () => ({ addToast: mockAddToast }),
}));

const makeVm = (id: string, state: ComputeInstanceState) => ({
  id,
  status: { state },
  metadata: { name: `vm-${id}` },
  spec: {},
});

type CapturedUpdateRequest = {
  object?: { id?: string; status?: unknown };
  updateMask?: { paths?: string[] };
};

describe('useVmPowerAction', () => {
  const createSuccessTransport = (onUpdate?: (request: unknown) => void) =>
    createRouterTransport((router) => {
      router.service(ComputeInstances, {
        list: () => ({ items: [makeVm('vm-1', ComputeInstanceState.RUNNING)] }),
        get: () => ({ object: makeVm('vm-1', ComputeInstanceState.RUNNING) }),
        update: (request) => {
          onUpdate?.(request);
          return { object: makeVm('vm-1', ComputeInstanceState.STOPPING) };
        },
      });
    });

  const createFailureTransport = () =>
    createRouterTransport((router) => {
      router.service(ComputeInstances, {
        list: () => ({ items: [] }),
        get: () => ({ object: undefined }),
        update: () => {
          throw new ConnectError('power action denied', Code.PermissionDenied);
        },
      });
    });

  const renderWithTransport = (transport: ReturnType<typeof createRouterTransport>) => {
    const queryClient = new QueryClient({
      defaultOptions: {
        queries: { retry: false },
        mutations: { retry: false },
      },
    });
    const wrapper = ({ children }: { children: ReactNode }) =>
      createElement(
        ApiProvider,
        { transport } as React.ComponentProps<typeof ApiProvider>,
        createElement(QueryClientProvider, { client: queryClient }, children),
      );
    return { ...renderHook(() => useVmPowerAction(), { wrapper }), queryClient };
  };

  beforeEach(() => {
    mockAddToast = vi.fn();
  });

  it('shows a success toast after starting a VM', async () => {
    const { result } = renderWithTransport(createSuccessTransport());

    act(() => {
      result.current.runPowerAction('vm-1', 'my-vm', 'start');
    });

    await waitFor(() => expect(mockAddToast).toHaveBeenCalled());
    expect(mockAddToast).toHaveBeenCalledWith(
      expect.objectContaining({
        variant: 'success',
        title: expect.stringContaining('my-vm'),
      }),
    );
  });

  it('shows a success toast after stopping a VM', async () => {
    const { result } = renderWithTransport(createSuccessTransport());

    act(() => {
      result.current.runPowerAction('vm-1', 'my-vm', 'stop');
    });

    await waitFor(() => expect(mockAddToast).toHaveBeenCalled());
    expect(mockAddToast).toHaveBeenCalledWith(
      expect.objectContaining({
        variant: 'success',
        title: expect.stringContaining('my-vm'),
      }),
    );
  });

  it('shows a success toast after restarting a VM', async () => {
    const { result } = renderWithTransport(createSuccessTransport());

    act(() => {
      result.current.runPowerAction('vm-1', 'my-vm', 'restart');
    });

    await waitFor(() => expect(mockAddToast).toHaveBeenCalled());
    expect(mockAddToast).toHaveBeenCalledWith(
      expect.objectContaining({
        variant: 'success',
        title: expect.stringContaining('my-vm'),
      }),
    );
  });

  it('invalidates the VM list after a successful power action', async () => {
    const { result, queryClient } = renderWithTransport(createSuccessTransport());
    const listKey = [ComputeInstances.typeName, 'list', {}] as const;
    queryClient.setQueryData(listKey, []);

    act(() => {
      result.current.runPowerAction('vm-1', 'my-vm', 'start');
    });

    await waitFor(() => expect(queryClient.getQueryState(listKey)?.isInvalidated).toBe(true));
  });

  it('sends the generic update request for a stop action', async () => {
    let request: CapturedUpdateRequest | undefined;
    const { result } = renderWithTransport(
      createSuccessTransport((captured) => {
        request = captured as CapturedUpdateRequest;
      }),
    );

    act(() => {
      result.current.runPowerAction('vm-1', 'my-vm', 'stop');
    });

    await waitFor(() => expect(mockAddToast).toHaveBeenCalled());
    expect(request?.object?.id).toBe('vm-1');
    expect(request?.object?.status).toBeUndefined();
    expect(request?.updateMask?.paths).toEqual(['spec.run_strategy']);
  });

  it('sends the generic update request for a start action', async () => {
    let request: CapturedUpdateRequest | undefined;
    const { result } = renderWithTransport(
      createSuccessTransport((captured) => {
        request = captured as CapturedUpdateRequest;
      }),
    );

    act(() => {
      result.current.runPowerAction('vm-1', 'my-vm', 'start');
    });

    await waitFor(() => expect(mockAddToast).toHaveBeenCalled());
    expect(request?.object?.id).toBe('vm-1');
    expect(request?.object?.status).toBeUndefined();
    expect(request?.updateMask?.paths).toEqual(['spec.run_strategy']);
  });

  it('sends the generic update request for a restart action', async () => {
    let request: CapturedUpdateRequest | undefined;
    const { result } = renderWithTransport(
      createSuccessTransport((captured) => {
        request = captured as CapturedUpdateRequest;
      }),
    );

    act(() => {
      result.current.runPowerAction('vm-1', 'my-vm', 'restart');
    });

    await waitFor(() => expect(mockAddToast).toHaveBeenCalled());
    expect(request?.object?.id).toBe('vm-1');
    expect(request?.object?.status).toBeUndefined();
    expect(request?.updateMask?.paths).toEqual(['spec.restart_requested_at']);
  });

  it('shows a danger toast with instance name when a power action fails', async () => {
    const { result } = renderWithTransport(createFailureTransport());

    act(() => {
      result.current.runPowerAction('vm-1', 'my-vm', 'stop');
    });

    await waitFor(() => expect(mockAddToast).toHaveBeenCalled());
    expect(mockAddToast).toHaveBeenCalledWith(
      expect.objectContaining({
        variant: 'danger',
        title: expect.stringContaining('my-vm'),
      }),
    );
  });

  it('includes the action verb in the success toast title', async () => {
    const { result } = renderWithTransport(createSuccessTransport());

    act(() => {
      result.current.runPowerAction('vm-1', 'web-server', 'start');
    });

    await waitFor(() => expect(mockAddToast).toHaveBeenCalled());
    expect(mockAddToast).toHaveBeenCalledWith(
      expect.objectContaining({
        variant: 'success',
        title: expect.stringContaining('Start'),
      }),
    );
  });

  it('does not show a toast before any action is triggered', () => {
    renderWithTransport(createSuccessTransport());
    expect(mockAddToast).not.toHaveBeenCalled();
  });
});
