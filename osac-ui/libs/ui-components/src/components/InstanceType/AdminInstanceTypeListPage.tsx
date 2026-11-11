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

import { InstanceTypes, type InstanceType as PrivateInstanceType } from '@osac/types/private';
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

import AdminInstanceTypeTable from './AdminInstanceTypeTable';
import {
  buildAdminInstanceTypeListFilter,
  isAdminInstanceTypeLifecycleFilter,
} from './ListPage/admin-instance-type-list-filter';
import AdminInstanceTypeCard from './ListPage/AdminInstanceTypeCard';
import AdminInstanceTypeLifecycleFilter from './ListPage/AdminInstanceTypeLifecycleFilter';

const LIFECYCLE_FILTER_PARAM = 'lifecycle';
const ADMIN_INSTANCE_TYPES_VIEW_KEY = 'admin-instance-types-list';

const EMPTY_INSTANCE_TYPES: PrivateInstanceType[] = [];

const AdminInstanceTypeListPage = () => {
  const { t } = useTranslation();
  const [, setSearchParams] = useSearchParams();
  const [search, setSearch] = usePageFilter(SEARCH_PARAM);
  const [searchResetKey, setSearchResetKey] = useState(0);
  const [lifecycleFilters, toggleLifecycleFilter] = useArrayPageFilter(
    LIFECYCLE_FILTER_PARAM,
    isAdminInstanceTypeLifecycleFilter,
  );
  const [viewType] = useViewType(ADMIN_INSTANCE_TYPES_VIEW_KEY);

  const listFilter = useMemo(
    () =>
      buildAdminInstanceTypeListFilter({
        search,
        lifecycles: lifecycleFilters,
      }),
    [lifecycleFilters, search],
  );

  const {
    data: listResponse,
    isLoading,
    error,
  } = useListResource(InstanceTypes, { filter: listFilter });
  const instanceTypes = listResponse?.items ?? EMPTY_INSTANCE_TYPES;

  const {
    data: totalsResponse,
    isLoading: totalsLoading,
    error: totalsError,
  } = useListResource(InstanceTypes, { limit: 0 });
  const totalItems = !totalsLoading && !totalsError ? (totalsResponse?.total ?? 0) : 0;

  const isFiltered = Boolean(search.trim() || lifecycleFilters.length);
  const showEmptyState = !isLoading && !error && instanceTypes.length === 0;

  const clearAllFilters = () => {
    setSearchResetKey((key) => key + 1);
    requestAnimationFrame(() => {
      setSearchParams(
        (prev) => {
          const next = new URLSearchParams(prev);
          next.delete(SEARCH_PARAM);
          next.delete(LIFECYCLE_FILTER_PARAM);
          return next;
        },
        { replace: true },
      );
    });
  };

  const emptyState =
    totalItems === 0 && !isFiltered ? (
      <EmptyState titleText={t('No instance types found')} headingLevel="h2">
        <EmptyStateBody>{t('No instance types are available yet.')}</EmptyStateBody>
      </EmptyState>
    ) : (
      <EmptyState titleText={t('No instance types match your filters')} headingLevel="h2">
        <EmptyStateBody>
          {t('Try a different lifecycle state or search term.')}{' '}
          <Button variant="link" isInline onClick={clearAllFilters}>
            {t('Clear all filters')}
          </Button>
        </EmptyStateBody>
      </EmptyState>
    );

  const filteredText =
    totalsLoading || totalsError || instanceTypes.length === totalItems
      ? t('{{count}} instance type', { count: instanceTypes.length })
      : t('{{shown}} of {{count}} instance type', {
          shown: instanceTypes.length,
          count: totalItems,
        });

  return (
    <ListPage
      title={t('Instance types')}
      description={t('Manage provider-defined instance types for this cloud platform.')}
      error={error}
      actions={
        <CreateButton service={InstanceTypes} to="/admin/infrastructure/instance-types/create">
          {t('Create instance type')}
        </CreateButton>
      }
    >
      <Stack hasGutter>
        <StackItem>
          <ListPageToolbar
            filteredText={!isLoading && !error && !showEmptyState ? filteredText : undefined}
            isFiltered={isFiltered}
            onClearAllFilters={clearAllFilters}
          >
            <Toolbar>
              <ToolbarContent>
                <ToolbarItem>
                  <AdminInstanceTypeLifecycleFilter
                    lifecycleFilters={lifecycleFilters}
                    onToggle={toggleLifecycleFilter}
                  />
                </ToolbarItem>
                <ToolbarItem>
                  <DebounceSearchInput
                    placeholder={t('Search by name')}
                    value={search}
                    onChange={setSearch}
                    resetKey={searchResetKey}
                    aria-label={t('Filter instance types by name')}
                  />
                </ToolbarItem>
                <ToolbarGroup align={{ default: 'alignEnd' }}>
                  <ToolbarItem>
                    <ViewSwitcher pageKey={ADMIN_INSTANCE_TYPES_VIEW_KEY} />
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
              <Gallery hasGutter minWidths={{ default: '400px' }} maxWidths={{ default: '400px' }}>
                {instanceTypes.map((instanceType) => (
                  <GalleryItem key={instanceType.id}>
                    <AdminInstanceTypeCard instanceType={instanceType} />
                  </GalleryItem>
                ))}
              </Gallery>
            ) : (
              <AdminInstanceTypeTable instanceTypes={instanceTypes} />
            )}
          </ListPageBody>
        </StackItem>
      </Stack>
    </ListPage>
  );
};

export default AdminInstanceTypeListPage;
