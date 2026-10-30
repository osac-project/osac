import { Skeleton } from '@patternfly/react-core';
import { Td, Tr } from '@patternfly/react-table';

import { SecurityGroups, Subnets, VirtualNetwork } from '@osac/types';
import { useListResource } from '@osac/ui-components/api/use-resource';
import {
  NatGatewayWithAddress,
  virtualNetworkScopeFilter,
} from '@osac/ui-components/api/v1/networking';
import VirtualNetworkActionsMenu from '@osac/ui-components/components/networking/VirtualNetworkActionsMenu';

import { CidrDisplay } from './CidrDisplay';
import { getVirtualNetworkName } from './virtual-network-list-utils';
import VirtualNetworkNatGatewayCell from './VirtualNetworkNatGatewayCell';
import { VirtualNetworkStatusLabel } from './VirtualNetworkStatusLabel';
import ResourceNameField from '../Resource/ResourceNameField';

interface VirtualNetworkTableRowProps {
  virtualNetwork: VirtualNetwork;
  natGatewayWithAddress?: NatGatewayWithAddress;
  columnLabels: { [key: string]: string };
}

const VirtualNetworkTableRow = ({
  virtualNetwork,
  natGatewayWithAddress,
  columnLabels,
}: VirtualNetworkTableRowProps) => {
  const { data: subnetTotals, isLoading: subnetsLoading } = useListResource(Subnets, {
    filter: virtualNetworkScopeFilter(virtualNetwork.id),
    limit: 0,
  });
  const subnetCount = subnetTotals?.total ?? 0;

  const { data: securityGroupTotals, isLoading: securityGroupsLoading } = useListResource(
    SecurityGroups,
    {
      filter: virtualNetworkScopeFilter(virtualNetwork.id),
      limit: 0,
    },
  );
  const securityGroupCount = securityGroupTotals?.total ?? 0;

  return (
    <Tr key={virtualNetwork.id}>
      <Td dataLabel={columnLabels.name}>
        <ResourceNameField
          resource={virtualNetwork}
          title={getVirtualNetworkName(virtualNetwork)}
          detailsUrl={`/networking/virtual-networks/${virtualNetwork.id}`}
        />
      </Td>
      <Td dataLabel={columnLabels.status}>
        <VirtualNetworkStatusLabel state={virtualNetwork.status?.state} />
      </Td>
      <Td dataLabel={columnLabels.ipV4cidr}>
        <CidrDisplay ipv4Cidr={virtualNetwork.spec?.ipv4Cidr} />
      </Td>
      <Td dataLabel={columnLabels.subnets}>
        {subnetsLoading ? <Skeleton width="25px" /> : subnetCount}
      </Td>
      <Td dataLabel={columnLabels.securityGroups}>
        {securityGroupsLoading ? <Skeleton width="25px" /> : securityGroupCount}
      </Td>
      <Td dataLabel={columnLabels.natGateway}>
        <VirtualNetworkNatGatewayCell natGatewayWithAddress={natGatewayWithAddress} />
      </Td>
      <Td dataLabel={columnLabels.actions} isActionCell>
        <VirtualNetworkActionsMenu virtualNetwork={virtualNetwork} />
      </Td>
    </Tr>
  );
};

export default VirtualNetworkTableRow;
