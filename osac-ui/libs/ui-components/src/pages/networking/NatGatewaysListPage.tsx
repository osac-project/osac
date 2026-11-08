import { useState } from 'react';
import { Stack, StackItem } from '@patternfly/react-core';

import { NATGateways } from '@osac/types';
import CreateButton from '@osac/ui-components/components/Primitives/CreateButton.tsx';

import { useNatGateways, useVirtualNetworks } from '../../api/v1/networking';
import NatGatewayCreateModal from '../../components/networking/NatGatewayCreateModal';
import NatGatewaysTable from '../../components/networking/NatGatewaysTable';
import NetworkingListEmptyState from '../../components/networking/NetworkingListEmptyState';
import NetworkingListToolbar from '../../components/networking/NetworkingListToolbar';
import { useNetworkingListFilters } from '../../components/networking/useNetworkingListFilters';
import ListPage from '../../components/Page/ListPage';
import ListPageBody from '../../components/Page/ListPageBody';
import { useTranslation } from '../../hooks/useTranslation';

export const NatGatewaysListPage = () => {
  const { t } = useTranslation();
  const filters = useNetworkingListFilters();
  const [isCreateModalOpen, setIsCreateModalOpen] = useState(false);

  const { natGateways, addressByExternalIpId, isLoading, error } = useNatGateways();
  const {
    data: virtualNetworks = [],
    isLoading: isLoadingVirtualNetworks,
    error: virtualNetworksError,
  } = useVirtualNetworks();

  const filteredNatGateways = natGateways.filter((natGateway) =>
    filters.matches(natGateway, natGateway.spec?.virtualNetwork?.id),
  );
  const createButton = (
    <CreateButton service={NATGateways} onClick={() => setIsCreateModalOpen(true)}>
      {t('Create NAT gateway')}
    </CreateButton>
  );

  return (
    <>
      <ListPage
        title={t('NAT gateways')}
        description={t(
          'Outbound connectivity from a virtual network to destinations outside the virtual network.',
        )}
        error={error}
        actions={createButton}
      >
        <ListPageBody isLoading={isLoading || isLoadingVirtualNetworks} error={error}>
          <Stack hasGutter>
            <StackItem>
              <NetworkingListToolbar
                filters={filters}
                searchLabel={t('Search NAT gateways by name')}
                virtualNetworks={virtualNetworks}
                shownCount={filteredNatGateways.length}
                totalCount={natGateways.length}
              />
            </StackItem>
            <StackItem>
              {filteredNatGateways.length === 0 ? (
                <NetworkingListEmptyState
                  isFiltered={filters.isFiltered}
                  emptyTitle={t('No NAT gateways')}
                  emptyBody={t(
                    'Create a NAT gateway to give a virtual network outbound access through an external IP.',
                  )}
                  emptyAction={createButton}
                  onClearFilters={filters.clearAll}
                />
              ) : (
                <NatGatewaysTable
                  natGateways={filteredNatGateways}
                  virtualNetworks={virtualNetworks}
                  addressByExternalIpId={addressByExternalIpId}
                />
              )}
            </StackItem>
          </Stack>
        </ListPageBody>
      </ListPage>

      {isCreateModalOpen && (
        <NatGatewayCreateModal
          virtualNetworks={virtualNetworks}
          natGateways={natGateways}
          isLoading={isLoadingVirtualNetworks}
          error={virtualNetworksError}
          virtualNetworkId={filters.virtualNetworkId}
          onClose={() => setIsCreateModalOpen(false)}
        />
      )}
    </>
  );
};
