import React, { type ReactNode, createElement } from 'react';
import { create } from '@bufbuild/protobuf';
import { Code, ConnectError, createRouterTransport } from '@connectrpc/connect';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, renderHook, waitFor } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import {
  type ClusterVersion,
  ClusterVersionReferenceSchema,
  ClusterVersionSchema,
  ClusterVersionState,
  ClusterVersions,
} from '@osac/types';

import {
  CLUSTER_VERSION_ACTIVE_LIST_FILTER,
  CLUSTER_VERSION_ALL_STATES_LIST_FILTER,
  clusterUpgradeCatalogFilter,
  clusterVersionNamesFilter,
  useClusterVersion,
  useClusterVersions,
  useConfiguredClusterVersion,
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

  it('scopes the upgrade catalog to the cluster tenant and shared versions', () => {
    expect(clusterUpgradeCatalogFilter('tenant-a')).toBe(
      `${CLUSTER_VERSION_ACTIVE_LIST_FILTER} && (this.metadata.tenant == "tenant-a" || this.metadata.tenant == "shared")`,
    );
    expect(clusterUpgradeCatalogFilter()).toBe(CLUSTER_VERSION_ACTIVE_LIST_FILTER);
  });

  it('references spec.state and spec.enabled so the all-states filter returns every state', () => {
    // Both fields must be referenced to defeat the public List RPC's default
    // hiding of obsolete/disabled versions (see cluster_versions_server.go).
    expect(CLUSTER_VERSION_ALL_STATES_LIST_FILTER).toContain('this.spec.state');
    expect(CLUSTER_VERSION_ALL_STATES_LIST_FILTER).toContain('this.spec.enabled');
    expect(CLUSTER_VERSION_ALL_STATES_LIST_FILTER).toContain(`${ClusterVersionState.OBSOLETE}`);
  });

  it('compares the enum spec.state with == and never list membership', () => {
    // The backend filter translator only resolves enum int literals to their stored
    // string name for ==/!=; `state in [...]` produces a text-vs-int SQL clause that
    // Postgres rejects with "failed to list". Guard against reintroducing it.
    expect(CLUSTER_VERSION_ALL_STATES_LIST_FILTER).toContain(
      `this.spec.state == ${ClusterVersionState.OBSOLETE}`,
    );
    expect(CLUSTER_VERSION_ALL_STATES_LIST_FILTER).not.toContain('this.spec.state in');
  });

  it('scopes the name filter to the given versions while keeping all-states predicates', () => {
    const filter = clusterVersionNamesFilter(['v4.17', 'v4.15']);

    // metadata.name is a string field, so `in [...]` is safe (unlike enum spec.state).
    expect(filter).toContain('this.metadata.name in ["v4.17", "v4.15"]');
    // All-states predicates remain so obsolete/disabled versions still resolve.
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
        onClusterVersionList: (req) => {
          capturedFilter = req.filter;
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

describe('useClusterVersion', () => {
  it('fetches a single version by id', async () => {
    const transport = createMockConnectTransport({
      clusterVersions: [makeClusterVersion('4-17-0')],
    });
    const { wrapper } = makeWrapper(transport);
    const { result } = renderHook(() => useClusterVersion('4-17-0'), { wrapper });

    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(result.current.data?.id).toBe('4-17-0');
    expect((await result.current.refetch()).data?.id).toBe('4-17-0');
  });

  it('is disabled when the id is falsy', () => {
    const transport = createMockConnectTransport({
      clusterVersions: [makeClusterVersion('4-17-0')],
    });
    const { wrapper } = makeWrapper(transport);
    const { result } = renderHook(() => useClusterVersion(undefined), { wrapper });

    expect(result.current.fetchStatus).toBe('idle');
    expect(result.current.data).toBeUndefined();
  });
});

describe('useConfiguredClusterVersion', () => {
  it('resolves a disabled obsolete configured version by ID', async () => {
    const configured = makeClusterVersion('4-15-0', ClusterVersionState.OBSOLETE, false);
    const transport = createMockConnectTransport({ clusterVersions: [configured] });
    const { wrapper } = makeWrapper(transport);
    const reference = create(ClusterVersionReferenceSchema, {
      id: configured.id,
      name: configured.metadata?.name,
    });
    const { result } = renderHook(() => useConfiguredClusterVersion(reference), { wrapper });

    await waitFor(() => expect(result.current.data?.id).toBe(configured.id));
    expect(result.current.isLoading).toBe(false);
    expect(result.current.isError).toBe(false);
  });

  it('forwards a configured reference ID unchanged', async () => {
    const configured = create(ClusterVersionSchema, {
      id: '  version-1  ',
      metadata: { name: 'current' },
      spec: { version: '4.15.0' },
    });
    let requestedId: string | undefined;
    const transport = createRouterTransport((router) => {
      router.service(ClusterVersions, {
        get: (request) => {
          requestedId = request.id;
          return { object: configured };
        },
      });
    });
    const { wrapper } = makeWrapper(transport);
    const reference = create(ClusterVersionReferenceSchema, {
      id: configured.id,
      name: configured.metadata?.name,
    });
    const { result } = renderHook(() => useConfiguredClusterVersion(reference), { wrapper });

    await waitFor(() => expect(result.current.data?.id).toBe(configured.id));
    expect(requestedId).toBe(configured.id);
  });

  it('uses a name-scoped all-states List for a name-only reference', async () => {
    const configured = makeClusterVersion('4-15-0', ClusterVersionState.OBSOLETE, false);
    let capturedFilter: string | undefined;
    const transport = createMockConnectTransport(
      {},
      {
        onClusterVersionList: (request) => {
          capturedFilter = request.filter;
          return { items: [configured] };
        },
      },
    );
    const { wrapper } = makeWrapper(transport);
    const reference = create(ClusterVersionReferenceSchema, { name: '4-15-0' });
    const { result } = renderHook(() => useConfiguredClusterVersion(reference), { wrapper });

    await waitFor(() => expect(result.current.data?.id).toBe(configured.id));
    expect(capturedFilter).toContain('this.metadata.name in ["4-15-0"]');
    expect(capturedFilter).toContain('this.metadata.project == ""');
    expect(capturedFilter).toContain('this.spec.state');
    expect(capturedFilter).toContain('this.spec.enabled');
    expect(capturedFilter).toContain(String(ClusterVersionState.OBSOLETE));
  });

  it('finds a name-only reference after earlier pages of other scopes', async () => {
    const configured = create(ClusterVersionSchema, {
      id: 'configured',
      metadata: { name: 'current', project: 'project-a' },
      spec: { version: '4.17.0', state: ClusterVersionState.ACTIVE, enabled: true },
    });
    const otherScopes = Array.from({ length: 100 }, (_, index) =>
      create(ClusterVersionSchema, {
        id: `other-${index}`,
        metadata: { name: 'current', project: `other-project-${index}` },
        spec: { version: '4.17.0', state: ClusterVersionState.ACTIVE, enabled: true },
      }),
    );
    const versions = [...otherScopes, configured];
    const offsets: number[] = [];
    const transport = createRouterTransport((router) => {
      router.service(ClusterVersions, {
        list: (request) => {
          const offset = request.offset ?? 0;
          offsets.push(offset);
          const items = versions.slice(offset, offset + (request.limit || 100));
          return { items, size: items.length, total: versions.length };
        },
      });
    });
    const { wrapper } = makeWrapper(transport);
    const reference = create(ClusterVersionReferenceSchema, {
      name: 'current',
      project: 'project-a',
    });
    const { result } = renderHook(() => useConfiguredClusterVersion(reference), { wrapper });

    await waitFor(() => expect(result.current.data?.id).toBe(configured.id));
    expect(offsets).toEqual([0, 100]);
  });

  it('resolves a name-only reference in the owning tenant when names overlap', async () => {
    const ownVersion = create(ClusterVersionSchema, {
      id: 'own-version',
      metadata: { name: 'current', project: 'project-a', tenant: 'tenant-a' },
      spec: { version: '4.17.0', state: ClusterVersionState.ACTIVE, enabled: true },
    });
    const otherVersion = create(ClusterVersionSchema, {
      id: 'other-version',
      metadata: { name: 'current', project: 'project-a', tenant: 'tenant-b' },
      spec: { version: '4.17.0', state: ClusterVersionState.ACTIVE, enabled: true },
    });
    let capturedFilter: string | undefined;
    const transport = createMockConnectTransport(
      {},
      {
        onClusterVersionList: (request) => {
          capturedFilter = request.filter;
          return { items: [otherVersion, ownVersion] };
        },
      },
    );
    const { wrapper } = makeWrapper(transport);
    const reference = create(ClusterVersionReferenceSchema, {
      name: 'current',
      project: 'project-a',
    });
    const { result } = renderHook(() => useConfiguredClusterVersion(reference, 'tenant-a'), {
      wrapper,
    });

    await waitFor(() => expect(result.current.data?.id).toBe(ownVersion.id));
    expect(capturedFilter).toContain('this.metadata.project == "project-a"');
    expect(capturedFilter).toContain('this.metadata.tenant == "tenant-a"');
  });

  it('returns unresolved for an absent, missing, mismatched or ambiguous reference', async () => {
    const configured = makeClusterVersion('4-15-0');
    const duplicate = create(ClusterVersionSchema, {
      id: 'other-id',
      metadata: { name: configured.metadata?.name },
      spec: configured.spec,
    });
    const transport = createMockConnectTransport(
      { clusterVersions: [configured] },
      { onClusterVersionList: () => ({ items: [configured, duplicate] }) },
    );
    const { wrapper } = makeWrapper(transport);
    const references = {
      absent: undefined,
      missing: create(ClusterVersionReferenceSchema, { id: 'missing' }),
      mismatched: create(ClusterVersionReferenceSchema, {
        id: configured.id,
        name: 'different-name',
      }),
      ambiguous: create(ClusterVersionReferenceSchema, { name: configured.metadata?.name }),
    };
    const { result } = renderHook(
      () => ({
        absent: useConfiguredClusterVersion(references.absent),
        missing: useConfiguredClusterVersion(references.missing),
        mismatched: useConfiguredClusterVersion(references.mismatched),
        ambiguous: useConfiguredClusterVersion(references.ambiguous),
      }),
      { wrapper },
    );

    await waitFor(() => expect(result.current.missing.isLoading).toBe(false));
    await waitFor(() => expect(result.current.mismatched.isLoading).toBe(false));
    await waitFor(() => expect(result.current.ambiguous.isLoading).toBe(false));
    for (const resolution of Object.values(result.current)) {
      expect(resolution.data).toBeUndefined();
      expect(resolution.isError).toBe(false);
    }
  });

  it('keeps a nonmatching project reference unresolved', async () => {
    const configured = create(ClusterVersionSchema, {
      id: 'version-1',
      metadata: { name: 'version-1', project: 'project-a' },
      spec: { version: '4.15.0', state: ClusterVersionState.ACTIVE, enabled: true },
    });
    const transport = createMockConnectTransport({ clusterVersions: [configured] });
    const { wrapper } = makeWrapper(transport);
    const reference = create(ClusterVersionReferenceSchema, {
      id: configured.id,
      name: configured.metadata?.name,
      project: 'project-b',
    });
    const { result } = renderHook(() => useConfiguredClusterVersion(reference), { wrapper });

    await waitFor(() => expect(result.current.isLoading).toBe(false));
    expect(result.current.data).toBeUndefined();
    expect(result.current.isError).toBe(false);
  });

  it.each([
    ['another project', { project: 'project-b' }],
    ['the shared tenant', { tenant: 'shared' }],
  ])('does not resolve a name-only reference in %s', async (_scope, metadata) => {
    const version = create(ClusterVersionSchema, {
      id: 'version-1',
      metadata: { name: 'current', ...metadata },
      spec: { version: '4.15.0', state: ClusterVersionState.ACTIVE, enabled: true },
    });
    const transport = createMockConnectTransport(
      {},
      { onClusterVersionList: () => ({ items: [version] }) },
    );
    const { wrapper } = makeWrapper(transport);
    const reference = create(ClusterVersionReferenceSchema, { name: 'current' });
    const { result } = renderHook(() => useConfiguredClusterVersion(reference), { wrapper });

    await waitFor(() => expect(result.current.isLoading).toBe(false));
    expect(result.current.data).toBeUndefined();
    expect(result.current.isError).toBe(false);
  });

  it.each([
    ['the named project', { project: 'project-b' }, { project: 'project-b' }],
    ['the shared tenant', { tenant: 'shared' }, { shared: true }],
  ])('resolves a name-only reference in %s', async (_scope, metadata, referenceScope) => {
    const version = create(ClusterVersionSchema, {
      id: 'version-1',
      metadata: { name: 'current', ...metadata },
      spec: { version: '4.15.0', state: ClusterVersionState.ACTIVE, enabled: true },
    });
    let capturedFilter: string | undefined;
    const transport = createMockConnectTransport(
      {},
      {
        onClusterVersionList: (request) => {
          capturedFilter = request.filter;
          return { items: [version] };
        },
      },
    );
    const { wrapper } = makeWrapper(transport);
    const reference = create(ClusterVersionReferenceSchema, {
      name: 'current',
      ...referenceScope,
    });
    const { result } = renderHook(() => useConfiguredClusterVersion(reference), { wrapper });

    await waitFor(() => expect(result.current.data?.id).toBe(version.id));
    expect(result.current.isError).toBe(false);
    expect(capturedFilter).toContain(`this.metadata.project == "${reference.project}"`);
    if (reference.shared) {
      expect(capturedFilter).toContain('this.metadata.tenant == "shared"');
    }
  });

  it('treats a NotFound response as unresolved and other transport errors as errors', async () => {
    const transport = createRouterTransport((router) => {
      router.service(ClusterVersions, {
        get: (request) => {
          if (request.id === 'missing') {
            throw new ConnectError('not found', Code.NotFound);
          }
          throw new ConnectError('temporarily unavailable', Code.Unavailable);
        },
      });
    });
    const { wrapper } = makeWrapper(transport);
    const { result } = renderHook(
      () => ({
        missing: useConfiguredClusterVersion(
          create(ClusterVersionReferenceSchema, { id: 'missing' }),
        ),
        unavailable: useConfiguredClusterVersion(
          create(ClusterVersionReferenceSchema, { id: 'unavailable' }),
        ),
      }),
      { wrapper },
    );

    await waitFor(() => expect(result.current.missing.isLoading).toBe(false));
    await waitFor(() => expect(result.current.unavailable.isLoading).toBe(false));
    expect(result.current.missing.data).toBeUndefined();
    expect(result.current.missing.isError).toBe(false);
    expect(result.current.unavailable.data).toBeUndefined();
    expect(result.current.unavailable.isError).toBe(true);
    expect(result.current.unavailable.error).toMatchObject({ code: Code.Unavailable });
  });

  it('keeps a pending lookup separate from an unresolved reference', async () => {
    const configured = makeClusterVersion('4-15-0');
    let release: (() => void) | undefined;
    const gate = new Promise<void>((resolve) => {
      release = resolve;
    });
    const transport = createRouterTransport((router) => {
      router.service(ClusterVersions, {
        get: async () => {
          await gate;
          return { object: configured };
        },
      });
    });
    const { wrapper } = makeWrapper(transport);
    const reference = create(ClusterVersionReferenceSchema, { id: configured.id });
    const { result } = renderHook(() => useConfiguredClusterVersion(reference), { wrapper });

    expect(result.current.isLoading).toBe(true);
    expect(result.current.data).toBeUndefined();
    act(() => release?.());
    await waitFor(() => expect(result.current.data?.id).toBe(configured.id));
  });

  it.each([
    ['ID', true],
    ['name', false],
  ] as const)('keeps a resolved version visible during a %s refetch', async (_label, byId) => {
    const configured = makeClusterVersion('4-15-0');
    let calls = 0;
    let releaseRefetch: (() => void) | undefined;
    const refetchGate = new Promise<void>((resolve) => {
      releaseRefetch = resolve;
    });
    const waitOnRefetch = async () => {
      calls += 1;
      if (calls === 2) {
        await refetchGate;
      }
    };
    const transport = createRouterTransport((router) => {
      router.service(ClusterVersions, {
        get: async () => {
          await waitOnRefetch();
          return { object: configured };
        },
        list: async () => {
          await waitOnRefetch();
          return { items: [configured] };
        },
      });
    });
    const { wrapper, queryClient } = makeWrapper(transport);
    const reference = create(
      ClusterVersionReferenceSchema,
      byId ? { id: configured.id } : { name: configured.metadata?.name },
    );
    const { result } = renderHook(() => useConfiguredClusterVersion(reference), { wrapper });

    await waitFor(() => expect(result.current.data?.id).toBe(configured.id));
    expect(result.current.isLoading).toBe(false);
    act(() => {
      void queryClient.invalidateQueries({ queryKey: [ClusterVersions.typeName] });
    });
    try {
      await waitFor(() => expect(calls).toBe(2));
      expect(queryClient.isFetching({ queryKey: [ClusterVersions.typeName] })).toBe(1);
      expect(result.current.isLoading).toBe(false);
      expect(result.current.data?.id).toBe(configured.id);
    } finally {
      act(() => releaseRefetch?.());
      await waitFor(() =>
        expect(queryClient.isFetching({ queryKey: [ClusterVersions.typeName] })).toBe(0),
      );
    }
  });

  it('does not use placeholder data from the previous name when the reference changes', async () => {
    const first = makeClusterVersion('4-15-0');
    const second = makeClusterVersion('4-16-0');
    let releaseSecond: (() => void) | undefined;
    const secondGate = new Promise<void>((resolve) => {
      releaseSecond = resolve;
    });
    const transport = createRouterTransport((router) => {
      router.service(ClusterVersions, {
        list: async (request) => {
          if (request.filter?.includes('4-16-0')) {
            await secondGate;
            return { items: [second] };
          }
          return { items: [first] };
        },
      });
    });
    const { wrapper } = makeWrapper(transport);
    const firstRef = create(ClusterVersionReferenceSchema, { name: '4-15-0' });
    const secondRef = create(ClusterVersionReferenceSchema, { name: '4-16-0' });
    const { result, rerender } = renderHook(
      ({ reference }) => useConfiguredClusterVersion(reference),
      { initialProps: { reference: firstRef }, wrapper },
    );

    await waitFor(() => expect(result.current.data?.id).toBe(first.id));
    rerender({ reference: secondRef });
    expect(result.current.isLoading).toBe(true);
    expect(result.current.data).toBeUndefined();

    act(() => releaseSecond?.());
    await waitFor(() => expect(result.current.data?.id).toBe(second.id));
  });
});
