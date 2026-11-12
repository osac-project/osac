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

import { ExternalIPPools } from '@osac/types/private';
import { useListResource } from '@osac/ui-components/api/use-resource';
import ListPage from '@osac/ui-components/components/Page/ListPage';
import ListPageBody from '@osac/ui-components/components/Page/ListPageBody';
import ListPageToolbar from '@osac/ui-components/components/Page/ListPageToolbar';
import CreateButton from '@osac/ui-components/components/Primitives/CreateButton.tsx';
import DebounceSearchInput from '@osac/ui-components/components/Primitives/DebounceSearchInput';
import ViewSwitcher from '@osac/ui-components/components/Primitives/ViewSwitcher';
import {
  SEARCH_PARAM,
  useArrayPageFilter,
  usePageFilter,
} from '@osac/ui-components/hooks/use-page-filter';
import { useViewType } from '@osac/ui-components/hooks/use-view-type';
import { useTranslation } from '@osac/ui-components/hooks/useTranslation';

import ExternalIpPoolCard from './ExternalIpPoolCard';
import { ExternalIpPoolsTable } from './ExternalIpPoolsTable';
import ExternalIpPoolStateFilter from './ExternalIpPoolStateFilter';
import {
  EXTERNAL_IP_POOL_STATE_FILTER_TO_ENUM,
  type ExternalIpPoolStateFilterValue,
  STATE_FILTER_PARAM,
  buildExternalIpPoolListFilter,
  isExternalIpPoolStateFilterValue,
} from './utils';
import { EXTERNAL_IP_POOLS_LIST_PATH } from './values';

const EXTERNAL_IP_POOLS_VIEW_KEY = 'external-ip-pools';

export const ExternalIpPoolsListPage = () => {
  const { t } = useTranslation();
  const [, setSearchParams] = useSearchParams();
  const [search, setSearch] = usePageFilter(SEARCH_PARAM);
  const [stateFilters, toggleStateFilter] = useArrayPageFilter<ExternalIpPoolStateFilterValue>(
    STATE_FILTER_PARAM,
    isExternalIpPoolStateFilterValue,
  );
  const [viewType] = useViewType(EXTERNAL_IP_POOLS_VIEW_KEY);
  const [searchResetKey, setSearchResetKey] = useState(0);

  const stateEnumValues = useMemo(
    () => stateFilters.map((value) => EXTERNAL_IP_POOL_STATE_FILTER_TO_ENUM[value]),
    [stateFilters],
  );

  const filter = useMemo(
    () => buildExternalIpPoolListFilter(stateEnumValues, search.trim()),
    [stateEnumValues, search],
  );

  const { data, isLoading, error } = useListResource(ExternalIPPools, { filter });

  const { data: totalsData, error: totalsError } = useListResource(ExternalIPPools, {
    limit: 0,
  });

  const items = data?.items ?? [];
  const totalItems = totalsData?.total ?? 0;
  const hasTotals = totalsData !== undefined && !totalsError;

  const isFiltered = Boolean(search || stateEnumValues.length);
  const showEmptyState = !isLoading && !error && items.length === 0;

  const clearAllFilters = () => {
    setSearchResetKey((key) => key + 1);
    setSearchParams(
      (prev) => {
        const next = new URLSearchParams(prev);
        next.delete(SEARCH_PARAM);
        next.delete(STATE_FILTER_PARAM);
        return next;
      },
      { replace: true },
    );
  };

  const emptyState =
    items.length === 0 && !isFiltered ? (
      <EmptyState titleText={t('No external IP pools found')} headingLevel="h2">
        <EmptyStateBody>{t('No external IP pools are available yet.')}</EmptyStateBody>
      </EmptyState>
    ) : (
      <EmptyState titleText={t('No external IP pools match your filters')} headingLevel="h2">
        <EmptyStateBody>
          {t('Try a different state, or search term.')}{' '}
          <Button variant="link" isInline onClick={clearAllFilters}>
            {t('Clear all filters')}
          </Button>
        </EmptyStateBody>
      </EmptyState>
    );

  const filteredText = hasTotals
    ? items.length === totalItems
      ? t('{{count}} external IP pool', { count: totalItems })
      : t('{{shown}} of {{total}} external IP pools', {
          shown: items.length,
          total: totalItems,
        })
    : undefined;

  return (
    <ListPage
      title={t('External IP pools')}
      description={t('Manage external IP address pools for this cloud platform.')}
      error={error}
      actions={
        <CreateButton service={ExternalIPPools} to={`${EXTERNAL_IP_POOLS_LIST_PATH}/create`}>
          {t('Create pool')}
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
                  <ExternalIpPoolStateFilter
                    stateFilters={stateFilters}
                    onToggle={toggleStateFilter}
                  />
                  <ToolbarItem>
                    <DebounceSearchInput
                      placeholder={t('Search external IP pools')}
                      value={search}
                      onChange={(v) => setSearch(v)}
                      resetKey={searchResetKey}
                      aria-label={t('Search external IP pools')}
                    />
                  </ToolbarItem>
                </ToolbarGroup>
                <ToolbarGroup align={{ default: 'alignEnd' }}>
                  <ToolbarItem>
                    <ViewSwitcher pageKey={EXTERNAL_IP_POOLS_VIEW_KEY} />
                  </ToolbarItem>
                </ToolbarGroup>
              </ToolbarContent>
            </Toolbar>
          </ListPageToolbar>
        </StackItem>
        <StackItem>
          <ListPageBody isLoading={isLoading} error={error}>
            {showEmptyState ? (
              emptyState
            ) : viewType === 'cards' ? (
              <Gallery hasGutter minWidths={{ default: '300px' }} maxWidths={{ default: '1fr' }}>
                {items.map((pool) => (
                  <GalleryItem key={pool.id}>
                    <ExternalIpPoolCard pool={pool} />
                  </GalleryItem>
                ))}
              </Gallery>
            ) : (
              <ExternalIpPoolsTable pools={items} />
            )}
          </ListPageBody>
        </StackItem>
      </Stack>
    </ListPage>
  );
};
