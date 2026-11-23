import React, { type ReactNode, createElement } from 'react';
import { MemoryRouter } from 'react-router-dom';
import { createRouterTransport } from '@connectrpc/connect';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { renderHook, waitFor } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import { type BareMetalInstance, BareMetalInstances, Capabilities } from '@osac/types';

import { useBareMetalInstances } from './baremetal-instance';
import { SessionProvider } from '../../hooks/use-session';
import { ApiProvider } from '../api-context';
import { type CelFilter } from '../cel';

const makeBmi = (id: string) => ({
  id,
  metadata: { name: `bmi-${id}` },
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
