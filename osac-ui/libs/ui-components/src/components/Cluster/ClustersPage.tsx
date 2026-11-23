import { useCallback, useMemo, useState } from 'react';
import { useSearchParams } from 'react-router-dom';
import {
  Button,
  EmptyState,
  EmptyStateBody,
  Gallery,
  GalleryItem,
  Stack,
  StackItem,
  Toolbar,
  ToolbarContent,
  ToolbarGroup,
  ToolbarItem,
} from '@patternfly/react-core';

import { type Cluster, type ClusterVersion, ClusterVersions, Clusters } from '@osac/types';
import { type CelFilter } from '@osac/ui-components/api/cel';
import { useListAllResources } from '@osac/ui-components/api/use-resource';
import { useClusters } from '@osac/ui-components/api/v1/cluster';
import { CLUSTER_VERSION_ALL_STATES_LIST_FILTER } from '@osac/ui-components/api/v1/cluster-versions';
import { buildClusterListFilter } from '@osac/ui-components/components/Cluster/ListPage/cluster-list-filter';
import ClusterCard from '@osac/ui-components/components/Cluster/ListPage/ClusterCard';
import ClusterStatusFilter from '@osac/ui-components/components/Cluster/ListPage/ClusterStatusFilter';
import ClusterVersionFilter from '@osac/ui-components/components/Cluster/ListPage/ClusterVersionFilter';
import ListPage from '@osac/ui-components/components/Page/ListPage';
import ListPageBody from '@osac/ui-components/components/Page/ListPageBody';
import ListPageToolbar from '@osac/ui-components/components/Page/ListPageToolbar';
import ProjectFilter from '@osac/ui-components/components/Page/ProjectFilter';
import CreateButton from '@osac/ui-components/components/Primitives/CreateButton.tsx';
import DebounceSearchInput from '@osac/ui-components/components/Primitives/DebounceSearchInput';
import ViewSwitcher from '@osac/ui-components/components/Primitives/ViewSwitcher';
import {
  SEARCH_PARAM,
  useArrayPageFilter,
  usePageFilter,
} from '@osac/ui-components/hooks/use-page-filter';
import { PROJECT_FILTER_PARAM, useSession } from '@osac/ui-components/hooks/use-session';
import { useViewType } from '@osac/ui-components/hooks/use-view-type';
import { useTranslation } from '@osac/ui-components/hooks/useTranslation';

import { isClusterSpecFilterValue, isClusterStatusFilter } from './ListPage/clusterDisplay';
import { ClustersTable } from './ListPage/ClustersTable';

const STATUS_FILTER_PARAM = 'status';
const CLUSTER_VERSION_FILTER_PARAM = 'clusterVersion';

const CLUSTERS_LIST_VIEW_KEY = 'clusters-list';

