import { useMemo } from 'react';
import {
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
} from '@patternfly/react-core';

import { useBareMetalInstanceCatalogItems } from '@osac/ui-components/api/v1/baremetal-instance';
import { useClusterCatalogItems } from '@osac/ui-components/api/v1/cluster-catalog-item';
import { useComputeInstanceCatalogItems } from '@osac/ui-components/api/v1/compute-instance-catalog-item';
import {
  CatalogItemKind,
  filterCatalogItemsBySearch,
  isCatalogItemKind,
} from '@osac/ui-components/components/catalog/catalogItemDisplay';
import { CatalogItemListSection } from '@osac/ui-components/components/catalog/CatalogItemListSection';
import ListPage from '@osac/ui-components/components/Page/ListPage';
import {
  SEARCH_PARAM,
  useArrayPageFilter,
  usePageFilter,
} from '@osac/ui-components/hooks/use-page-filter';
import { useTranslation } from '@osac/ui-components/hooks/useTranslation';

const TYPE_FILTER_PARAM = 'types';

const useCatalogItems = () => {
  const vms = useComputeInstanceCatalogItems();
  const clusters = useClusterCatalogItems();
  const bms = useBareMetalInstanceCatalogItems();

  const isLoading = vms.isLoading || clusters.isLoading || bms.isLoading;
  const error = vms.error || clusters.error || bms.error;
  const hasSuccessfulQuery = [vms, clusters, bms].some((query) => !query.isLoading && !query.error);

  return {
    error,
    isLoading,
    hasSuccessfulQuery,
    vms: vms.data,
    clusters: clusters.data,
    bms: bms.data,
  };
};

const CatalogPage = () => {
  const { t } = useTranslation();
  const [typeFilter, setTypeFilter] = useArrayPageFilter(TYPE_FILTER_PARAM, isCatalogItemKind);
  const [searchFilter, setSearchFilter] = usePageFilter(SEARCH_PARAM);

  const {
    vms = [],
    bms = [],
    clusters = [],
    isLoading,
    error,
    hasSuccessfulQuery,
  } = useCatalogItems();

  const catalogTypeFilters = useMemo<ReadonlyArray<{ value: CatalogItemKind; label: string }>>(
    () => [
      { value: 'bm', label: t('Bare Metal Machines') },
      { value: 'cluster', label: t('Clusters') },
      { value: 'vm', label: t('Virtual Machines') },
    ],
    [t],
  );

  const filteredVms =
    !typeFilter.length || typeFilter.includes('vm')
      ? filterCatalogItemsBySearch(vms, searchFilter)
      : [];
  const filteredBms =
    !typeFilter.length || typeFilter.includes('bm')
      ? filterCatalogItemsBySearch(bms, searchFilter)
      : [];
  const filteredClusters =
    !typeFilter.length || typeFilter.includes('cluster')
      ? filterCatalogItemsBySearch(clusters, searchFilter)
      : [];

  const data = [...filteredVms, ...filteredClusters, ...filteredBms];

  const isCatalogEmpty = vms.length === 0 && bms.length === 0 && clusters.length === 0;
  const showEmptyState = !isLoading && !error && data.length === 0;

  const pageDescription = t(
    'Browse catalog items and launch virtual machines, clusters, or bare metal machines from published offerings.',
  );

  return (
    <ListPage title={t('Catalog')} description={pageDescription}>
      <Stack hasGutter>
        <StackItem>
          <Flex
            spaceItems={{ default: 'spaceItemsSm' }}
            alignItems={{ default: 'alignItemsCenter' }}
            flexWrap={{ default: 'wrap' }}
          >
            <FlexItem>
              <ToggleGroup aria-label={t('Filter catalog by resource type')}>
                {catalogTypeFilters.map((option) => {
                  let count = 0;
                  switch (option.value) {
                    case 'vm':
                      count = vms.length;
                      break;
                    case 'bm':
                      count = bms.length;
                      break;
                    case 'cluster':
                      count = clusters.length;
                      break;
                  }

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
                      isSelected={typeFilter.includes(option.value)}
                      onChange={() => setTypeFilter(option.value)}
                    />
                  );
                })}
              </ToggleGroup>
            </FlexItem>
            <FlexItem>
              <SearchInput
                placeholder={t('Search catalog items')}
                value={searchFilter}
                onChange={(_event, value) => setSearchFilter(value)}
                onClear={() => setSearchFilter('')}
                aria-label={t('Filter catalog by keyword')}
                isDisabled={isLoading || !hasSuccessfulQuery}
              />
            </FlexItem>
          </Flex>
        </StackItem>

        {showEmptyState ? (
          <StackItem>
            <EmptyState titleText={t('No catalog items found')} headingLevel="h2">
              <EmptyStateBody>
                {isCatalogEmpty
                  ? t('No published catalog items are available yet.')
                  : t('No catalog items match your filters.')}
              </EmptyStateBody>
            </EmptyState>
          </StackItem>
        ) : (
          <CatalogItemListSection items={data} isLoading={isLoading} error={error} />
        )}
      </Stack>
    </ListPage>
  );
};

export default CatalogPage;
