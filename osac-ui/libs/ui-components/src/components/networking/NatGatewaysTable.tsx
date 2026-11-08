import { Link } from 'react-router-dom';
import { Table, Tbody, Td, Th, Thead, Tr } from '@patternfly/react-table';

import type { NATGateway, VirtualNetwork } from '@osac/types';

import NatGatewayActionsMenu from './NatGatewayActionsMenu';
import { natGatewayTabPath } from './natGatewayPaths';
import { NatGatewayStatusLabel } from './NatGatewayStatusLabel';
import NetworkingProjectName from './NetworkingProjectName';
import NetworkingResourceName from './NetworkingResourceName';
import StatusWithMessage from './StatusWithMessage';
import { useNetworkingTenants } from './useNetworkingTenants';
import { resourceDisplayName } from '../../api/v1/networking';
import { useTranslation } from '../../hooks/useTranslation';

interface NatGatewaysTableProps {
  natGateways: NATGateway[];
  virtualNetworks: VirtualNetwork[];
  addressByExternalIpId: Record<string, string>;
}

/**
 * NAT gateways with their status, virtual network, external IP, project and (provider admin)
 * tenant.
 */
const NatGatewaysTable = ({
  natGateways,
  virtualNetworks,
  addressByExternalIpId,
}: NatGatewaysTableProps) => {
  const { t } = useTranslation();
  const { isAdmin, tenantName } = useNetworkingTenants();

  return (
    <Table aria-label={t('NAT gateways')} variant="compact">
      <Thead>
        <Tr>
          <Th>{t('Name')}</Th>
          <Th>{t('Status')}</Th>
          {/* Short values leave this column narrow; wrap the header instead of cutting it. */}
          <Th modifier="wrap">{t('Virtual network')}</Th>
          <Th info={{ tooltip: t('Reachable where your provider routes it.') }}>
            {t('External IP')}
          </Th>
          <Th>{t('Project')}</Th>
          {isAdmin ? <Th>{t('Tenant')}</Th> : null}
          <Th aria-label={t('Actions')} />
        </Tr>
      </Thead>
      <Tbody>
        {natGateways.map((natGateway) => {
          const vnId = natGateway.spec?.virtualNetwork?.id ?? '';
          const vn = virtualNetworks.find((item) => item.id === vnId);
          const externalIpId = natGateway.spec?.externalIp?.id;
          const address = externalIpId ? addressByExternalIpId[externalIpId] : undefined;
          return (
            <Tr key={natGateway.id}>
              <Td dataLabel={t('Name')}>
                {/* A virtual network has one NAT gateway, so its tab is the gateway's page. */}
                <NetworkingResourceName
                  resource={natGateway}
                  detailsUrl={vnId ? natGatewayTabPath(vnId) : undefined}
                />
              </Td>
              <Td dataLabel={t('Status')} modifier="nowrap">
                <StatusWithMessage message={natGateway.status?.message}>
                  <NatGatewayStatusLabel state={natGateway.status?.state} />
                </StatusWithMessage>
              </Td>
              <Td dataLabel={t('Virtual network')}>
                {vnId ? (
                  <Link to={`/networking/virtual-networks/${vnId}`}>
                    {resourceDisplayName(vn?.metadata, vnId)}
                  </Link>
                ) : (
                  '—'
                )}
              </Td>
              <Td dataLabel={t('External IP')} modifier="nowrap">
                {address ? <code>{address}</code> : '—'}
              </Td>
              <Td dataLabel={t('Project')}>
                <NetworkingProjectName
                  project={natGateway.metadata?.project}
                  tenant={natGateway.metadata?.tenant}
                />
              </Td>
              {isAdmin ? (
                <Td dataLabel={t('Tenant')} modifier="nowrap">
                  {tenantName(natGateway.metadata?.tenant)}
                </Td>
              ) : null}
              <Td dataLabel={t('Actions')} isActionCell>
                <NatGatewayActionsMenu natGateway={natGateway} />
              </Td>
            </Tr>
          );
        })}
      </Tbody>
    </Table>
  );
};

export default NatGatewaysTable;
