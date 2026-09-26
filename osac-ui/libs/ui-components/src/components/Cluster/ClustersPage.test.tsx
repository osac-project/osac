import React, { type ReactNode, createElement } from 'react';
import { MemoryRouter } from 'react-router-dom';
import { create } from '@bufbuild/protobuf';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen, waitFor } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import {
  type Cluster,
  ClusterSchema,
  ClusterVersionSchema,
  ClusterVersionState,
} from '@osac/types';

import { ClustersPage } from './ClustersPage';
import { ApiProvider } from '../../api/api-context';
import { SessionProvider } from '../../hooks/use-session';
import { createMockConnectTransport } from '../../test-utils/createMockConnectTransport';

const makeCluster = (id: string, versionName: string): Cluster =>
  create(ClusterSchema, {
    id,
    metadata: { name: id },
    spec: { version: { name: versionName } },
  });

const clusterVersions = [
  create(ClusterVersionSchema, {
    id: 'v4.17',
    metadata: { name: 'v4.17' },
    spec: { version: '4.17.0', state: ClusterVersionState.ACTIVE, enabled: true },
  }),
  create(ClusterVersionSchema, {
    id: 'v4.15',
    metadata: { name: 'v4.15' },
    spec: { version: '4.15.0', state: ClusterVersionState.OBSOLETE, enabled: false },
  }),
];

const renderPage = (clusters: Cluster[]) => {
  let callCount = 0;
  let capturedFilter: string | undefined;
  const transport = createMockConnectTransport(
    { clusters },
    {
      onClusterVersionList: (req) => {
        callCount += 1;
        capturedFilter = req.filter;
        return { items: clusterVersions };
      },
    },
  );
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
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
  render(<ClustersPage />, { wrapper });
  return {
    getCallCount: () => callCount,
    getCapturedFilter: () => capturedFilter,
  };
};

describe('ClustersPage version join', () => {
  it('resolves versions with exactly one scoped List call for multiple clusters', async () => {
    const { getCallCount, getCapturedFilter } = renderPage([
      makeCluster('c-active', 'v4.17'),
      makeCluster('c-obsolete', 'v4.15'),
    ]);

    // Obsolete version resolves from the same single List call.
    expect(await screen.findByText('4.15.0')).toBeInTheDocument();
    expect(screen.getByText('4.17.0')).toBeInTheDocument();
    expect(screen.getByText('Obsolete')).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'Create cluster' })).toHaveAttribute(
      'href',
      '/clusters/create',
    );

    expect(getCallCount()).toBe(1);
    const filter = getCapturedFilter() ?? '';
    expect(filter).toContain('this.metadata.name in');
    expect(filter).toContain('this.spec.state');
    expect(filter).toContain('this.spec.enabled');
  });

  it('does not fetch versions when there are no clusters', async () => {
    const { getCallCount } = renderPage([]);

    expect(await screen.findByText('No clusters found')).toBeInTheDocument();
    await waitFor(() => expect(getCallCount()).toBe(0));
  });
});
