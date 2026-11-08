import { useState } from 'react';
import { Stack, StackItem } from '@patternfly/react-core';

import { SecurityGroups } from '@osac/types';
import CreateButton from '@osac/ui-components/components/Primitives/CreateButton.tsx';

import { useSecurityGroups, useVirtualNetworks } from '../../api/v1/networking';
import NetworkingListEmptyState from '../../components/networking/NetworkingListEmptyState';
import NetworkingListToolbar from '../../components/networking/NetworkingListToolbar';
import { SecurityGroupCreateModal } from '../../components/networking/SecurityGroupCreateModal';
import SecurityGroupsTable from '../../components/networking/SecurityGroupsTable';
import { useNetworkingListFilters } from '../../components/networking/useNetworkingListFilters';
import ListPage from '../../components/Page/ListPage';
import ListPageBody from '../../components/Page/ListPageBody';
import { useTranslation } from '../../hooks/useTranslation';

export const SecurityGroupsListPage = () => {
  const { t } = useTranslation();
  const filters = useNetworkingListFilters();
  const [isCreateModalOpen, setIsCreateModalOpen] = useState(false);

  const { data: securityGroups = [], isLoading, error } = useSecurityGroups();
  const { data: virtualNetworks = [] } = useVirtualNetworks();

  const filteredSecurityGroups = securityGroups.filter((sg) =>
    filters.matches(sg, sg.spec?.virtualNetwork?.id),
  );
  const createButton = (
    <CreateButton service={SecurityGroups} onClick={() => setIsCreateModalOpen(true)}>
      {t('Create security group')}
    </CreateButton>
  );

  return (
    <>
      <ListPage
        title={t('Security groups')}
        description={t('Firewall rules for the workloads in a virtual network.')}
        error={error}
        actions={createButton}
      >
        <ListPageBody isLoading={isLoading} error={error}>
          <Stack hasGutter>
            <StackItem>
              <NetworkingListToolbar
                filters={filters}
                searchLabel={t('Search security groups by name')}
                virtualNetworks={virtualNetworks}
                shownCount={filteredSecurityGroups.length}
                totalCount={securityGroups.length}
              />
            </StackItem>
            <StackItem>
              {filteredSecurityGroups.length === 0 ? (
                <NetworkingListEmptyState
                  isFiltered={filters.isFiltered}
                  emptyTitle={t('No security groups')}
                  emptyBody={t('Create a security group, then add its rules with Edit rules.')}
                  emptyAction={createButton}
                  onClearFilters={filters.clearAll}
                />
              ) : (
                <SecurityGroupsTable
                  securityGroups={filteredSecurityGroups}
                  virtualNetworks={virtualNetworks}
                />
              )}
            </StackItem>
          </Stack>
        </ListPageBody>
      </ListPage>

      {isCreateModalOpen && (
        <SecurityGroupCreateModal onClose={() => setIsCreateModalOpen(false)} />
      )}
    </>
  );
};
