import { create } from '@bufbuild/protobuf';
import { type Transport, createRouterTransport } from '@connectrpc/connect';
import { screen, waitFor } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import {
  Capabilities,
  type Cluster,
  ClusterSchema,
  ClusterVersionSchema,
  ClusterVersionState,
  ClusterVersions,
  Clusters,
  ProjectSchema,
  ProjectState,
  Projects,
} from '@osac/types';
import { cel } from '@osac/ui-components/api/cel';
import { CLUSTER_VERSION_ALL_STATES_LIST_FILTER } from '@osac/ui-components/api/v1/cluster-versions';

import { ClustersPage } from './ClustersPage';
import { buildClusterListFilter } from './ListPage/cluster-list-filter';
import { SessionProvider } from '../../hooks/use-session';
import { wrapWithAuthInterceptor } from '../../test-utils/createMockConnectTransport';
import { renderWithProviders } from '../../test-utils/TestProviders';

const renderClustersPage = (transport: Transport, routerEntries?: string[]) =>
  renderWithProviders(
    <SessionProvider role="tenant-user" username="test-user" tenantId="test-tenant">
      <ClustersPage />
    </SessionProvider>,
    { transport, routerEntries },
  );

const makeCluster = (id: string, versionName: string): Cluster =>
  create(ClusterSchema, {
    id,
    metadata: { name: id },
    spec: { version: { id: versionName, name: versionName } },
    status: { observedVersion: versionName === 'v4.15' ? '4.15.0' : '4.17.0' },
  });

