import { startTransition, useMemo, useState } from 'react';
import { useSearchParams } from 'react-router-dom';
import {
  Gallery,
  GalleryItem,
  Stack,
  StackItem,
  Toolbar,
  ToolbarContent,
  ToolbarGroup,
  ToolbarItem,
} from '@patternfly/react-core';

import { SecurityGroups, Subnets, VirtualNetworks } from '@osac/types';
import { useListAllResources, useListResource } from '@osac/ui-components/api/use-resource';
import { useNatGateways, virtualNetworksScopeFilter } from '@osac/ui-components/api/v1/networking';
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
import DebounceSearchInput from '@osac/ui-components/components/Primitives/DebounceSearchInput';
import ViewSwitcher from '@osac/ui-components/components/Primitives/ViewSwitcher';
import {
  SEARCH_PARAM,
  useArrayPageFilter,
  usePageFilter,
} from '@osac/ui-components/hooks/use-page-filter.ts';
import { useSession } from '@osac/ui-components/hooks/use-session';
import { useViewType } from '@osac/ui-components/hooks/use-view-type';
import { useTranslation } from '@osac/ui-components/hooks/useTranslation';

import { resourceDisplayName } from '../../api/v1/networking';
import GatedCreateButton from '../../components/networking/GatedCreateButton';
import NetworkingListEmptyState from '../../components/networking/NetworkingListEmptyState';
import NetworkingSelectFilter from '../../components/networking/NetworkingSelectFilter';
import { useNetworkClassNotReadyReason } from '../../components/networking/useNetworkClassNotReadyReason';
import { TENANT_FILTER_PARAM } from '../../components/networking/useNetworkingListFilters';
import { useNetworkingTenants } from '../../components/networking/useNetworkingTenants';
import { VirtualNetworkCreateModal } from '../../components/networking/VirtualNetworkCreateModal';
import ProjectFilter from '../../components/Page/ProjectFilter';

const VIRTUAL_NETWORKS_VIEW_KEY = 'virtual-networks';

