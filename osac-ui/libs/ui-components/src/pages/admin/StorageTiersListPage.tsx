import { useMemo, useState } from 'react';
import { useSearchParams } from 'react-router-dom';
import {
  Alert,
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

import { StorageBackend, StorageBackends, StorageTiers } from '@osac/types/private';
import { cel } from '@osac/ui-components/api/cel';
import { useListResource } from '@osac/ui-components/api/use-resource';
import ListPage from '@osac/ui-components/components/Page/ListPage';
import ListPageBody from '@osac/ui-components/components/Page/ListPageBody';
import ListPageToolbar from '@osac/ui-components/components/Page/ListPageToolbar';
import CreateButton from '@osac/ui-components/components/Primitives/CreateButton.tsx';
import DebounceSearchInput from '@osac/ui-components/components/Primitives/DebounceSearchInput';
import ViewSwitcher from '@osac/ui-components/components/Primitives/ViewSwitcher';
import { uniqueBackendIds } from '@osac/ui-components/components/Storage/storageTierBackendResolution';
import StorageTierCard from '@osac/ui-components/components/Storage/StorageTierCard';
import { StorageTiersTable } from '@osac/ui-components/components/Storage/StorageTiersTable';
import {
  STORAGE_TIERS_LIST_PATH,
  buildStorageTierListFilter,
} from '@osac/ui-components/components/Storage/utils';
import { SEARCH_PARAM, usePageFilter } from '@osac/ui-components/hooks/use-page-filter';
import { useViewType } from '@osac/ui-components/hooks/use-view-type';
import { useTranslation } from '@osac/ui-components/hooks/useTranslation';

const STORAGE_TIERS_VIEW_KEY = 'storage-tiers';

export const StorageTiersListPage = () => {
  const { t } = useTranslation();
  const [, setSearchParams] = useSearchParams();
  const [search, setSearch] = usePageFilter(SEARCH_PARAM);
  const [viewType] = useViewType(STORAGE_TIERS_VIEW_KEY);
  const [searchResetKey, setSearchResetKey] = useState(0);

  const filter = useMemo(() => buildStorageTierListFilter(search.trim()), [search]);

  const { data, isLoading, error } = useListResource(StorageTiers, { filter });

  const { data: totalsData, error: totalsError } = useListResource(StorageTiers, {
    limit: 0,
  });

  const items = useMemo(() => data?.items ?? [], [data?.items]);
  const totalItems = totalsData?.total ?? 0;
  const hasTotals = totalsData !== undefined && !totalsError;

  const backendIds = useMemo(() => uniqueBackendIds(items), [items]);

  const { data: backends, error: backendsError } = useListResource(
    StorageBackends,
    { filter: cel<StorageBackend>((filter) => filter.field('id').isIn(backendIds)) },
    { enabled: backendIds.length > 0 },
  );

  const backendsById = useMemo(
    () => new Map((backends?.items || []).map((backend) => [backend.id, backend])),
    [backends?.items],
  );

  const isFiltered = Boolean(search);
  const showEmptyState = !isLoading && !error && items.length === 0;

  const clearAllFilters = () => {
    setSearchResetKey((key) => key + 1);
    setSearchParams(
      (prev) => {
        const next = new URLSearchParams(prev);
        next.delete(SEARCH_PARAM);
        return next;
      },
      { replace: true },
    );
  };

  const emptyState =
    totalItems === 0 && !isFiltered ? (
      <EmptyState titleText={t('No storage tiers found')} headingLevel="h2">
        <EmptyStateBody>{t('No storage tiers are available yet.')}</EmptyStateBody>
      </EmptyState>
    ) : (
      <EmptyState titleText={t('No storage tiers match your filters')} headingLevel="h2">
        <EmptyStateBody>
          {t('Try a different search term.')}{' '}
          <Button variant="link" isInline onClick={clearAllFilters}>
            {t('Clear all filters')}
          </Button>
        </EmptyStateBody>
      </EmptyState>
    );

  const filteredText = hasTotals
    ? items.length === totalItems
      ? t('{{count}} storage tier', { count: totalItems })
      : t('{{shown}} of {{count}} storage tier', {
          shown: items.length,
          count: totalItems,
        })
    : undefined;

  return (
    <ListPage
      title={t('Storage Tiers')}
      description={t('Manage storage tiers for this cloud platform.')}
      error={error}
      actions={
        <CreateButton service={StorageTiers} to={`${STORAGE_TIERS_LIST_PATH}/create`}>
          {t('Create tier')}
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
                    <DebounceSearchInput
                      placeholder={t('Search storage tiers')}
                      value={search}
                      onChange={(v) => setSearch(v)}
                      resetKey={searchResetKey}
                      aria-label={t('Search storage tiers')}
                    />
                  </ToolbarItem>
                </ToolbarGroup>
                <ToolbarGroup align={{ default: 'alignEnd' }}>
                  <ToolbarItem>
                    <ViewSwitcher pageKey={STORAGE_TIERS_VIEW_KEY} />
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
            ) : (
              <Stack hasGutter>
                {Boolean(backendsError) && (
                  <StackItem>
                    <Alert variant="warning" isInline title={t('Unable to resolve backend names')}>
                      {t(
                        'Backend IDs are shown in place of names until this recovers. This is separate from the normal fallback shown when a tier references a backend that no longer exists.',
                      )}
                    </Alert>
                  </StackItem>
                )}
                <StackItem>
                  {viewType === 'cards' ? (
                    <Gallery
                      hasGutter
                      minWidths={{ default: '300px' }}
                      maxWidths={{ default: '1fr' }}
                    >
                      {items.map((tier) => (
                        <GalleryItem key={tier.id}>
                          <StorageTierCard tier={tier} backendsById={backendsById} />
                        </GalleryItem>
                      ))}
                    </Gallery>
                  ) : (
                    <StorageTiersTable tiers={items} backendsById={backendsById} />
                  )}
                </StackItem>
              </Stack>
            )}
          </ListPageBody>
        </StackItem>
      </Stack>
    </ListPage>
  );
};
