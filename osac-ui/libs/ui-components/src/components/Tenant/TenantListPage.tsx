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

import { Tenants } from '@osac/types/private';
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
} from '@osac/ui-components/hooks/use-page-filter.ts';
import { useViewType } from '@osac/ui-components/hooks/use-view-type';
import { useTranslation } from '@osac/ui-components/hooks/useTranslation';

import TenantCard from './TenantCard';
import TenantStateFilter from './TenantStateFilter';
import TenantTable from './TenantTable';
import {
  STATE_FILTER_PARAM,
  TENANT_STATE_FILTER_TO_ENUM,
  type TenantStateFilterValue,
  buildTenantListFilter,
  isTenantStateFilterValue,
} from './utils';

const TENANTS_VIEW_KEY = 'tenants';

const TenantListPage = () => {
  const { t } = useTranslation();
  const [, setSearchParams] = useSearchParams();
  const [search, setSearch] = usePageFilter(SEARCH_PARAM);
  const [stateFilters, toggleStateFilter] = useArrayPageFilter<TenantStateFilterValue>(
    STATE_FILTER_PARAM,
    isTenantStateFilterValue,
  );
  const [viewType] = useViewType(TENANTS_VIEW_KEY);
  const [searchResetKey, setSearchResetKey] = useState(0);

  const stateEnumValues = useMemo(
    () => stateFilters.map((value) => TENANT_STATE_FILTER_TO_ENUM[value]),
    [stateFilters],
  );

  const filter = useMemo(
    () => buildTenantListFilter(stateEnumValues, search.trim()),
    [stateEnumValues, search],
  );

  const { data, isLoading, error } = useListResource(Tenants, { filter });

  const { data: totalsData, error: totalsError } = useListResource(Tenants, {
    limit: 0,
  });

  const items = data?.items ?? [];
  const totalItems = totalsData?.total ?? 0;
  const hasTotals = totalsData !== undefined && !totalsError;

  const isFiltered = Boolean(search || stateEnumValues.length);

  const showEmptyState = !isLoading && !error && items.length === 0;

  const clearAllFilters = () => {
    setSearchResetKey((key) => key + 1);
    requestAnimationFrame(() => {
      setSearchParams(
        (prev) => {
          const next = new URLSearchParams(prev);
          next.delete(SEARCH_PARAM);
          next.delete(STATE_FILTER_PARAM);
          return next;
        },
        { replace: true },
      );
    });
  };

  const emptyState =
    totalItems === 0 && !isFiltered ? (
      <EmptyState titleText={t('No tenants found')} headingLevel="h2">
        <EmptyStateBody>{t('No tenants are available yet.')}</EmptyStateBody>
      </EmptyState>
    ) : (
      <EmptyState titleText={t('No tenants match your filters')} headingLevel="h2">
        <EmptyStateBody>
          {t('Try a different state or search term.')}{' '}
          <Button variant="link" isInline onClick={clearAllFilters}>
            {t('Clear all filters')}
          </Button>
        </EmptyStateBody>
      </EmptyState>
    );

  const filteredText = hasTotals
    ? items.length === totalItems
      ? t('{{count}} tenant', { count: totalItems })
      : t('{{shown}} of {{total}} tenants', {
          shown: items.length,
          total: totalItems,
        })
    : undefined;

  return (
    <ListPage
      title={t('Tenants')}
      description={t('Manage tenants for this cloud platform.')}
      error={error}
      actions={
        <CreateButton service={Tenants} to="/admin/tenants/create">
          {t('Create tenant')}
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
                  <TenantStateFilter stateFilters={stateFilters} onToggle={toggleStateFilter} />
                  <ToolbarItem>
                    <DebounceSearchInput
                      placeholder={t('Search tenants')}
                      value={search}
                      onChange={(v) => setSearch(v)}
                      resetKey={searchResetKey}
                      aria-label={t('Search tenants')}
                    />
                  </ToolbarItem>
                </ToolbarGroup>
                <ToolbarGroup align={{ default: 'alignEnd' }}>
                  <ToolbarItem>
                    <ViewSwitcher pageKey={TENANTS_VIEW_KEY} />
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
                {items.map((tenant) => (
                  <GalleryItem key={tenant.id}>
                    <TenantCard tenant={tenant} />
                  </GalleryItem>
                ))}
              </Gallery>
            ) : (
              <TenantTable tenants={items} />
            )}
          </ListPageBody>
        </StackItem>
      </Stack>
    </ListPage>
  );
};

export default TenantListPage;