export const ClustersPage = () => {
  const { t } = useTranslation();
  const [, setSearchParams] = useSearchParams();
  const { projects, setProjects } = useSession();
  const [search, setSearch] = usePageFilter(SEARCH_PARAM);
  const [statusFilterParam, setStatusFilterParam] = usePageFilter(STATUS_FILTER_PARAM);
  const statusFilter = isClusterStatusFilter(statusFilterParam) ? statusFilterParam : undefined;
  const [versionFilterParams, toggleVersionFilter, clearVersionFilter] = useArrayPageFilter(
    CLUSTER_VERSION_FILTER_PARAM,
    isClusterSpecFilterValue,
  );
  const [viewType] = useViewType(CLUSTERS_LIST_VIEW_KEY);
  const [searchResetKey, setSearchResetKey] = useState(0);
  const hasVersionFilter = versionFilterParams.length > 0;

  const {
    data: clusterVersionReply,
    error: versionLoadError,
    isLoading: versionsLoading,
  } = useListAllResources(ClusterVersions, CLUSTER_VERSION_ALL_STATES_LIST_FILTER);
  const clusterVersions = useMemo(() => clusterVersionReply?.items ?? [], [clusterVersionReply]);
  const observedVersions = useMemo(() => {
    if (!hasVersionFilter) {
      return undefined;
    }
    const versions: string[] = [];
    for (const name of versionFilterParams) {
      const version = clusterVersions.find((item) => item.metadata?.name === name)?.spec?.version;
      if (version) {
        versions.push(version);
      }
    }
    return versions;
  }, [clusterVersions, hasVersionFilter, versionFilterParams]);

  const listFilter = useMemo(
    (): CelFilter<Cluster> | undefined =>
      buildClusterListFilter({
        search,
        status: statusFilter,
        observedVersions,
      }),
    [observedVersions, search, statusFilter],
  );

  const {
    clusters,
    totalItems,
    isLoading: clustersLoading,
    error: clusterError,
  } = useClusters(listFilter);
  const pageError = clusterError || (hasVersionFilter ? versionLoadError : undefined);
  const pageLoading = clustersLoading || (hasVersionFilter && versionsLoading);

  const clusterVersionBySemver = useMemo(() => {
    const versionsBySemver = new Map<string, ClusterVersion>();
    for (const clusterVersion of clusterVersions) {
      const semver = clusterVersion.spec?.version;
      if (semver) {
        versionsBySemver.set(semver, clusterVersion);
      }
    }
    return versionsBySemver;
  }, [clusterVersions]);

  const resolveClusterVersion = useCallback(
    (cluster: Cluster): ClusterVersion | undefined => {
      const observedVersion = cluster.status?.observedVersion;
      if (!observedVersion) {
        return undefined;
      }
      return clusterVersionBySemver.get(observedVersion);
    },
    [clusterVersionBySemver],
  );

  const isFiltered = Boolean(search || projects.length || statusFilter || hasVersionFilter);
  const showEmptyState = !pageLoading && !pageError && clusters.length === 0;

  const clearAllFilters = () => {
    setProjects([]);
    setSearchResetKey((key) => key + 1);
    requestAnimationFrame(() => {
      setSearchParams(
        (prev) => {
          const next = new URLSearchParams(prev);
          next.delete(SEARCH_PARAM);
          next.delete(PROJECT_FILTER_PARAM);
          next.delete(STATUS_FILTER_PARAM);
          next.delete(CLUSTER_VERSION_FILTER_PARAM);
          return next;
        },
        { replace: true },
      );
    });
  };

  const emptyState =
    totalItems === 0 && !isFiltered ? (
      <EmptyState titleText={t('No clusters found')} headingLevel="h2">
        <EmptyStateBody>
          {t('No clusters are provisioned for your organization yet.')}
        </EmptyStateBody>
      </EmptyState>
    ) : (
      <EmptyState titleText={t('No clusters match your filters')} headingLevel="h2">
        <EmptyStateBody>
          {t('Try a different project, status, cluster version, or search term.')}{' '}
          <Button variant="link" isInline onClick={clearAllFilters}>
            {t('Clear all filters')}
          </Button>
        </EmptyStateBody>
      </EmptyState>
    );

  const filteredText = !isFiltered
    ? t('{{count}} cluster', { count: totalItems })
    : t('{{shown}} of {{count}} cluster', {
        shown: clusters.length,
        count: totalItems,
      });

  return (
    <ListPage
      title={t('Clusters')}
      description={t('OpenShift clusters provisioned for your organization.')}
      error={pageError}
      actions={
        <CreateButton service={Clusters} to="/clusters/create">
          {t('Create cluster')}
        </CreateButton>
      }
    >
      <Stack hasGutter>
        <StackItem>
          <ListPageToolbar
            filteredText={
              !pageLoading && !pageError && clusters.length > 0 ? filteredText : undefined
            }
            isFiltered={isFiltered}
            onClearAllFilters={clearAllFilters}
          >
            <Toolbar>
              <ToolbarContent>
                <ToolbarItem>
                  <ProjectFilter />
                </ToolbarItem>
                <ToolbarItem>
                  <ClusterStatusFilter
                    selected={statusFilter}
                    onChange={(value) => setStatusFilterParam(value ?? '')}
                  />
                </ToolbarItem>
                <ToolbarItem>
                  <ClusterVersionFilter
                    clusterVersions={clusterVersions}
                    selected={versionFilterParams}
                    onToggle={toggleVersionFilter}
                    onClear={clearVersionFilter}
                    loadError={versionLoadError}
                  />
                </ToolbarItem>
                <ToolbarItem>
                  <DebounceSearchInput
                    placeholder={t('Search by name')}
                    value={search}
                    onChange={setSearch}
                    resetKey={searchResetKey}
                    aria-label={t('Filter clusters by name')}
                  />
                </ToolbarItem>
                <ToolbarGroup align={{ default: 'alignEnd' }}>
                  <ToolbarItem>
                    <ViewSwitcher pageKey={CLUSTERS_LIST_VIEW_KEY} />
                  </ToolbarItem>
                </ToolbarGroup>
              </ToolbarContent>
            </Toolbar>
          </ListPageToolbar>
        </StackItem>
        <StackItem>
          <ListPageBody isLoading={pageLoading} error={pageError}>
            {showEmptyState ? (
              emptyState
            ) : viewType === 'cards' ? (
              <Gallery hasGutter minWidths={{ default: '400px' }} maxWidths={{ default: '400px' }}>
                {clusters.map((cluster) => (
                  <GalleryItem key={cluster.id}>
                    <ClusterCard
                      cluster={cluster}
                      clusterVersion={resolveClusterVersion(cluster)}
                    />
                  </GalleryItem>
                ))}
              </Gallery>
            ) : (
              <ClustersTable clusters={clusters} resolveClusterVersion={resolveClusterVersion} />
            )}
          </ListPageBody>
        </StackItem>
      </Stack>
    </ListPage>
  );
};
