/* eslint-disable @typescript-eslint/no-unsafe-assignment -- vi.mock breaks ESLint type resolution */
import React, { type ReactNode, createElement } from 'react';
import { initReactI18next } from 'react-i18next';
import { Code, ConnectError, createRouterTransport } from '@connectrpc/connect';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, renderHook, waitFor } from '@testing-library/react';
import i18n from 'i18next';
import { type Mock, beforeEach, describe, expect, it, vi } from 'vitest';

import type {
  BareMetalInstanceCatalogItem,
  BareMetalInstanceCatalogItemsUpdateRequest,
  ClusterCatalogItem,
  ClusterCatalogItemsUpdateRequest,
  ComputeInstanceCatalogItem,
  ComputeInstanceCatalogItemsUpdateRequest,
} from '@osac/types';
import {
  BareMetalInstanceCatalogItems,
  ClusterCatalogItems,
  ComputeInstanceCatalogItems,
} from '@osac/types';

import { useSetCatalogItemPublished } from './useSetCatalogItemPublished';
import { ApiProvider } from '../../../api/api-context';

i18n.use(initReactI18next).init({
  initImmediate: false,
  lng: 'en',
  fallbackLng: 'en',
  resources: { en: { translation: {} } },
  interpolation: { escapeValue: false },
});

let mockAddToast: Mock;

vi.mock('../../Toast/useToast', () => ({
  useToast: () => ({ addToast: mockAddToast }),
}));

const vmItem: ComputeInstanceCatalogItem = {
  $typeName: 'osac.public.v1.ComputeInstanceCatalogItem',
  id: 'catalog-1',
  published: false,
} as unknown as ComputeInstanceCatalogItem;

const clusterItem: ClusterCatalogItem = {
  $typeName: 'osac.public.v1.ClusterCatalogItem',
  id: 'catalog-2',
  published: true,
} as unknown as ClusterCatalogItem;

const bmItem: BareMetalInstanceCatalogItem = {
  $typeName: 'osac.public.v1.BareMetalInstanceCatalogItem',
  id: 'catalog-3',
  published: false,
} as unknown as BareMetalInstanceCatalogItem;

const renderWithTransport = (
  transport: ReturnType<typeof createRouterTransport>,
  item: ComputeInstanceCatalogItem | ClusterCatalogItem | BareMetalInstanceCatalogItem,
) => {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  const wrapper = ({ children }: { children: ReactNode }) =>
    createElement(
      ApiProvider,
      { transport } as React.ComponentProps<typeof ApiProvider>,
      createElement(QueryClientProvider, { client: queryClient }, children),
    );
  return renderHook(() => useSetCatalogItemPublished(item), { wrapper });
};

describe('useSetCatalogItemPublished', () => {
  beforeEach(() => {
    mockAddToast = vi.fn();
  });

  it('sends only id and published to the compute instance catalog item service', async () => {
    const update = vi.fn((_req: ComputeInstanceCatalogItemsUpdateRequest) => ({
      object: { ...vmItem, published: true },
    }));
    const transport = createRouterTransport((router) => {
      router.service(ComputeInstanceCatalogItems, { update });
    });
    const { result } = renderWithTransport(transport, vmItem);

    act(() => {
      result.current.setPublished(true);
    });

    await waitFor(() => expect(update).toHaveBeenCalled());
    const request = update.mock.calls[0][0];
    expect(request.object).toMatchObject({ id: 'catalog-1', published: true });
    expect(request.updateMask?.paths).toEqual(['id', 'published']);
  });

  it('sends only id and published to the cluster catalog item service', async () => {
    const update = vi.fn((_req: ClusterCatalogItemsUpdateRequest) => ({
      object: { ...clusterItem, published: false },
    }));
    const transport = createRouterTransport((router) => {
      router.service(ClusterCatalogItems, { update });
    });
    const { result } = renderWithTransport(transport, clusterItem);

    act(() => {
      result.current.setPublished(false);
    });

    await waitFor(() => expect(update).toHaveBeenCalled());
    const request = update.mock.calls[0][0];
    expect(request.object).toMatchObject({ id: 'catalog-2', published: false });
    expect(request.updateMask?.paths).toEqual(['id', 'published']);
  });

  it('sends only id and published to the bare metal catalog item service', async () => {
    const update = vi.fn((_req: BareMetalInstanceCatalogItemsUpdateRequest) => ({
      object: { ...bmItem, published: true },
    }));
    const transport = createRouterTransport((router) => {
      router.service(BareMetalInstanceCatalogItems, { update });
    });
    const { result } = renderWithTransport(transport, bmItem);

    act(() => {
      result.current.setPublished(true);
    });

    await waitFor(() => expect(update).toHaveBeenCalled());
    const request = update.mock.calls[0][0];
    expect(request.object).toMatchObject({ id: 'catalog-3', published: true });
    expect(request.updateMask?.paths).toEqual(['id', 'published']);
  });

  it('shows a danger toast and keeps the item unchanged when the update fails', async () => {
    const transport = createRouterTransport((router) => {
      router.service(ComputeInstanceCatalogItems, {
        update: () => {
          throw new ConnectError('permission denied', Code.PermissionDenied);
        },
      });
    });
    const { result } = renderWithTransport(transport, vmItem);

    act(() => {
      result.current.setPublished(true);
    });

    await waitFor(() => expect(mockAddToast).toHaveBeenCalled());
    expect(mockAddToast).toHaveBeenCalledWith(
      expect.objectContaining({ variant: 'danger', description: expect.any(String) }),
    );
  });
});
