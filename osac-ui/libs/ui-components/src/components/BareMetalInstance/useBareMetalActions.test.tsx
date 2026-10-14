/* eslint-disable @typescript-eslint/no-unsafe-assignment -- vi.mock breaks ESLint type resolution */
import React, { type ReactNode, createElement } from 'react';
import { initReactI18next } from 'react-i18next';
import { Code, ConnectError, createRouterTransport } from '@connectrpc/connect';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, renderHook, waitFor } from '@testing-library/react';
import i18n from 'i18next';
import { type Mock, beforeEach, describe, expect, it, vi } from 'vitest';

import type { BareMetalInstance } from '@osac/types';
import {
  BareMetalInstanceRunStrategy,
  BareMetalInstanceState,
  BareMetalInstances,
} from '@osac/types';

import { useBareMetalActions } from './useBareMetalActions';
import { ApiProvider } from '../../api/api-context';

i18n.use(initReactI18next).init({
  lng: 'en',
  fallbackLng: 'en',
  resources: { en: { translation: {} } },
  interpolation: { escapeValue: false },
});

let mockAddToast: Mock;

vi.mock('../Toast/useToast', () => ({
  useToast: () => ({ addToast: mockAddToast }),
}));

const makeBmi = (id: string, state: BareMetalInstanceState): BareMetalInstance =>
  ({
    id,
    metadata: { name: `bmi-${id}` },
    spec: {
      catalogItem: { id: 'catalog-1' },
      runStrategy: BareMetalInstanceRunStrategy.ALWAYS,
      restartTrigger: 0n,
    },
    status: { state },
  }) as unknown as BareMetalInstance;

describe('useBareMetalActions', () => {
  const createSuccessTransport = () =>
    createRouterTransport((router) => {
      router.service(BareMetalInstances, {
        list: () => ({ items: [makeBmi('bmi-1', BareMetalInstanceState.RUNNING)] }),
        get: () => ({ object: makeBmi('bmi-1', BareMetalInstanceState.RUNNING) }),
        update: () => ({ object: makeBmi('bmi-1', BareMetalInstanceState.STOPPING) }),
      });
    });

  const createFailureTransport = () =>
    createRouterTransport((router) => {
      router.service(BareMetalInstances, {
        list: () => ({ items: [] }),
        get: () => ({ object: undefined }),
        update: () => {
          throw new ConnectError('power action denied', Code.PermissionDenied);
        },
      });
    });

  const renderWithTransport = (
    transport: ReturnType<typeof createRouterTransport>,
    instance: BareMetalInstance,
  ) => {
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
    return renderHook(() => useBareMetalActions(instance), { wrapper });
  };

  beforeEach(() => {
    mockAddToast = vi.fn();
  });

  it('shows a success toast after starting a bare metal instance', async () => {
    const instance = makeBmi('bmi-1', BareMetalInstanceState.STOPPED);
    const { result } = renderWithTransport(createSuccessTransport(), instance);

    act(() => {
      result.current.start();
    });

    await waitFor(() => expect(mockAddToast).toHaveBeenCalled());
    expect(mockAddToast).toHaveBeenCalledWith(
      expect.objectContaining({
        variant: 'success',
        title: expect.stringContaining('bmi-bmi-1'),
      }),
    );
  });

  it('shows a success toast after stopping a bare metal instance', async () => {
    const instance = makeBmi('bmi-1', BareMetalInstanceState.RUNNING);
    const { result } = renderWithTransport(createSuccessTransport(), instance);

    act(() => {
      result.current.stop();
    });

    await waitFor(() => expect(mockAddToast).toHaveBeenCalled());
    expect(mockAddToast).toHaveBeenCalledWith(
      expect.objectContaining({
        variant: 'success',
        title: expect.stringContaining('bmi-bmi-1'),
      }),
    );
  });

  it('shows a success toast after restarting a bare metal instance', async () => {
    const instance = makeBmi('bmi-1', BareMetalInstanceState.RUNNING);
    const { result } = renderWithTransport(createSuccessTransport(), instance);

    act(() => {
      result.current.restart();
    });

    await waitFor(() => expect(mockAddToast).toHaveBeenCalled());
    expect(mockAddToast).toHaveBeenCalledWith(
      expect.objectContaining({
        variant: 'success',
        title: expect.stringContaining('bmi-bmi-1'),
      }),
    );
  });

  it('shows a danger toast with instance name when a power action fails', async () => {
    const instance = makeBmi('bmi-1', BareMetalInstanceState.RUNNING);
    const { result } = renderWithTransport(createFailureTransport(), instance);

    act(() => {
      result.current.stop();
    });

    await waitFor(() => expect(mockAddToast).toHaveBeenCalled());
    expect(mockAddToast).toHaveBeenCalledWith(
      expect.objectContaining({
        variant: 'danger',
        title: expect.stringContaining('bmi-bmi-1'),
      }),
    );
  });

  it('does not fire a mutation when the action is not allowed', async () => {
    const instance = makeBmi('bmi-1', BareMetalInstanceState.RUNNING);
    const { result } = renderWithTransport(createSuccessTransport(), instance);

    // canStart is false when state is RUNNING
    act(() => {
      result.current.start();
    });

    // Give time for any potential (undesired) async to settle
    await new Promise((r) => setTimeout(r, 50));
    expect(mockAddToast).not.toHaveBeenCalled();
  });

  it('includes the action verb in the success toast title', async () => {
    const instance = makeBmi('bmi-1', BareMetalInstanceState.RUNNING);
    const { result } = renderWithTransport(createSuccessTransport(), instance);

    act(() => {
      result.current.restart();
    });

    await waitFor(() => expect(mockAddToast).toHaveBeenCalled());
    expect(mockAddToast).toHaveBeenCalledWith(
      expect.objectContaining({
        variant: 'success',
        title: expect.stringContaining('Restart'),
      }),
    );
  });

  it('includes the error description in the danger toast', async () => {
    const instance = makeBmi('bmi-1', BareMetalInstanceState.RUNNING);
    const { result } = renderWithTransport(createFailureTransport(), instance);

    act(() => {
      result.current.stop();
    });

    await waitFor(() => expect(mockAddToast).toHaveBeenCalled());
    expect(mockAddToast).toHaveBeenCalledWith(
      expect.objectContaining({
        variant: 'danger',
        description: expect.any(String),
      }),
    );
  });
});
