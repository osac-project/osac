import { useMemo } from 'react';
import { Table, Tbody, Th, Thead, Tr } from '@patternfly/react-table';

import type { VirtualNetwork } from '@osac/types';
import type { NatGatewayWithAddress } from '@osac/ui-components/api/v1/networking';
import VirtualNetworkTableRow from '@osac/ui-components/components/networking/VirtualNetworkTableRow';

import { useTranslation } from '../../hooks/useTranslation';

interface VirtualNetworkTableProps {
  virtualNetworks: VirtualNetwork[];
  natGatewaysByVirtualNetworkId: Record<string, NatGatewayWithAddress>;
}

const VirtualNetworkTable = ({
  virtualNetworks,
  natGatewaysByVirtualNetworkId,
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
          <VirtualNetworkTableRow
            key={vn.id}
            columnLabels={columnLabels}
            virtualNetwork={vn}
            natGatewayWithAddress={natGatewaysByVirtualNetworkId[vn.id]}
          />
        ))}
      </Tbody>
    </Table>
  );
};

export default VirtualNetworkTable;