export const VirtualNetworksListPage = () => {
  const { t } = useTranslation();
  // Cloud provider admins see every tenant's networks, so the table names the tenant.
  const { isAdmin, tenants, tenantName } = useNetworkingTenants();
  // A virtual network needs a READY network class; only the provider admin can see it.
  const networkClassNotReadyReason = useNetworkClassNotReadyReason();
  const [, setSearchParams] = useSearchParams();
  const [search, setSearch] = usePageFilter(SEARCH_PARAM);
  const [tenantParam, setTenantId] = usePageFilter(TENANT_FILTER_PARAM);
  const tenantId = isAdmin ? tenantParam : '';
  const { projects, setProjects } = useSession();
  const [searchResetKey, setSearchResetKey] = useState(0);
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
    () => buildVirtualNetworkListFilter(stateEnumValues, search.trim(), tenantId, projects),
    [stateEnumValues, search, tenantId, projects],
  );

  const { data, isLoading, error } = useListResource(VirtualNetworks, { filter });

  const { data: totalsData, error: totalsError } = useListResource(VirtualNetworks, {
    limit: 0,
  });

  const virtualIdsFilter = useMemo(() => {
    if (isLoading || error || !data?.items?.length) {
      return undefined;
    }
    return virtualNetworksScopeFilter(data.items.map((item) => item.id));
  }, [data?.items, error, isLoading]);

  const {
    data: subnetReply,
    isLoading: subnetsIsLoading,
    error: subnetsError,
    isPlaceholderData: subnetsPlaceholderData,
  } = useListAllResources(Subnets, virtualIdsFilter, { enabled: Boolean(virtualIdsFilter) });
  const subnetsLoading = subnetsIsLoading || subnetsPlaceholderData;

  const {
    data: securityGroupReply,
    isLoading: securityGroupsIsLoading,
    error: securityGroupsError,
    isPlaceholderData: securityGroupsPlaceholderData,
  } = useListAllResources(SecurityGroups, virtualIdsFilter, { enabled: Boolean(virtualIdsFilter) });
  const securityGroupsLoading = securityGroupsIsLoading || securityGroupsPlaceholderData;

  const {
    natGatewaysByVirtualNetworkId,
    isLoading: isLoadingNatGateways,
    error: natGatewaysError,
  } = useNatGateways();

  const subnetCountByVN = useMemo(
    () =>
      (subnetReply?.items ?? []).reduce(
        (acc, subnet) => {
          const vnId = subnet.spec?.virtualNetwork?.id;
          if (vnId) {
            acc[vnId] = (acc[vnId] || 0) + 1;
          }
          return acc;
        },
        {} as Record<string, number>,
      ),
    [subnetReply],
  );

  const securityGroupCountByVN = useMemo(
    () =>
      (securityGroupReply?.items ?? []).reduce(
        (acc, securityGroup) => {
          const vnId = securityGroup.spec?.virtualNetwork?.id;
          if (vnId) {
            acc[vnId] = (acc[vnId] || 0) + 1;
          }
          return acc;
        },
        {} as Record<string, number>,
      ),
    [securityGroupReply],
  );

  const items = data?.items ?? [];
  const totalItems = totalsData?.total ?? 0;
  const hasTotals = totalsData !== undefined && !totalsError;

  const isFiltered = Boolean(
    search.trim() || stateEnumValues.length || tenantId || projects.length > 0,
  );
  const showEmptyState = !isLoading && !error && items.length === 0;

  const clearAllFilters = () => {
    setSearchResetKey((key) => key + 1);
    if (projects.length > 0) {
      // In the router's transition, so the project filter sees the cleared URL.
      startTransition(() => setProjects([]));
    }
    setSearchParams(
      (prev) => {
        const next = new URLSearchParams(prev);
        next.delete(SEARCH_PARAM);
        next.delete(STATE_FILTER_PARAM);
        next.delete(TENANT_FILTER_PARAM);
        return next;
      },
      { replace: true },
    );
  };

  const createButton = (
    <GatedCreateButton
      service={VirtualNetworks}
      onClick={() => setIsCreateModalOpen(true)}
      disabledReason={networkClassNotReadyReason}
    >
      {t('Create virtual network')}
    </GatedCreateButton>
  );

  const emptyState = (
    <NetworkingListEmptyState
      isFiltered={isFiltered}
      emptyTitle={t('No virtual networks')}
      emptyBody={t('Create a virtual network to hold subnets and security groups for workloads.')}
      emptyAction={createButton}
      onClearFilters={clearAllFilters}
    />
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
        description={t('Networks for workloads and catalog networking.')}
        error={error}
        actions={createButton}
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
                    {isAdmin ? (
                      <ToolbarItem>
                        <NetworkingSelectFilter
                          allLabel={t('All tenants')}
                          ariaLabel={t('Filter by tenant')}
                          options={tenants.map((tenant) => ({
                            value: tenant.id,
                            label: resourceDisplayName(tenant.metadata, tenant.id),
                          }))}
                          selected={tenantId}
                          onChange={setTenantId}
                        />
                      </ToolbarItem>
                    ) : null}
                    <ToolbarItem>
                      <ProjectFilter />
                    </ToolbarItem>
                    <VirtualNetworkStateFilter
                      stateFilters={stateFilters}
                      onToggle={toggleStateFilter}
                    />
                    <ToolbarItem>
                      <DebounceSearchInput
                        placeholder={t('Search virtual networks')}
                        value={search}
                        resetKey={searchResetKey}
                        onChange={setSearch}
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
            <ListPageBody isLoading={isLoading} error={error}>
              {showEmptyState ? (
                emptyState
              ) : viewType === 'cards' ? (
                <Gallery hasGutter minWidths={{ default: '300px' }} maxWidths={{ default: '1fr' }}>
                  {items.map((vn) => (
                    <GalleryItem key={vn.id}>
                      <VirtualNetworkCard
                        virtualNetwork={vn}
                        subnetCount={subnetCountByVN[vn.id] || 0}
                        subnetsLoading={subnetsLoading}
                        subnetsError={subnetsError}
                        securityGroupCount={securityGroupCountByVN[vn.id] || 0}
                        securityGroupsLoading={securityGroupsLoading}
                        securityGroupsError={securityGroupsError}
                        natGatewayWithAddress={natGatewaysByVirtualNetworkId[vn.id]}
                        natGatewaysLoading={isLoadingNatGateways}
                        natGatewaysError={natGatewaysError}
                        tenantName={isAdmin ? tenantName : undefined}
                      />
                    </GalleryItem>
                  ))}
                </Gallery>
              ) : (
                <VirtualNetworkTable
                  virtualNetworks={items}
                  subnetCountByVirtualNetworkId={subnetCountByVN}
                  subnetsLoading={subnetsLoading}
                  subnetsError={subnetsError}
                  securityGroupCountByVirtualNetworkId={securityGroupCountByVN}
                  securityGroupsLoading={securityGroupsLoading}
                  securityGroupsError={securityGroupsError}
                  natGatewaysByVirtualNetworkId={natGatewaysByVirtualNetworkId}
                  natGatewaysLoading={isLoadingNatGateways}
                  natGatewaysError={natGatewaysError}
                  tenantName={isAdmin ? tenantName : undefined}
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
