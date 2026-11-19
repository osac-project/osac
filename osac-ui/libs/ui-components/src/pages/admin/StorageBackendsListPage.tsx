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

import { StorageBackends } from '@osac/types/private';
import { useListResource } from '@osac/ui-components/api/use-resource';
import ListPage from '@osac/ui-components/components/Page/ListPage';
import ListPageBody from '@osac/ui-components/components/Page/ListPageBody';
import ListPageToolbar from '@osac/ui-components/components/Page/ListPageToolbar';
import CreateButton from '@osac/ui-components/components/Primitives/CreateButton.tsx';
import DebounceSearchInput from '@osac/ui-components/components/Primitives/DebounceSearchInput';
import ViewSwitcher from '@osac/ui-components/components/Primitives/ViewSwitcher';
import StorageBackendCard from '@osac/ui-components/components/Storage/StorageBackendCard';
import { StorageBackendsTable } from '@osac/ui-components/components/Storage/StorageBackendsTable';
import { STORAGE_BACKENDS_LIST_PATH } from '@osac/ui-components/components/Storage/utils';
import { SEARCH_PARAM, usePageFilter } from '@osac/ui-components/hooks/use-page-filter';
import { useViewType } from '@osac/ui-components/hooks/use-view-type';
import { useTranslation } from '@osac/ui-components/hooks/useTranslation';

import { buildStorageBackendListFilter } from '../../components/Storage/utils';

const STORAGE_BACKENDS_VIEW_KEY = 'storage-backends';

export const StorageBackendsListPage = () => {
  const { t } = useTranslation();
  const [, setSearchParams] = useSearchParams();
  const [search, setSearch] = usePageFilter(SEARCH_PARAM);
  const [viewType] = useViewType(STORAGE_BACKENDS_VIEW_KEY);
  const [searchResetKey, setSearchResetKey] = useState(0);

  const filter = useMemo(() => buildStorageBackendListFilter(search.trim()), [search]);

  const { data, isLoading, error } = useListResource(StorageBackends, { filter });

  const { data: totalsData, error: totalsError } = useListResource(StorageBackends, {
    limit: 0,
  });

  const items = data?.items ?? [];
  const totalItems = totalsData?.total ?? 0;
  const hasTotals = totalsData !== undefined && !totalsError;

  const isFiltered = Boolean(search.trim());
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
      <EmptyState titleText={t('No storage backends found')} headingLevel="h2">
        <EmptyStateBody>{t('No storage backends are available yet.')}</EmptyStateBody>
      </EmptyState>
    ) : (
      <EmptyState titleText={t('No storage backends match your filters')} headingLevel="h2">
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
      ? t('{{count}} storage backend', { count: totalItems })
      : t('{{shown}} of {{total}} storage backends', {
          shown: items.length,
          total: totalItems,
        })
    : undefined;

  return (
    <ListPage
      title={t('Storage Backends')}
      description={t('Manage storage backends for this cloud platform.')}
      error={error}
      actions={
        <CreateButton service={StorageBackends} to={`${STORAGE_BACKENDS_LIST_PATH}/create`}>
          {t('Create backend')}
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
                      placeholder={t('Search storage backends')}
                      value={search}
                      onChange={(v) => setSearch(v)}
                      resetKey={searchResetKey}
                      aria-label={t('Search storage backends')}
                    />
                  </ToolbarItem>
                </ToolbarGroup>
                <ToolbarGroup align={{ default: 'alignEnd' }}>
                  <ToolbarItem>
                    <ViewSwitcher pageKey={STORAGE_BACKENDS_VIEW_KEY} />
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
                {items.map((backend) => (
                  <GalleryItem key={backend.id}>
                    <StorageBackendCard backend={backend} />
                  </GalleryItem>
                ))}
              </Gallery>
            ) : (
              <StorageBackendsTable backends={items} />
            )}
          </ListPageBody>
        </StackItem>
      </Stack>
    </ListPage>
  );
};
