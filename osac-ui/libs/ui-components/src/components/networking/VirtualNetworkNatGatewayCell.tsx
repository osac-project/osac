import { Content } from '@patternfly/react-core';

import type { NatGatewayWithAddress } from '@osac/ui-components/api/v1/networking';

interface VirtualNetworkNatGatewayCellProps {
  natGatewayWithAddress?: NatGatewayWithAddress;
}

const VirtualNetworkNatGatewayCell = ({
  natGatewayWithAddress,
}: VirtualNetworkNatGatewayCellProps) => {
  const natGateway = natGatewayWithAddress?.natGateway;
  const address = natGatewayWithAddress?.address;

  if (!natGateway) {
    return <Content component="p">—</Content>;
  }

  return (
    <>
      <Content component="p">{natGateway.metadata?.name ?? natGateway.id}</Content>
      <Content component="p">
        <code>{address ?? '—'}</code>
      </Content>
    </>
  );
};

export default VirtualNetworkNatGatewayCell;
