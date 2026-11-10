import { useMemo, useState } from 'react';
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

import { type ExternalIP, ExternalIPs } from '@osac/types';
import { useListResource } from '@osac/ui-components/api/use-resource';
import { externalIpPoolId, useExternalIpJoins } from '@osac/ui-components/api/v1/external-ip-data';
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
import { useProjectFilterQuery } from '@osac/ui-components/hooks/use-project-filter-query';
import { PROJECT_FILTER_PARAM, useSession } from '@osac/ui-components/hooks/use-session';
import { useViewType } from '@osac/ui-components/hooks/use-view-type';
import { useTranslation } from '@osac/ui-components/hooks/useTranslation';

import { EXTERNAL_IPS_LIST_PATH } from './CreatePage/values';
import ExternalIpCard from './ExternalIpCard';
import ExternalIpsTable from './ExternalIpsTable';
import ExternalIpStateFilter from './ExternalIpStateFilter';
import {
  EXTERNAL_IP_STATE_FILTER_TO_ENUM,
  type ExternalIpStateFilterValue,
  STATE_FILTER_PARAM,
  buildExternalIpListFilter,
  isExternalIpStateFilterValue,
} from './utils';

const EXTERNAL_IPS_VIEW_KEY = 'external-ips';

const ExternalIpListPage = () => {
  const { t } = useTranslation();
  const [, setSearchParams] = useSearchParams();
  const { projects, setProjects } = useSession();
  const [search, setSearch] = usePageFilter(SEARCH_PARAM);
  const [stateFilters, toggleStateFilter] = useArrayPageFilter<ExternalIpStateFilterValue>(
    STATE_FILTER_PARAM,
    isExternalIpStateFilterValue,
  );
  const projectFilter = useProjectFilterQuery<ExternalIP>();
  const [viewType] = useViewType(EXTERNAL_IPS_VIEW_KEY);
  const [searchResetKey, setSearchResetKey] = useState(0);

  const stateEnumValues = useMemo(
    () => stateFilters.map((value) => EXTERNAL_IP_STATE_FILTER_TO_ENUM[value]),
    [stateFilters],
  );

  const filter = useMemo(
    () => buildExternalIpListFilter(projectFilter, stateEnumValues, search.trim()),
    [projectFilter, stateEnumValues, search],
  );

  const { data, isLoading, error } = useListResource(ExternalIPs, { filter });

  const { data: totalsData, error: totalsError } = useListResource(ExternalIPs, {
    filter: projectFilter,
    limit: 0,
  });

  const items = data?.items ?? [];
  const totalItems = totalsData?.total ?? 0;
  const hasTotals = totalsData !== undefined && !totalsError;
  const hasDisplayedIps = items.length > 0;
  const {
    poolsById,
    attachedTargetsByExternalIpId,
    isLoading: joinsLoading,
    error: joinsError,
  } = useExternalIpJoins(items);

  const isFiltered = Boolean(search || projects.length || stateEnumValues.length);

  const showEmptyState = !isLoading && !error && items.length === 0;

  const clearAllFilters = () => {
    setSearchResetKey((key) => key + 1);
    setProjects([]);
    requestAnimationFrame(() => {
      setSearchParams(
        (prev) => {
          const next = new URLSearchParams(prev);
          next.delete(SEARCH_PARAM);
          next.delete(PROJECT_FILTER_PARAM);
          next.delete(STATE_FILTER_PARAM);
          return next;
        },
        { replace: true },
      );
    });
  };

  const emptyState =
    totalItems === 0 && !isFiltered ? (
      <EmptyState titleText={t('No external IPs found')} headingLevel="h2">
        <EmptyStateBody>{t('No external IPs are available yet.')}</EmptyStateBody>
      </EmptyState>
    ) : (
      <EmptyState titleText={t('No external IPs match your filters')} headingLevel="h2">
        <EmptyStateBody>
          {t('Try a different project, state, or search term.')}{' '}
          <Button variant="link" isInline onClick={clearAllFilters}>
            {t('Clear all filters')}
          </Button>
        </EmptyStateBody>
      </EmptyState>
    );

  const filteredText = hasTotals
    ? items.length === totalItems
      ? t('{{count}} external IP', { count: totalItems })
      : t('{{shown}} of {{total}} external IPs', {
          shown: items.length,
          total: totalItems,
        })
    : undefined;

  const bodyLoading = isLoading || (hasDisplayedIps && joinsLoading);
  const bodyError = error ?? joinsError;

  return (
    <ListPage
      title={t('External IPs')}
      description={t(
        'Review provider-assigned IP pools and allocate external addresses for edge exposure.',
      )}
      error={bodyError}
      actions={
        <CreateButton service={ExternalIPs} to={`${EXTERNAL_IPS_LIST_PATH}/create`}>
          {t('Create external IP')}
        </CreateButton>
      }
    >
      <Stack hasGutter>
        <StackItem>
          <ListPageToolbar
            filteredText={showEmptyState ? undefined : filteredText}
            isFiltered={isFiltered}
            onClearAllFilters={clearAllFilters}
          >
            <Toolbar>
              <ToolbarContent>
                <ToolbarGroup>
                  <ToolbarItem>
                    <ProjectFilter />
                  </ToolbarItem>
                  <ExternalIpStateFilter stateFilters={stateFilters} onToggle={toggleStateFilter} />
                  <ToolbarItem>
                    <DebounceSearchInput
                      placeholder={t('Search external IPs')}
                      value={search}
                      onChange={(v) => setSearch(v)}
                      resetKey={searchResetKey}
                      aria-label={t('Search external IPs')}
                    />
                  </ToolbarItem>
                </ToolbarGroup>
                <ToolbarGroup align={{ default: 'alignEnd' }}>
                  <ToolbarItem>
                    <ViewSwitcher pageKey={EXTERNAL_IPS_VIEW_KEY} />
                  </ToolbarItem>
                </ToolbarGroup>
              </ToolbarContent>
            </Toolbar>
          </ListPageToolbar>
        </StackItem>
        <StackItem>
          <ListPageBody isLoading={bodyLoading} error={bodyError}>
            {showEmptyState ? (
              emptyState
            ) : viewType === 'cards' ? (
              <Gallery hasGutter minWidths={{ default: '300px' }} maxWidths={{ default: '1fr' }}>
                {items.map((externalIp) => {
                  const poolId = externalIpPoolId(externalIp);
                  return (
                    <GalleryItem key={externalIp.id}>
                      <ExternalIpCard
                        externalIp={externalIp}
                        pool={poolId ? poolsById[poolId] : undefined}
                        attachedTarget={attachedTargetsByExternalIpId[externalIp.id]}
                      />
                    </GalleryItem>
                  );
                })}
              </Gallery>
            ) : (
              <ExternalIpsTable
                externalIps={items}
                poolsById={poolsById}
                attachedTargetsByExternalIpId={attachedTargetsByExternalIpId}
              />
            )}
          </ListPageBody>
        </StackItem>
      </Stack>
    </ListPage>
  );
};

export default ExternalIpListPage;
