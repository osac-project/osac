import { useMemo, useState } from 'react';
import { useSearchParams } from 'react-router-dom';
import {
  Button,
  EmptyState,
  EmptyStateBody,
  Gallery,
  GalleryItem,
  SearchInput,
  Stack,
  StackItem,
  Toolbar,
  ToolbarContent,
  ToolbarGroup,
  ToolbarItem,
} from '@patternfly/react-core';

import { VirtualNetworks } from '@osac/types';
import { useListResource } from '@osac/ui-components/api/use-resource';
import { useNatGateways } from '@osac/ui-components/api/v1/networking';
import {
  STATE_FILTER_PARAM,
  VIRTUAL_NETWORK_STATE_FILTER_TO_ENUM,
  type VirtualNetworkStateFilterValue,
  buildVirtualNetworkListFilter,
  isVirtualNetworkStateFilterValue,
} from '@osac/ui-components/components/networking/virtual-network-list-utils';
import VirtualNetworkCard from '@osac/ui-components/components/networking/VirtualNetworkCard';
import VirtualNetworkStateFilter from '@osac/ui-components/components/networking/VirtualNetworkStateFilter';
import VirtualNetworkTable from '@osac/ui-components/components/networking/VirtualNetworkTable';
import ListPage from '@osac/ui-components/components/Page/ListPage';
import ListPageBody from '@osac/ui-components/components/Page/ListPageBody';
import ListPageToolbar from '@osac/ui-components/components/Page/ListPageToolbar';
import CreateButton from '@osac/ui-components/components/Primitives/CreateButton.tsx';
import ViewSwitcher from '@osac/ui-components/components/Primitives/ViewSwitcher';
import {
  SEARCH_PARAM,
  useArrayPageFilter,
  usePageFilter,
} from '@osac/ui-components/hooks/use-page-filter.ts';
import { useViewType } from '@osac/ui-components/hooks/use-view-type';
import { useTranslation } from '@osac/ui-components/hooks/useTranslation';

import { VirtualNetworkCreateModal } from '../../components/networking/VirtualNetworkCreateModal';

const VIRTUAL_NETWORKS_VIEW_KEY = 'virtual-networks';

export const VirtualNetworksListPage = () => {
  const { t } = useTranslation();
  const [, setSearchParams] = useSearchParams();
  const [search, setSearch] = usePageFilter(SEARCH_PARAM);
  const [stateFilters, toggleStateFilter] = useArrayPageFilter<VirtualNetworkStateFilterValue>(
    STATE_FILTER_PARAM,
    isVirtualNetworkStateFilterValue,
  );
  const [viewType] = useViewType(VIRTUAL_NETWORKS_VIEW_KEY);
  const [isCreateModalOpen, setIsCreateModalOpen] = useState(false);

  const stateEnumValues = useMemo(
    () => stateFilters.map((value) => VIRTUAL_NETWORK_STATE_FILTER_TO_ENUM[value]),
    [stateFilters],
  );

  const filter = useMemo(
    () => buildVirtualNetworkListFilter(stateEnumValues, search.trim()),
    [stateEnumValues, search],
  );

  const { data, isLoading, error } = useListResource(VirtualNetworks, { filter });

  const { data: totalsData, error: totalsError } = useListResource(VirtualNetworks, {
    limit: 0,
  });

  const {
    natGatewaysByVirtualNetworkId,
    isLoading: isLoadingNatGateways,
    error: natGatewaysError,
  } = useNatGateways();

  const items = data?.items ?? [];
  const totalItems = totalsData?.total ?? 0;
  const hasTotals = totalsData !== undefined && !totalsError;

  const isFiltered = Boolean(search.trim() || stateEnumValues.length);
  const showEmptyState = !isLoading && !error && items.length === 0;
  const listError = error ?? natGatewaysError;
  const isListLoading = isLoading || isLoadingNatGateways;

  const clearAllFilters = () => {
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
      <EmptyState titleText={t('No virtual networks found')} headingLevel="h2">
        <EmptyStateBody>{t('No virtual networks are available yet.')}</EmptyStateBody>
      </EmptyState>
    ) : (
      <EmptyState titleText={t('No virtual networks match your filters')} headingLevel="h2">
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
      ? t('{{count}} virtual network', { count: totalItems })
      : t('{{shown}} of {{count}} virtual network', {
          shown: items.length,
          count: totalItems,
        })
    : undefined;

  return (
    <>
      <ListPage
        title={t('Virtual networks')}
        description={t('Virtual networks your tenant uses for workloads and catalog networking.')}
        error={listError}
        actions={
          <CreateButton service={VirtualNetworks} onClick={() => setIsCreateModalOpen(true)}>
            {t('Create virtual network')}
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
                    <VirtualNetworkStateFilter
                      stateFilters={stateFilters}
                      onToggle={toggleStateFilter}
                    />
                    <ToolbarItem>
                      <SearchInput
                        placeholder={t('Search virtual networks')}
                        value={search}
                        onChange={(_e, v) => setSearch(v)}
                        onClear={() => setSearch('')}
                        aria-label={t('Search virtual networks')}
                      />
                    </ToolbarItem>
                  </ToolbarGroup>
                  <ToolbarGroup align={{ default: 'alignEnd' }}>
                    <ToolbarItem>
                      <ViewSwitcher pageKey={VIRTUAL_NETWORKS_VIEW_KEY} />
                    </ToolbarItem>
                  </ToolbarGroup>
                </ToolbarContent>
              </Toolbar>
            </ListPageToolbar>
          </StackItem>
          <StackItem>
            <ListPageBody isLoading={isListLoading} error={listError}>
              {showEmptyState ? (
                emptyState
              ) : viewType === 'cards' ? (
                <Gallery hasGutter minWidths={{ default: '300px' }} maxWidths={{ default: '1fr' }}>
                  {items.map((vn) => (
                    <GalleryItem key={vn.id}>
                      <VirtualNetworkCard
                        virtualNetwork={vn}
                        natGatewayWithAddress={natGatewaysByVirtualNetworkId[vn.id]}
                      />
                    </GalleryItem>
                  ))}
                </Gallery>
              ) : (
                <VirtualNetworkTable
                  virtualNetworks={items}
                  natGatewaysByVirtualNetworkId={natGatewaysByVirtualNetworkId}
                />
              )}
            </ListPageBody>
          </StackItem>
        </Stack>
      </ListPage>

      {isCreateModalOpen && (
        <VirtualNetworkCreateModal onClose={() => setIsCreateModalOpen(false)} />
      )}
    </>
  );
};
