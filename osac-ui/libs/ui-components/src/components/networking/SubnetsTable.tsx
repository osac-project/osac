import { Link } from 'react-router-dom';
import { Table, Tbody, Td, Th, Thead, Tr } from '@patternfly/react-table';

import type { Subnet, VirtualNetwork } from '@osac/types';

import NetworkingProjectName from './NetworkingProjectName';
import NetworkingResourceName from './NetworkingResourceName';
import StatusWithMessage from './StatusWithMessage';
import SubnetActionsMenu from './SubnetActionsMenu';
import { SubnetStatusLabel } from './SubnetStatusLabel';
import { useNetworkingTenants } from './useNetworkingTenants';
import { resourceDisplayName } from '../../api/v1/networking';
import { useTranslation } from '../../hooks/useTranslation';

interface SubnetsTableProps {
  subnets: Subnet[];
  /** When set, the table shows a Virtual network column resolved from these networks. */
  virtualNetworks?: VirtualNetwork[];
}

/** Subnets with their status, CIDR, parent network, project and (provider admin) tenant. */
const SubnetsTable = ({ subnets, virtualNetworks }: SubnetsTableProps) => {
  const { t } = useTranslation();
  const { isAdmin, tenantName } = useNetworkingTenants();

  return (
    <Table aria-label={t('Subnets')} variant="compact">
      <Thead>
        <Tr>
          <Th>{t('Name')}</Th>
          <Th>{t('Status')}</Th>
          <Th>{t('IPv4 CIDR')}</Th>
          {virtualNetworks ? <Th>{t('Virtual network')}</Th> : null}
          <Th>{t('Project')}</Th>
          {isAdmin ? <Th>{t('Tenant')}</Th> : null}
          <Th aria-label={t('Actions')} />
        </Tr>
      </Thead>
      <Tbody>
        {subnets.map((subnet) => {
          const vnId = subnet.spec?.virtualNetwork?.id ?? '';
          const vn = virtualNetworks?.find((item) => item.id === vnId);
          return (
            <Tr key={subnet.id}>
              <Td dataLabel={t('Name')}>
                <NetworkingResourceName resource={subnet} />
              </Td>
              <Td dataLabel={t('Status')} modifier="nowrap">
                <StatusWithMessage message={subnet.status?.message}>
                  <SubnetStatusLabel state={subnet.status?.state} />
                </StatusWithMessage>
              </Td>
              <Td dataLabel={t('IPv4 CIDR')}>{subnet.spec?.ipv4Cidr || '—'}</Td>
              {virtualNetworks ? (
                <Td dataLabel={t('Virtual network')}>
                  {vnId ? (
                    <Link to={`/networking/virtual-networks/${vnId}`}>
                      {resourceDisplayName(vn?.metadata, vnId)}
                    </Link>
                  ) : (
                    '—'
                  )}
                </Td>
              ) : null}
              <Td dataLabel={t('Project')}>
                <NetworkingProjectName
                  project={subnet.metadata?.project}
                  tenant={subnet.metadata?.tenant}
                />
              </Td>
              {isAdmin ? (
                <Td dataLabel={t('Tenant')} modifier="nowrap">
                  {tenantName(subnet.metadata?.tenant)}
                </Td>
              ) : null}
              <Td dataLabel={t('Actions')} isActionCell>
                <SubnetActionsMenu subnet={subnet} />
              </Td>
            </Tr>
          );
        })}
      </Tbody>
    </Table>
  );
};

export default SubnetsTable;
