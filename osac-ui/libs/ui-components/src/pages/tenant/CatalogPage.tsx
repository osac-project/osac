import { useCallback, useMemo, useState } from 'react';
import { useSearchParams } from 'react-router-dom';
import {
  Button,
  Content,
  EmptyState,
  EmptyStateBody,
  Flex,
  FlexItem,
  Label,
  SearchInput,
  Stack,
  StackItem,
  ToggleGroup,
  ToggleGroupItem,
  Toolbar,
  ToolbarContent,
  ToolbarGroup,
  ToolbarItem,
} from '@patternfly/react-core';

import { type CatalogListFilterCriteria } from '@osac/ui-components/api/v1/catalog-item-list-filter';
import {
  CatalogItemKind,
  isCatalogItemKind,
  isCatalogPublishedFilter,
} from '@osac/ui-components/components/catalog/catalogItemDisplay';
import {
  CATALOG_ITEMS_VIEW_KEY,
  CatalogItemListSection,
} from '@osac/ui-components/components/catalog/CatalogItemListSection';
import CatalogPublishedStatusFilter from '@osac/ui-components/components/catalog/CatalogPublishedStatusFilter';
import CatalogTenantFilter from '@osac/ui-components/components/catalog/CatalogTenantFilter.tsx';
import ListPage from '@osac/ui-components/components/Page/ListPage';
import FieldSeparator from '@osac/ui-components/components/Primitives/FieldSeparator';
import ViewSwitcher from '@osac/ui-components/components/Primitives/ViewSwitcher';
import { useCatalogItems } from '@osac/ui-components/hooks/use-catalog-items';
import {
  SEARCH_PARAM,
  useArrayPageFilter,
  usePageFilter,
} from '@osac/ui-components/hooks/use-page-filter';
import { useSession } from '@osac/ui-components/hooks/use-session.tsx';
import { useTranslation } from '@osac/ui-components/hooks/useTranslation';

const TYPE_FILTER_PARAM = 'types';
const PUBLISHED_FILTER_PARAM = 'published';
const TENANT_FILTER_PARAM = 'tenant';

