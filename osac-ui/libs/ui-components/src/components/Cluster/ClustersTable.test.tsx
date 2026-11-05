import React, { type ReactNode, createElement } from 'react';
import { MemoryRouter } from 'react-router-dom';
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

import { ClustersTable } from './ClustersTable';
import { ApiProvider } from '../../api/api-context';
import { createMockConnectTransport } from '../../test-utils/createMockConnectTransport';

const makeCluster = (id: string, versionName: string): Cluster =>
  create(ClusterSchema, {
    id,
    metadata: { name: id },
    spec: { version: { name: versionName } },
  });

const makeClusterVersion = (
  name: string,
  version: string,
  state: ClusterVersionState,
  enabled = true,
): ClusterVersion =>
  create(ClusterVersionSchema, {
    id: name,
    metadata: { name },
    spec: { version, state, enabled },
  });

const clusterVersions = [
  makeClusterVersion('v4.17', '4.17.0', ClusterVersionState.ACTIVE),
  makeClusterVersion('v4.16', '4.16.0', ClusterVersionState.DEPRECATED),
  makeClusterVersion('v4.15', '4.15.0', ClusterVersionState.OBSOLETE),
  makeClusterVersion('v4.14', '4.14.0', ClusterVersionState.ACTIVE, false),
];

const renderTable = (
  clusters: Cluster[],
  versions: ClusterVersion[] = clusterVersions,
  showTenant = false,
) => {
  let callCount = 0;
  let capturedFilter: string | undefined;
  const transport = createMockConnectTransport(
    {},
    {
      onClusterVersionList: (req) => {
        callCount += 1;
        capturedFilter = req.filter;
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
      createElement(
        QueryClientProvider,
        { client: queryClient },
        createElement(MemoryRouter, null, children),
      ),
    );
  const result = render(<ClustersTable clusters={clusters} showTenant={showTenant} />, {
    wrapper,
  });
  return {
    ...result,
    getCallCount: () => callCount,
    getCapturedFilter: () => capturedFilter,
  };
};

describe('ClustersTable version join', () => {
  it('renders the resolved version string and lifecycle label for every state', async () => {
    renderTable([
      makeCluster('c-active', 'v4.17'),
      makeCluster('c-deprecated', 'v4.16'),
      makeCluster('c-obsolete', 'v4.15'),
      makeCluster('c-disabled', 'v4.14'),
    ]);

    expect(await screen.findByText('4.17.0')).toBeInTheDocument();
    expect(screen.getByText('4.16.0')).toBeInTheDocument();
    expect(screen.getByText('4.15.0')).toBeInTheDocument();
    expect(screen.getByText('4.14.0')).toBeInTheDocument();

    // Both the active and the disabled-but-active version render an Active label.
    expect(screen.getAllByText('Active')).toHaveLength(2);
    expect(screen.getByText('Deprecated')).toBeInTheDocument();
    expect(screen.getByText('Obsolete')).toBeInTheDocument();
  });

  it('issues a single scoped List referencing the version names plus all states', async () => {
    const { getCallCount, getCapturedFilter } = renderTable([
      makeCluster('c-active', 'v4.17'),
      makeCluster('c-obsolete', 'v4.15'),
    ]);

    expect(await screen.findByText('4.17.0')).toBeInTheDocument();
    expect(getCallCount()).toBe(1);
    const filter = getCapturedFilter() ?? '';
    expect(filter).toContain('this.metadata.name in ["v4.17", "v4.15"]');
    expect(filter).toContain('this.spec.state');
    expect(filter).toContain('this.spec.enabled');
    // Enum spec.state must never use list membership (see OSAC-4206).
    expect(filter).not.toContain('this.spec.state in');
  });

  it('does not issue a versions List when no cluster references a version', async () => {
    const { getCallCount } = renderTable([
      create(ClusterSchema, { id: 'c-none', metadata: { name: 'c-none' } }),
    ]);

    expect(await screen.findByText('c-none')).toBeInTheDocument();
    await waitFor(() => expect(getCallCount()).toBe(0));
  });

  it('falls back to the raw reference name with a blank lifecycle when unresolved', async () => {
    renderTable([makeCluster('c-missing', 'v-deleted')], []);

    expect(await screen.findByText('v-deleted')).toBeInTheDocument();
    const lifecycleCell = screen
      .getByText('v-deleted')
      .closest('tr')
      ?.querySelector('[data-label="Lifecycle"]');
    expect(lifecycleCell?.querySelector('.pf-v6-c-label')).toBeNull();
  });

  it('shows skeletons while cluster versions are loading', () => {
    const { container } = renderTable([makeCluster('c-active', 'v4.17')]);

    expect(container.querySelectorAll('.pf-v6-c-skeleton').length).toBeGreaterThan(0);
    expect(screen.queryByText('4.17.0')).toBeNull();
  });

  it("shows each cluster's tenant when asked, for cloud provider admins", async () => {
    renderTable(
      [create(ClusterSchema, { id: 'c-t', metadata: { name: 'c-t', tenant: 'tenant-1' } })],
      [],
      true,
    );

    expect(await screen.findByRole('columnheader', { name: 'Tenant' })).toBeInTheDocument();
    expect(screen.getByText('tenant-1')).toBeInTheDocument();
  });
});
