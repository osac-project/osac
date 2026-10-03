import { useState } from 'react';
import {
  Content,
  SearchInput,
  Toolbar,
  ToolbarContent,
  ToolbarGroup,
  ToolbarItem,
} from '@patternfly/react-core';
import { Table, Tbody, Td, Th, Thead, Tr } from '@patternfly/react-table';

import CreateButton from '@osac/ui-components/components/Primitives/CreateButton.tsx';
import ResourceNameField from '@osac/ui-components/components/Resource/ResourceNameField.tsx';
import { SEARCH_PARAM, usePageFilter } from '@osac/ui-components/hooks/use-page-filter.ts';

import { useNatGateways, useSubnets, useVirtualNetworks } from '../../api/v1/networking';
import { CidrDisplay } from '../../components/networking/CidrDisplay';
import { VirtualNetworkCreateModal } from '../../components/networking/VirtualNetworkCreateModal';
import { VirtualNetworkStatusLabel } from '../../components/networking/VirtualNetworkStatusLabel';
import ListPage from '../../components/Page/ListPage';
import ListPageBody from '../../components/Page/ListPageBody';
import { SubtleContent } from '../../components/SubtleContent/SubtleContent';
import { useTranslation } from '../../hooks/useTranslation';

export const VirtualNetworksListPage = () => {
  const { t } = useTranslation();
  const [search, setSearch] = usePageFilter(SEARCH_PARAM);
  const [isCreateModalOpen, setIsCreateModalOpen] = useState(false);

  const { data: virtualNetworks = [], isLoading, error } = useVirtualNetworks();
  const { data: allSubnets = [] } = useSubnets();
  const {
    natGatewaysByVirtualNetworkId,
    isLoading: isLoadingNatGateways,
    error: natGatewaysError,
  } = useNatGateways();

  const subnetCountByVN = allSubnets.reduce(
    (acc, subnet) => {
      const vnId = subnet.spec?.virtualNetwork?.id;
      if (vnId) {
        acc[vnId] = (acc[vnId] || 0) + 1;
      }
      return acc;
    },
    {} as Record<string, number>,
  );

  const filteredVNs = virtualNetworks.filter((vn) => {
    const name = vn.metadata?.name ?? '';
    return !search || name.toLowerCase().includes(search.toLowerCase());
  });

  return (
    <>
      <ListPage
        title={t('Virtual networks')}
        description={t('Manage virtual networks for your compute instances.')}
        actions={
          <CreateButton onClick={() => setIsCreateModalOpen(true)}>
            {t('Create virtual network')}
          </CreateButton>
        }
      >
        <ListPageBody
          isLoading={isLoading || isLoadingNatGateways}
          error={error ?? natGatewaysError}
        >
          <Toolbar>
            <ToolbarContent>
              <ToolbarGroup>
                <ToolbarItem>
                  <SearchInput
                    placeholder={t('Search virtual networks by name…')}
                    value={search}
                    onChange={(_e, v) => setSearch(v)}
                    onClear={() => setSearch('')}
                  />
                </ToolbarItem>
              </ToolbarGroup>
            </ToolbarContent>
          </Toolbar>
          {filteredVNs.length === 0 ? (
            <SubtleContent component="p">
              {search
                ? t('No virtual networks match your search.')
                : t('No virtual networks yet. Create one to get started.')}
            </SubtleContent>
          ) : (
            <Table aria-label={t('Virtual networks')} variant="compact" borders>
              <Thead>
                <Tr>
                  <Th>{t('Name')}</Th>
                  <Th>{t('Status')}</Th>
                  <Th>{t('CIDR')}</Th>
                  <Th>{t('Subnets')}</Th>
                  <Th>{t('NAT gateway')}</Th>
                </Tr>
              </Thead>
              <Tbody>
                {filteredVNs.map((vn) => {
                  const subnetCount = subnetCountByVN[vn.id] || 0;
                  const natGatewayWithAddress = natGatewaysByVirtualNetworkId[vn.id];
                  const natGateway = natGatewayWithAddress?.natGateway;
                  const address = natGatewayWithAddress?.address;

                  return (
                    <Tr key={vn.id}>
                      <Td dataLabel={t('Name')}>
                        <ResourceNameField
                          resource={vn}
                          detailsUrl={`/networking/virtual-networks/${vn.id}`}
                        />
                      </Td>
                      <Td dataLabel={t('Status')}>
                        <VirtualNetworkStatusLabel state={vn.status?.state} />
                      </Td>
                      <Td dataLabel={t('CIDR')}>
                        <CidrDisplay ipv4Cidr={vn.spec?.ipv4Cidr} ipv6Cidr={vn.spec?.ipv6Cidr} />
                      </Td>
                      <Td dataLabel={t('Subnets')}>{subnetCount}</Td>
                      <Td dataLabel={t('NAT gateway')}>
                        {natGateway ? (
                          <>
                            <Content component="p">
                              {natGateway.metadata?.name ?? natGateway.id}
                            </Content>
                            <Content component="p">
                              <code>{address ?? '—'}</code>
                            </Content>
                          </>
                        ) : (
                          <Content component="p">—</Content>
                        )}
                      </Td>
                    </Tr>
                  );
                })}
              </Tbody>
            </Table>
          )}
        </ListPageBody>
      </ListPage>

      {isCreateModalOpen && (
        <VirtualNetworkCreateModal onClose={() => setIsCreateModalOpen(false)} />
      )}
    </>
  );
};
