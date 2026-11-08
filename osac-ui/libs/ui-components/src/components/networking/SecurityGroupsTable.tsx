import { Link } from 'react-router-dom';
import { Table, Tbody, Td, Th, Thead, Tr } from '@patternfly/react-table';

import type { SecurityGroup, VirtualNetwork } from '@osac/types';

import NetworkingProjectName from './NetworkingProjectName';
import NetworkingResourceName from './NetworkingResourceName';
import { securityGroupStatusMessage } from './securityGroupRuleUtils';
import { SecurityGroupStatusLabel } from './SecurityGroupStatusLabel';
import StatusWithMessage from './StatusWithMessage';
import { useNetworkingTenants } from './useNetworkingTenants';
import { resourceDisplayName } from '../../api/v1/networking';
import { useTranslation } from '../../hooks/useTranslation';

interface SecurityGroupsTableProps {
  securityGroups: SecurityGroup[];
  /** When set, the table shows a Virtual network column resolved from these networks. */
  virtualNetworks?: VirtualNetwork[];
}

/**
 * Security groups with their status, rule counts, parent network, project and (provider
 * admin) tenant.
 */
const SecurityGroupsTable = ({ securityGroups, virtualNetworks }: SecurityGroupsTableProps) => {
  const { t } = useTranslation();
  const { isAdmin, tenantName } = useNetworkingTenants();

  return (
    <Table aria-label={t('Security groups')} variant="compact">
      <Thead>
        <Tr>
          <Th>{t('Name')}</Th>
          <Th>{t('Status')}</Th>
          {virtualNetworks ? <Th>{t('Virtual network')}</Th> : null}
          <Th modifier="wrap">{t('Inbound rules')}</Th>
          <Th modifier="wrap">{t('Outbound rules')}</Th>
          <Th>{t('Project')}</Th>
          {isAdmin ? <Th>{t('Tenant')}</Th> : null}
        </Tr>
      </Thead>
      <Tbody>
        {securityGroups.map((sg) => {
          const vnId = sg.spec?.virtualNetwork?.id ?? '';
          const vn = virtualNetworks?.find((item) => item.id === vnId);
          return (
            <Tr key={sg.id}>
              <Td dataLabel={t('Name')}>
                <NetworkingResourceName
                  resource={sg}
                  detailsUrl={`/networking/security-groups/${sg.id}`}
                />
              </Td>
              <Td dataLabel={t('Status')} modifier="nowrap">
                <StatusWithMessage message={securityGroupStatusMessage(sg)}>
                  <SecurityGroupStatusLabel state={sg.status?.state} message={sg.status?.message} />
                </StatusWithMessage>
              </Td>
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
              <Td dataLabel={t('Inbound rules')}>{sg.spec?.ingress?.length ?? 0}</Td>
              <Td dataLabel={t('Outbound rules')}>{sg.spec?.egress?.length ?? 0}</Td>
              <Td dataLabel={t('Project')}>
                <NetworkingProjectName
                  project={sg.metadata?.project}
                  tenant={sg.metadata?.tenant}
                />
              </Td>
              {isAdmin ? (
                <Td dataLabel={t('Tenant')} modifier="nowrap">
                  {tenantName(sg.metadata?.tenant)}
                </Td>
              ) : null}
            </Tr>
          );
        })}
      </Tbody>
    </Table>
  );
};

export default SecurityGroupsTable;
