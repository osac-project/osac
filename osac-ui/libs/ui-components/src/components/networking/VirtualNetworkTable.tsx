import { useMemo } from 'react';
import { Table, Tbody, Td, Th, Thead, Tr } from '@patternfly/react-table';

import type { VirtualNetwork } from '@osac/types';
import type { NatGatewayWithAddress } from '@osac/ui-components/api/v1/networking';
import VirtualNetworkActionsMenu from '@osac/ui-components/components/networking/VirtualNetworkActionsMenu';
import VirtualNetworkLoadedField from '@osac/ui-components/components/networking/VirtualNetworkLoadedField';

import { CidrDisplay } from './CidrDisplay';
import { getVirtualNetworkName } from './virtual-network-list-utils';
import VirtualNetworkNatGatewayCell from './VirtualNetworkNatGatewayCell';
import { VirtualNetworkStatusLabel } from './VirtualNetworkStatusLabel';
import { useTranslation } from '../../hooks/useTranslation';
import ResourceNameField from '../Resource/ResourceNameField';

interface VirtualNetworkTableProps {
  virtualNetworks: VirtualNetwork[];
  subnetCountByVirtualNetworkId: Record<string, number>;
  subnetsLoading?: boolean;
  subnetsError?: unknown;
  securityGroupCountByVirtualNetworkId: Record<string, number>;
  securityGroupsLoading?: boolean;
  securityGroupsError?: unknown;
  natGatewaysByVirtualNetworkId: Record<string, NatGatewayWithAddress>;
  natGatewaysLoading?: boolean;
  natGatewaysError?: unknown;
}

const VirtualNetworkTable = ({
  virtualNetworks,
  subnetCountByVirtualNetworkId,
  subnetsLoading,
  subnetsError,
  securityGroupCountByVirtualNetworkId,
  securityGroupsLoading,
  securityGroupsError,
  natGatewaysByVirtualNetworkId,
  natGatewaysLoading,
  natGatewaysError,
}: VirtualNetworkTableProps) => {
  const { t } = useTranslation();

  const columnLabels = useMemo(
    () => ({
      name: t('Name'),
      status: t('Status'),
      ipV4cidr: t('IPv4 CIDR'),
      subnets: t('Subnets'),
      securityGroups: t('Security groups'),
      natGateway: t('NAT gateway'),
      actions: t('Actions'),
    }),
    [t],
  );

  return (
    <Table aria-label={t('Virtual networks')} variant="compact">
      <Thead>
        <Tr>
          <Th>{columnLabels.name}</Th>
          <Th>{columnLabels.status}</Th>
          <Th>{columnLabels.ipV4cidr}</Th>
          <Th>{columnLabels.subnets}</Th>
          <Th>{columnLabels.securityGroups}</Th>
          <Th>{columnLabels.natGateway}</Th>
          <Th aria-label={columnLabels.actions} />
        </Tr>
      </Thead>
      <Tbody>
        {virtualNetworks.map((vn) => (
          <Tr key={vn.id}>
            <Td dataLabel={columnLabels.name}>
              <ResourceNameField
                resource={vn}
                title={getVirtualNetworkName(vn)}
                detailsUrl={`/networking/virtual-networks/${vn.id}`}
              />
            </Td>
            <Td dataLabel={columnLabels.status}>
              <VirtualNetworkStatusLabel state={vn.status?.state} />
            </Td>
            <Td dataLabel={columnLabels.ipV4cidr}>
              <CidrDisplay ipv4Cidr={vn.spec?.ipv4Cidr} />
            </Td>
            <Td dataLabel={columnLabels.subnets}>
              <VirtualNetworkLoadedField
                unavailableAriaLabel={t('Failed to load subnets')}
                isLoading={subnetsLoading}
                error={subnetsError}
              >
                {subnetCountByVirtualNetworkId[vn.id] || 0}
              </VirtualNetworkLoadedField>
            </Td>
            <Td dataLabel={columnLabels.securityGroups}>
              <VirtualNetworkLoadedField
                unavailableAriaLabel={t('Failed to load security groups')}
                isLoading={securityGroupsLoading}
                error={securityGroupsError}
              >
                {securityGroupCountByVirtualNetworkId[vn.id] || 0}
              </VirtualNetworkLoadedField>
            </Td>
            <Td dataLabel={columnLabels.natGateway}>
              <VirtualNetworkLoadedField
                unavailableAriaLabel={t('Failed to load NAT gateways')}
                isLoading={natGatewaysLoading}
                error={natGatewaysError}
              >
                <VirtualNetworkNatGatewayCell
                  natGatewayWithAddress={natGatewaysByVirtualNetworkId[vn.id]}
                />
              </VirtualNetworkLoadedField>
            </Td>
            <Td dataLabel={columnLabels.actions} isActionCell>
              <VirtualNetworkActionsMenu virtualNetwork={vn} />
            </Td>
          </Tr>
        ))}
      </Tbody>
    </Table>
  );
};

export default VirtualNetworkTable;
