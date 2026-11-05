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

import {
  BareMetalInstanceTypes,
  type BareMetalInstanceType as PrivateBareMetalInstanceType,
} from '@osac/types/private';
import { useListResource } from '@osac/ui-components/api/use-resource';
import ListPage from '@osac/ui-components/components/Page/ListPage';
import ListPageBody from '@osac/ui-components/components/Page/ListPageBody';
import ListPageToolbar from '@osac/ui-components/components/Page/ListPageToolbar';
import CreateButton from '@osac/ui-components/components/Primitives/CreateButton.tsx';
import DebounceSearchInput from '@osac/ui-components/components/Primitives/DebounceSearchInput';
import ViewSwitcher from '@osac/ui-components/components/Primitives/ViewSwitcher';
import { SEARCH_PARAM, usePageFilter } from '@osac/ui-components/hooks/use-page-filter';
import { useViewType } from '@osac/ui-components/hooks/use-view-type';
import { useTranslation } from '@osac/ui-components/hooks/useTranslation';

import AdminBareMetalInstanceTypeTable from './AdminBareMetalInstanceTypeTable';
import { buildAdminBareMetalInstanceTypeListFilter } from './ListPage/admin-bare-metal-instance-type-list-filter';
import AdminBareMetalInstanceTypeCard from './ListPage/AdminBareMetalInstanceTypeCard';

export const BAREMETAL_INSTANCE_TYPES_LIST_ROUTE = '/admin/infrastructure/baremetal-instance-types';

const ADMIN_BARE_METAL_INSTANCE_TYPES_VIEW_KEY = 'admin-bare-metal-instance-types-list';

const EMPTY_BARE_METAL_INSTANCE_TYPES: PrivateBareMetalInstanceType[] = [];

const AdminBareMetalInstanceTypeListPage = () => {
  const { t } = useTranslation();
  const [, setSearchParams] = useSearchParams();
  const [search, setSearch] = usePageFilter(SEARCH_PARAM);
  const [searchResetKey, setSearchResetKey] = useState(0);
  const [viewType] = useViewType(ADMIN_BARE_METAL_INSTANCE_TYPES_VIEW_KEY);

  const listFilter = useMemo(() => buildAdminBareMetalInstanceTypeListFilter({ search }), [search]);

  const {
    data: listResponse,
    isLoading,
    error,
  } = useListResource(BareMetalInstanceTypes, { filter: listFilter });
  const bareMetalInstanceTypes = listResponse?.items ?? EMPTY_BARE_METAL_INSTANCE_TYPES;

  const {
    data: totalsResponse,
    isLoading: totalsLoading,
    error: totalsError,
  } = useListResource(BareMetalInstanceTypes, { limit: 0 });
  const totalItems = !totalsLoading && !totalsError ? (totalsResponse?.total ?? 0) : 0;

  const isFiltered = Boolean(search.trim());
  const showEmptyState = !isLoading && !error && bareMetalInstanceTypes.length === 0;

  const clearAllFilters = () => {
    setSearchResetKey((key) => key + 1);
    requestAnimationFrame(() => {
      setSearchParams(
        (prev) => {
          const next = new URLSearchParams(prev);
          next.delete(SEARCH_PARAM);
          return next;
        },
        { replace: true },
      );
    });
  };

  const emptyState =
    totalItems === 0 && !isFiltered ? (
      <EmptyState titleText={t('No bare metal instance types found')} headingLevel="h2">
        <EmptyStateBody>{t('No bare metal instance types are available yet.')}</EmptyStateBody>
      </EmptyState>
    ) : (
      <EmptyState
        titleText={t('No bare metal instance types match your filters')}
        headingLevel="h2"
      >
        <EmptyStateBody>
          {t('Try a different search term.')}{' '}
          <Button variant="link" isInline onClick={clearAllFilters}>
            {t('Clear all filters')}
          </Button>
        </EmptyStateBody>
      </EmptyState>
    );

  const filteredText =
    totalsLoading || totalsError || bareMetalInstanceTypes.length === totalItems
      ? t('{{count}} bare metal instance type', { count: bareMetalInstanceTypes.length })
      : t('{{shown}} of {{count}} bare metal instance type', {
          shown: bareMetalInstanceTypes.length,
          count: totalItems,
        });

  return (
    <ListPage
      title={t('Bare metal instance types')}
      description={t('Manage provider-defined bare metal instance types for this cloud platform.')}
      error={error}
      actions={
        <CreateButton
          service={BareMetalInstanceTypes}
          to="/admin/infrastructure/baremetal-instance-types/create"
        >
          {t('Create bare metal instance type')}
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
                  <DebounceSearchInput
                    placeholder={t('Search by name')}
                    value={search}
                    onChange={setSearch}
                    resetKey={searchResetKey}
                    aria-label={t('Filter bare metal instance types by name')}
                  />
                </ToolbarItem>
                <ToolbarGroup align={{ default: 'alignEnd' }}>
                  <ToolbarItem>
                    <ViewSwitcher pageKey={ADMIN_BARE_METAL_INSTANCE_TYPES_VIEW_KEY} />
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
                {bareMetalInstanceTypes.map((bareMetalInstanceType) => (
                  <GalleryItem key={bareMetalInstanceType.id}>
                    <AdminBareMetalInstanceTypeCard bareMetalInstanceType={bareMetalInstanceType} />
                  </GalleryItem>
                ))}
              </Gallery>
            ) : (
              <AdminBareMetalInstanceTypeTable bareMetalInstanceTypes={bareMetalInstanceTypes} />
            )}
          </ListPageBody>
        </StackItem>
      </Stack>
    </ListPage>
  );
};

export default AdminBareMetalInstanceTypeListPage;
