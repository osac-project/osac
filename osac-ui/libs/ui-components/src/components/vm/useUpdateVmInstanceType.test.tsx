import React, { type ReactNode, createElement } from 'react';
import { initReactI18next } from 'react-i18next';
import { Code, ConnectError, createRouterTransport } from '@connectrpc/connect';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { renderHook, waitFor } from '@testing-library/react';
import i18n from 'i18next';
import { type Mock, beforeEach, describe, expect, it, vi } from 'vitest';

import {
  type ComputeInstance,
  ComputeInstanceState,
  ComputeInstances,
  type ComputeInstancesUpdateRequest,
} from '@osac/types';

import { useUpdateVmInstanceType } from './useUpdateVmInstanceType';
import { ApiProvider } from '../../api/api-context';

i18n.use(initReactI18next).init({
  initImmediate: false,
  lng: 'en',
  fallbackLng: 'en',
  resources: { en: { translation: {} } },
  interpolation: { escapeValue: false },
});

let mockAddToast: Mock;
let lastUpdate: ComputeInstancesUpdateRequest | undefined;

vi.mock('../Toast/useToast', () => ({
  useToast: () => ({ addToast: mockAddToast }),
}));

const vm = (state: ComputeInstanceState): ComputeInstance =>
  ({
    id: 'vm-1',
    metadata: { name: 'web-01' },
    status: { state },
    spec: { instanceType: { id: 'standard-4-8' } },
  }) as ComputeInstance;

const renderHookWith = (
  update: (request: ComputeInstancesUpdateRequest) => { warnings?: string[] },
) => {
  const transport = createRouterTransport((router) => {
    router.service(ComputeInstances, {
      update: (request) => {
        lastUpdate = request;
        return update(request);
      },
    });
  });
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  const wrapper = ({ children }: { children: ReactNode }) =>
    createElement(
      ApiProvider,
      { transport } as React.ComponentProps<typeof ApiProvider>,
      createElement(QueryClientProvider, { client: queryClient }, children),
    );
  return renderHook(() => useUpdateVmInstanceType(), { wrapper });
};

describe('useUpdateVmInstanceType', () => {
  beforeEach(() => {
    mockAddToast = vi.fn();
    lastUpdate = undefined;
  });

  it('sends an explicit spec.instance_type mask', async () => {
    const { result } = renderHookWith(() => ({ warnings: [] }));

    await result.current.updateInstanceType(
      vm(ComputeInstanceState.RUNNING),
      'standard-8-16',
      'Large',
    );

    expect(lastUpdate?.object?.id).toBe('vm-1');
    expect(lastUpdate?.object?.spec?.instanceType?.id).toBe('standard-8-16');
    expect(lastUpdate?.updateMask?.paths).toEqual(['spec.instance_type']);
  });

  it('toasts submitted copy for a running VM', async () => {
    const { result } = renderHookWith(() => ({ warnings: [] }));

    await result.current.updateInstanceType(
      vm(ComputeInstanceState.RUNNING),
      'standard-8-16',
      'Large',
    );

    await waitFor(() => expect(mockAddToast).toHaveBeenCalled());
    expect(mockAddToast).toHaveBeenCalledWith(
      expect.objectContaining({
        variant: 'success',
        title: 'Instance type change submitted for Large.',
      }),
    );
  });

  it('adds next-start copy when the VM is stopped', async () => {
    const { result } = renderHookWith(() => ({ warnings: [] }));

    await result.current.updateInstanceType(
      vm(ComputeInstanceState.STOPPED),
      'standard-8-16',
      'Large',
    );

    expect(mockAddToast).toHaveBeenCalledWith(
      expect.objectContaining({
        title: 'Instance type change submitted for Large. Resize will apply on next start.',
      }),
    );
  });

  it('includes response warnings in the toast description', async () => {
    const { result } = renderHookWith(() => ({ warnings: ['type is deprecated'] }));

    await result.current.updateInstanceType(
      vm(ComputeInstanceState.PAUSED),
      'standard-8-16',
      'Large',
    );

    expect(mockAddToast).toHaveBeenCalledWith(
      expect.objectContaining({ description: 'type is deprecated' }),
    );
  });

  it('rejects without a success toast when the update fails', async () => {
    const { result } = renderHookWith(() => {
      throw new ConnectError('cannot change GPU', Code.FailedPrecondition);
    });

    await expect(
      result.current.updateInstanceType(vm(ComputeInstanceState.RUNNING), 'gpu-1', 'GPU'),
    ).rejects.toThrow('cannot change GPU');
    expect(mockAddToast).not.toHaveBeenCalled();
  });
});
