import React, { type ReactNode, createElement } from 'react';
import { create } from '@bufbuild/protobuf';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, renderHook, waitFor } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import {
  type ClusterVersion,
  ClusterVersionSchema,
  ClusterVersionState,
  ClusterVersions,
} from '@osac/types';

import {
  CLUSTER_VERSION_ACTIVE_LIST_FILTER,
  CLUSTER_VERSION_ALL_STATES_LIST_FILTER,
  clusterUpgradeCatalogFilter,
  clusterVersionNamesFilter,
  useClusterVersions,
} from './cluster-versions';
import { createMockConnectTransport } from '../../test-utils/createMockConnectTransport';
import { ApiProvider } from '../api-context';

const makeClusterVersion = (
  id: string,
  state: ClusterVersionState = ClusterVersionState.ACTIVE,
  enabled = true,
): ClusterVersion =>
  create(ClusterVersionSchema, {
    id,
    metadata: { name: id },
    spec: { version: id.replace(/-/g, '.'), state, enabled },
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

describe('cluster version list filters', () => {
  it('restricts the active filter to ACTIVE/DEPRECATED enabled versions', () => {
    expect(CLUSTER_VERSION_ACTIVE_LIST_FILTER).toBe(
      `(this.spec.state == ${ClusterVersionState.ACTIVE} || this.spec.state == ${ClusterVersionState.DEPRECATED}) && this.spec.enabled == true`,
    );
  });

  it('scopes upgrade catalog requests to the cluster tenant and shared versions', () => {
    const filter = clusterUpgradeCatalogFilter('tenant-a');

    expect(filter).toContain('this.metadata.tenant == "tenant-a"');
    expect(filter).toContain('this.metadata.tenant == "shared"');
    expect(filter).not.toContain('this.spec.state');
    expect(filter).not.toContain('this.spec.enabled');
  });

  it('references spec.state and spec.enabled so the all-states filter returns every state', () => {
    expect(CLUSTER_VERSION_ALL_STATES_LIST_FILTER).toContain('this.spec.state');
    expect(CLUSTER_VERSION_ALL_STATES_LIST_FILTER).toContain('this.spec.enabled');
    expect(CLUSTER_VERSION_ALL_STATES_LIST_FILTER).toContain(`${ClusterVersionState.OBSOLETE}`);
  });

  it('compares the enum spec.state with == and never list membership', () => {
    expect(CLUSTER_VERSION_ALL_STATES_LIST_FILTER).toContain(
      `this.spec.state == ${ClusterVersionState.OBSOLETE}`,
    );
    expect(CLUSTER_VERSION_ALL_STATES_LIST_FILTER).not.toContain('this.spec.state in');
  });

  it('scopes the name filter to the given versions while retaining all states', () => {
    const filter = clusterVersionNamesFilter(['v4.17', 'v4.15']);

    expect(filter).toContain('this.metadata.name in ["v4.17", "v4.15"]');
    expect(filter).toContain('this.spec.state');
    expect(filter).toContain('this.spec.enabled');
    expect(filter).toContain(`this.spec.state == ${ClusterVersionState.OBSOLETE}`);
    expect(filter).not.toContain('this.spec.state in');
  });
});

describe('useClusterVersions', () => {
  it('unwraps the list response into items', async () => {
    const transport = createMockConnectTransport({
      clusterVersions: [makeClusterVersion('4-17-0'), makeClusterVersion('4-16-0')],
    });
    const { wrapper } = makeWrapper(transport);
    const { result } = renderHook(() => useClusterVersions(), { wrapper });

    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(result.current.data?.map((item) => item.id)).toEqual(['4-17-0', '4-16-0']);
  });

  it('stays idle when disabled', () => {
    const transport = createMockConnectTransport({
      clusterVersions: [makeClusterVersion('4-17-0')],
    });
    const { wrapper } = makeWrapper(transport);
    const { result } = renderHook(() => useClusterVersions({}, { enabled: false }), {
      wrapper,
    });

    expect(result.current.fetchStatus).toBe('idle');
    expect(result.current.data).toBeUndefined();
  });

  it('refetches after service-key invalidation', async () => {
    let calls = 0;
    const transport = createMockConnectTransport(
      {},
      {
        onClusterVersionList: () => {
          calls += 1;
          return { items: [] };
        },
      },
    );
    const { wrapper, queryClient } = makeWrapper(transport);
    const { result } = renderHook(() => useClusterVersions(), { wrapper });

    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(calls).toBe(1);
    await act(async () => {
      await queryClient.invalidateQueries({ queryKey: [ClusterVersions.typeName] });
    });
    await waitFor(() => expect(calls).toBe(2));
  });

  it('passes the filter through to the list request', async () => {
    let capturedFilter: string | undefined;
    const transport = createMockConnectTransport(
      {},
      {
        onClusterVersionList: (request) => {
          capturedFilter = request.filter;
          return { items: [] };
        },
      },
    );
    const { wrapper } = makeWrapper(transport);
    const { result } = renderHook(
      () => useClusterVersions({ filter: CLUSTER_VERSION_ACTIVE_LIST_FILTER }),
      { wrapper },
    );

    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(capturedFilter).toBe(CLUSTER_VERSION_ACTIVE_LIST_FILTER);
  });
});
