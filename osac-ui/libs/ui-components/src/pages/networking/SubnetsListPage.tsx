import { useState } from 'react';
import { Stack, StackItem } from '@patternfly/react-core';

import { Subnets } from '@osac/types';
import CreateButton from '@osac/ui-components/components/Primitives/CreateButton.tsx';

import { useSubnets, useVirtualNetworks } from '../../api/v1/networking';
import NetworkingListEmptyState from '../../components/networking/NetworkingListEmptyState';
import NetworkingListToolbar from '../../components/networking/NetworkingListToolbar';
import { SubnetCreateModal } from '../../components/networking/SubnetCreateModal';
import SubnetsTable from '../../components/networking/SubnetsTable';
import { useNetworkingListFilters } from '../../components/networking/useNetworkingListFilters';
import ListPage from '../../components/Page/ListPage';
import ListPageBody from '../../components/Page/ListPageBody';
import { useTranslation } from '../../hooks/useTranslation';

export const SubnetsListPage = () => {
  const { t } = useTranslation();
  const filters = useNetworkingListFilters();
  const [isCreateModalOpen, setIsCreateModalOpen] = useState(false);

  const { data: subnets = [], isLoading, error } = useSubnets();
  const {
    data: virtualNetworks = [],
    isLoading: isLoadingVirtualNetworks,
    error: virtualNetworksError,
  } = useVirtualNetworks();

  const filteredSubnets = subnets.filter((subnet) =>
    filters.matches(subnet, subnet.spec?.virtualNetwork?.id),
  );
  const createButton = (
    <CreateButton service={Subnets} onClick={() => setIsCreateModalOpen(true)}>
      {t('Create subnet')}
    </CreateButton>
  );

  return (
    <>
      <ListPage
        title={t('Subnets')}
        description={t('Address ranges inside virtual networks.')}
        error={error}
        actions={createButton}
      >
        <ListPageBody
          isLoading={isLoading || isLoadingVirtualNetworks}
          error={error ?? virtualNetworksError}
        >
          <Stack hasGutter>
            <StackItem>
              <NetworkingListToolbar
                filters={filters}
                searchLabel={t('Search subnets by name')}
                virtualNetworks={virtualNetworks}
                shownCount={filteredSubnets.length}
                totalCount={subnets.length}
              />
            </StackItem>
            <StackItem>
              {filteredSubnets.length === 0 ? (
                <NetworkingListEmptyState
                  isFiltered={filters.isFiltered}
                  emptyTitle={t('No subnets')}
                  emptyBody={t('Create a subnet to give workloads addresses in a virtual network.')}
                  emptyAction={createButton}
                  onClearFilters={filters.clearAll}
                />
              ) : (
                <SubnetsTable subnets={filteredSubnets} virtualNetworks={virtualNetworks} />
              )}
            </StackItem>
          </Stack>
        </ListPageBody>
      </ListPage>

      {isCreateModalOpen && (
        <SubnetCreateModal
          onClose={() => setIsCreateModalOpen(false)}
          virtualNetworkId={filters.virtualNetworkId}
        />
      )}
    </>
  );
};