const defaultProject = create(ProjectSchema, {
  id: 'p-default',
  metadata: { name: 'default' },
  spec: { title: 'Default' },
  status: { state: ProjectState.ACTIVE },
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

const createTransport = (
  clusters: Cluster[],
  versions = clusterVersions,
  versionListGate?: Promise<void>,
) => {
  let clusterListFilter: string | undefined;
  let clusterListRequests = 0;
  const capturedVersionFilters: string[] = [];

  const transport = wrapWithAuthInterceptor(
    createRouterTransport((router) => {
      router.service(Capabilities, {
        get: () => ({ enabledServices: [] }),
      });
      router.service(Clusters, {
        list: (req) => {
          clusterListRequests += 1;
          if (req.limit === 0) {
            return { items: [], total: clusters.length };
          }
          clusterListFilter = req.filter;
          return { items: clusters, total: clusters.length };
        },
        get: ({ id }) => ({ object: clusters.find((cluster) => cluster.id === id) }),
        create: (req) => ({ object: { id: 'cluster-new', ...req.object } }),
      });
      router.service(ClusterVersions, {
        list: async (req) => {
          await versionListGate;
          capturedVersionFilters.push(req.filter ?? '');
          if (
            req.filter?.includes('this.metadata.name in') ||
            req.filter === CLUSTER_VERSION_ALL_STATES_LIST_FILTER
          ) {
            const offset = req.offset ?? 0;
            return {
              items: req.limit ? versions.slice(offset, offset + req.limit) : versions,
              total: versions.length,
            };
          }
          return {
            items: versions.filter(
              (version) =>
                version.spec?.state === ClusterVersionState.ACTIVE && version.spec?.enabled,
            ),
            total: 1,
          };
        },
        get: ({ id }) => ({
          object: versions.find((version) => version.id === id) ?? versions[0],
        }),
      });
      router.service(Projects, {
        list: () => ({
          items: [defaultProject],
          total: 1,
        }),
      });
    }),
  );

  return {
    transport,
    getClusterListFilter: () => clusterListFilter,
    getClusterListRequests: () => clusterListRequests,
    getCapturedVersionFilters: () => capturedVersionFilters,
  };
};

describe('ClustersPage', () => {
  it('sends a CEL filter when filtering clusters by status', async () => {
    const { transport, getClusterListFilter } = createTransport([makeCluster('c-active', 'v4.17')]);
    const { user } = renderClustersPage(transport);

    await screen.findByText('OpenShift 4.17.0');

    await user.click(screen.getByRole('button', { name: 'Filter clusters by status' }));
    await user.click(screen.getByRole('option', { name: 'Ready' }));

    const expected = buildClusterListFilter({ status: 'ready' });

    await waitFor(() => {
      expect(getClusterListFilter()).toBe(expected);
    });
  });

  it('applies a catalog name from the URL to the observed version', async () => {
    const { transport, getClusterListFilter } = createTransport([
      makeCluster('c-obsolete', 'v4.15'),
      makeCluster('c-active', 'v4.17'),
    ]);
    const expected = buildClusterListFilter({ observedVersions: ['4.15.0'] });

    renderClustersPage(transport, ['/?clusterVersion=v4.15']);

    await waitFor(() => {
      expect(getClusterListFilter()).toBe(expected);
    });
  });

  it('loads clusters while selected version metadata is pending', async () => {
    let releaseVersions = () => {};
    const versionListGate = new Promise<void>((resolve) => {
      releaseVersions = resolve;
    });
    const { transport, getClusterListRequests, getClusterListFilter } = createTransport(
      [makeCluster('c-active', 'v4.17')],
      clusterVersions,
      versionListGate,
    );

    renderClustersPage(transport, ['/?clusterVersion=v4.17']);

    await waitFor(() => expect(getClusterListRequests()).toBeGreaterThan(0));
    releaseVersions();
    await waitFor(() =>
      expect(getClusterListFilter()).toBe(buildClusterListFilter({ observedVersions: ['4.17.0'] })),
    );
  });

  it('resolves a selected version beyond the first catalog page', async () => {
    const versions = Array.from({ length: 101 }, (_, index) =>
      create(ClusterVersionSchema, {
        id: `v4.${index}`,
        metadata: { name: `v4.${index}` },
        spec: { version: `4.${index}.0`, state: ClusterVersionState.ACTIVE, enabled: true },
      }),
    );
    const { transport, getClusterListFilter, getCapturedVersionFilters } = createTransport(
      [makeCluster('c-active', 'v4.17')],
      versions,
    );

    renderClustersPage(transport, ['/?clusterVersion=v4.100']);

    await waitFor(() => {
      expect(getClusterListFilter()).toBe('this.status.observed_version in ["4.100.0"]');
    });
    expect(getCapturedVersionFilters()).toEqual([
      CLUSTER_VERSION_ALL_STATES_LIST_FILTER,
      CLUSTER_VERSION_ALL_STATES_LIST_FILTER,
    ]);
  });

  it('sends a CEL filter when filtering clusters by cluster version', async () => {
    const { transport, getClusterListFilter } = createTransport([makeCluster('c-active', 'v4.17')]);
    const { user } = renderClustersPage(transport);

    await screen.findByText('OpenShift 4.17.0');

    await user.click(screen.getByRole('button', { name: 'Filter by cluster version' }));
    await user.click(screen.getByRole('checkbox', { name: 'OpenShift 4.17.0' }));

    const expected = buildClusterListFilter({ observedVersions: ['4.17.0'] });

    await waitFor(() => {
      expect(getClusterListFilter()).toBe(expected);
    });
  });

  it('sends a CEL filter when filtering clusters by search', async () => {
    const { transport, getClusterListFilter } = createTransport([makeCluster('c-active', 'v4.17')]);
    const { user } = renderClustersPage(transport);

    await screen.findByText('OpenShift 4.17.0');

    const searchInput = screen.getByRole('textbox', { name: 'Filter clusters by name' });
    await user.type(searchInput, 'c-active');

    const expected = buildClusterListFilter({ search: 'c-active' });

    await waitFor(() => {
      expect(getClusterListFilter()).toBe(expected);
    });
  });

  it('joins lifecycle metadata by observed semver instead of desired catalog reference', async () => {
    const desiredActive = makeCluster('c-mismatch', 'v4.17');
    if (!desiredActive.status) {
      throw new Error('Expected status');
    }
    desiredActive.status.observedVersion = '4.15.0';
    const { transport } = createTransport([desiredActive]);
    renderClustersPage(transport);

    expect(await screen.findByText('OpenShift 4.15.0')).toBeInTheDocument();
    expect(screen.getByText('Obsolete')).toBeInTheDocument();
    expect(screen.queryByText('Active')).not.toBeInTheDocument();
  });

  it('filters on the observed version while an upgrade targets another version', async () => {
    const upgrading = makeCluster('c-upgrading', 'v4.17');
    if (!upgrading.status) {
      throw new Error('Expected status');
    }
    upgrading.status.observedVersion = '4.15.0';
    const { transport, getClusterListFilter } = createTransport([upgrading]);
    renderClustersPage(transport, ['/?clusterVersion=v4.15']);

    await screen.findByText('c-upgrading');
    expect(screen.getAllByText('OpenShift 4.15.0')).toHaveLength(2);
    await waitFor(() => {
      expect(getClusterListFilter()).toBe('this.status.observed_version in ["4.15.0"]');
    });
  });

  it('clears project, status, cluster version, and search filters together', async () => {
    const { transport, getClusterListFilter } = createTransport([makeCluster('c-active', 'v4.17')]);
    const criteriaFilter = buildClusterListFilter({
      search: 'nomatch',
      status: 'ready',
      observedVersions: ['4.17.0'],
    });
    const expected = cel<Cluster>((builder) =>
      builder.and(
        cel<Cluster>((filter) => filter.field('metadata.project').isIn(['default'])),
        criteriaFilter,
      ),
    );

    const { user } = renderClustersPage(transport, [
      '/?project=default&status=ready&clusterVersion=v4.17&search=nomatch',
    ]);

    await waitFor(() => {
      expect(getClusterListFilter()).toBe(expected);
    });

    expect(screen.getByRole('button', { name: /Project:\s*Default/ })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Filter clusters by status' })).toHaveTextContent(
      'Ready',
    );
    await waitFor(() => {
      expect(screen.getByRole('button', { name: 'Filter by cluster version' })).toHaveTextContent(
        'OpenShift 4.17.0',
      );
    });
    expect(screen.getByRole('textbox', { name: 'Filter clusters by name' })).toHaveValue('nomatch');

    await user.click(screen.getByRole('button', { name: 'Clear all filters' }));

    await waitFor(() => {
      expect(getClusterListFilter()).toBeUndefined();
    });

    expect(screen.getByRole('button', { name: /Project:\s*All projects/ })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Filter clusters by status' })).toHaveTextContent(
      'All statuses',
    );
    expect(screen.getByRole('button', { name: 'Filter by cluster version' })).toHaveTextContent(
      'All cluster versions',
    );
    expect(screen.getByRole('textbox', { name: 'Filter clusters by name' })).toHaveValue('');
  });
});