const CatalogPage = () => {
  const { t } = useTranslation();
  const { role } = useSession();
  const [, setSearchParams] = useSearchParams();
  const [typeFilter, setTypeFilter] = useArrayPageFilter(TYPE_FILTER_PARAM, isCatalogItemKind);
  const [publishedFilterParam, setPublishedFilterParam] = usePageFilter(PUBLISHED_FILTER_PARAM);
  const [tenantFilter, setTenantFilter] = usePageFilter(TENANT_FILTER_PARAM);
  const [searchFilter, setSearchFilter] = usePageFilter(SEARCH_PARAM);
  const publishedFilter = isCatalogPublishedFilter(publishedFilterParam)
    ? publishedFilterParam
    : undefined;
  const isFiltered = publishedFilter || typeFilter?.length || tenantFilter || searchFilter;
  const [typeFilterChanged, setTypeFilterChanged] = useState<boolean>(false);

  const filterCriteria = useMemo<CatalogListFilterCriteria>(
    () => ({
      search: searchFilter,
      published: publishedFilter,
      tenant: tenantFilter,
    }),
    [publishedFilter, searchFilter, tenantFilter],
  );

  const {
    vms,
    bms,
    clusters,
    isLoading,
    error,
    hasSuccessfulQuery,
    typeCounts,
    unfilteredTotalItems,
  } = useCatalogItems(filterCriteria, typeFilter, !typeFilter?.length && !typeFilterChanged);

  const catalogTypeFilters = useMemo<ReadonlyArray<{ value: CatalogItemKind; label: string }>>(
    () => [
      { value: 'bm', label: t('Bare Metal Machines') },
      { value: 'cluster', label: t('Clusters') },
      { value: 'vm', label: t('Virtual Machines') },
    ],
    [t],
  );

  const data = [...vms, ...clusters, ...bms];

  const showFullCatalogCount =
    data.length === unfilteredTotalItems && !isFiltered && typeFilter.length === 0;

  const showEmptyState = !isLoading && !error && data.length === 0;

  const pageDescription =
    role === 'admin'
      ? t(
          'Create catalog items from master templates across Bare Metal, Clusters, Models, and Virtual machines, then attach them to tenants.',
        )
      : role === 'tenant-admin'
        ? t("Filter the provider's global catalog down to safe, approved offerings.")
        : t('Browse and provision catalog items available to your tenant.');

  // react-router's setSearchParams does not compose across synchronous calls;
  // each updater receives the render-captured params. Clear every filter in one
  // update so none of them is overwritten.
  const clearAllFilters = () => {
    setSearchParams(
      (prev) => {
        const next = new URLSearchParams(prev);
        next.delete(TYPE_FILTER_PARAM);
        next.delete(PUBLISHED_FILTER_PARAM);
        next.delete(TENANT_FILTER_PARAM);
        next.delete(SEARCH_PARAM);
        return next;
      },
      { replace: true },
    );
    setTypeFilterChanged(false);
  };

  const renderEmptyState = () => {
    if (unfilteredTotalItems === 0) {
      return (
        <EmptyState titleText={t('No catalog items found')} headingLevel="h2">
          <EmptyStateBody>{t('No catalog items are available yet.')}</EmptyStateBody>
        </EmptyState>
      );
    }

    return (
      <EmptyState titleText={t('No catalog items match your filters')} headingLevel="h2">
        <EmptyStateBody>
          {t('Try a different service, publish status, tenant, or search term.')}{' '}
          <Button variant="link" isInline onClick={clearAllFilters}>
            {t('Clear all filters')}
          </Button>
        </EmptyStateBody>
      </EmptyState>
    );
  };
  const selectedTypeOptions: CatalogItemKind[] = useMemo(() => {
    if (typeFilter?.length || typeFilterChanged) {
      return typeFilter;
    }

    return Object.keys(typeCounts).filter(
      (key) => typeCounts[key as CatalogItemKind] > 0,
    ) as CatalogItemKind[];
  }, [typeCounts, typeFilter, typeFilterChanged]);

  const toggleTypeFilter = useCallback(
    (kind: CatalogItemKind) => {
      setTypeFilter(kind);

      if (!typeFilter?.length && !typeFilterChanged) {
        selectedTypeOptions.forEach((option) => {
          setTypeFilter(option);
        });
        setTypeFilterChanged(true);
      }
    },
    [setTypeFilter, typeFilter?.length, typeFilterChanged, selectedTypeOptions],
  );

  return (
    <ListPage label={t('Global marketplace')} title={t('Catalog')} description={pageDescription}>
      <Stack hasGutter>
        <StackItem>
          <Toolbar>
            <ToolbarContent rowWrap={{ default: 'nowrap' }}>
              <ToolbarGroup>
                <ToolbarItem>
                  <ToggleGroup aria-label={t('Filter catalog by resource type')}>
                    {catalogTypeFilters.map((option) => {
                      const count = typeCounts[option.value];

                      return (
                        <ToggleGroupItem
                          key={option.value}
                          text={
                            <Flex
                              spaceItems={{ default: 'spaceItemsSm' }}
                              flexWrap={{ default: 'nowrap' }}
                            >
                              <FlexItem>{option.label}</FlexItem>
                              <FlexItem>
                                <Label isCompact>{count}</Label>
                              </FlexItem>
                            </Flex>
                          }
                          buttonId={`catalog-type-filter-${option.value}`}
                          isSelected={selectedTypeOptions.includes(option.value)}
                          onChange={() => {
                            toggleTypeFilter(option.value);
                          }}
                        />
                      );
                    })}
                  </ToggleGroup>
                </ToolbarItem>
                {role === 'admin' || role === 'tenant-admin' ? (
                  <ToolbarItem>
                    <CatalogPublishedStatusFilter
                      selected={publishedFilter}
                      onChange={(value) => setPublishedFilterParam(value ?? '')}
                    />
                  </ToolbarItem>
                ) : null}
                {role === 'admin' ? (
                  <ToolbarItem>
                    <CatalogTenantFilter
                      selected={tenantFilter}
                      onChange={(value) => setTenantFilter(value ?? '')}
                    />
                  </ToolbarItem>
                ) : null}
                <ToolbarItem>
                  <SearchInput
                    placeholder={t('Search catalog items')}
                    value={searchFilter}
                    onChange={(_event, value) => setSearchFilter(value)}
                    onClear={() => setSearchFilter('')}
                    aria-label={t('Filter catalog by keyword')}
                    isDisabled={!hasSuccessfulQuery}
                  />
                </ToolbarItem>
              </ToolbarGroup>
              <ToolbarGroup align={{ default: 'alignEnd' }}>
                <ToolbarItem>
                  <ViewSwitcher pageKey={CATALOG_ITEMS_VIEW_KEY} />
                </ToolbarItem>
              </ToolbarGroup>
            </ToolbarContent>
          </Toolbar>
        </StackItem>
        {showEmptyState ? (
          <StackItem>{renderEmptyState()}</StackItem>
        ) : (
          <>
            <StackItem>
              <Flex gap={{ default: 'gapXs' }}>
                <FlexItem>
                  <Content className="pf-v6-u-font-weight-bold">
                    {showFullCatalogCount
                      ? t('{{count}} catalog item', { count: unfilteredTotalItems })
                      : t('{{shown}} of {{count}} catalog item', {
                          shown: data.length,
                          count: unfilteredTotalItems,
                        })}
                  </Content>
                </FlexItem>
                {isFiltered ? (
                  <>
                    <FlexItem>
                      <FieldSeparator />
                    </FlexItem>
                    <FlexItem>
                      <Button variant="link" isInline onClick={clearAllFilters}>
                        {t('Clear all filters')}
                      </Button>
                    </FlexItem>
                  </>
                ) : null}
              </Flex>
            </StackItem>
            <StackItem>
              <CatalogItemListSection items={data} isLoading={isLoading} error={error} />
            </StackItem>
          </>
        )}
      </Stack>
    </ListPage>
  );
};

export default CatalogPage;
