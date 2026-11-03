import React, { type ReactNode, createElement } from 'react';
import { create } from '@bufbuild/protobuf';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen, waitFor } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import {
  type Cluster,
  ClusterSchema,
  type ClusterVersion,
  ClusterVersionSchema,
  ClusterVersionState,
} from '@osac/types';

import { ClusterConfigurationCard } from './ClusterConfigurationCard';
import { ApiProvider } from '../../../api/api-context';
import { createMockConnectTransport } from '../../../test-utils/createMockConnectTransport';

const obsoleteVersion = create(ClusterVersionSchema, {
  id: 'v4.17',
  metadata: { name: 'v4.17' },
  spec: { version: '4.17.0', state: ClusterVersionState.OBSOLETE, enabled: false },
});

const desiredVersion = create(ClusterVersionSchema, {
  id: 'v4.18',
  metadata: { name: 'v4.18' },
  spec: { version: '4.18.0', state: ClusterVersionState.ACTIVE, enabled: true },
});

const renderCard = (
  cluster: Cluster,
  versions: ClusterVersion[] = [obsoleteVersion, desiredVersion],
) => {
  const listFilters: (string | undefined)[] = [];
  const transport = createMockConnectTransport(
    { clusterVersions: versions },
    {
      onClusterVersionList: (request) => {
        listFilters.push(request.filter);
        return { items: versions };
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
      createElement(QueryClientProvider, { client: queryClient }, children),
    );
  return {
    ...render(<ClusterConfigurationCard cluster={cluster} />, { wrapper }),
    listFilters,
    queryClient,
  };
};

describe('ClusterConfigurationCard current version', () => {
  it('shows the observed version and its lifecycle when the desired target differs', async () => {
    const { listFilters } = renderCard(
      create(ClusterSchema, {
        id: 'cl-1',
        spec: { version: { id: 'v4.18', name: 'v4.18' } },
        status: { observedVersion: '4.17.0' },
      }),
    );

    const versionRow = screen
      .getByText('Current version')
      .closest('.pf-v6-c-description-list__group');
    expect(versionRow).toHaveTextContent('4.17.0');
    expect(screen.queryByText('4.18.0')).not.toBeInTheDocument();
    expect(await screen.findByText('Obsolete')).toBeInTheDocument();
    expect(listFilters).toHaveLength(1);
    expect(listFilters[0]).toContain('this.spec.version == "4.17.0"');
    expect(listFilters[0]).toContain('this.spec.state');
    expect(listFilters[0]).toContain('this.spec.enabled');
  });

  it('shows an unknown current version when the observation is missing', () => {
    const { listFilters } = renderCard(
      create(ClusterSchema, {
        id: 'cl-2',
        spec: { version: { id: 'v4.18', name: 'v4.18' } },
      }),
    );

    const versionRow = screen
      .getByText('Current version')
      .closest('.pf-v6-c-description-list__group');
    expect(versionRow).toHaveTextContent('—');
    expect(versionRow?.querySelector('.pf-v6-c-label')).toBeNull();
    expect(screen.queryByText('4.18.0')).not.toBeInTheDocument();
    expect(listFilters).toHaveLength(0);
  });

  it('shows an unmatched observation without a lifecycle badge', async () => {
    const { listFilters, queryClient } = renderCard(
      create(ClusterSchema, {
        id: 'cl-3',
        spec: { version: { id: 'v4.18', name: 'v4.18' } },
        status: { observedVersion: '4.16.0' },
      }),
      [desiredVersion],
    );

    await waitFor(() => expect(listFilters).toHaveLength(1));
    await waitFor(() => expect(queryClient.isFetching()).toBe(0));
    const versionRow = screen
      .getByText('Current version')
      .closest('.pf-v6-c-description-list__group');
    expect(versionRow).toHaveTextContent('4.16.0');
    expect(versionRow?.querySelector('.pf-v6-c-label')).toBeNull();
  });

  it('updates the current version and badge when the observation advances', async () => {
    const cluster = create(ClusterSchema, {
      id: 'cl-4',
      spec: { version: { id: 'v4.18', name: 'v4.18' } },
      status: { observedVersion: '4.17.0' },
    });
    const { rerender } = renderCard(cluster);
    expect(await screen.findByText('Obsolete')).toBeInTheDocument();

    rerender(
      <ClusterConfigurationCard
        cluster={create(ClusterSchema, {
          id: 'cl-4',
          spec: { version: { id: 'v4.18', name: 'v4.18' } },
          status: { observedVersion: '4.18.0' },
        })}
      />,
    );

    expect(screen.getByText('4.18.0')).toBeInTheDocument();
    expect(screen.queryByText('4.17.0')).not.toBeInTheDocument();
    expect(await screen.findByText('Active')).toBeInTheDocument();
  });
});
