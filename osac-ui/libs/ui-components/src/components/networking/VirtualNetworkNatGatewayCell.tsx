import type { NatGatewayWithAddress } from '@osac/ui-components/api/v1/networking';

interface VirtualNetworkNatGatewayCellProps {
  natGatewayWithAddress?: NatGatewayWithAddress;
}

const VirtualNetworkNatGatewayCell = ({
  natGatewayWithAddress,
}: VirtualNetworkNatGatewayCellProps) => {
  const natGateway = natGatewayWithAddress?.natGateway;
  if (!natGateway) {
    return <>—</>;
  }

  const name = natGateway.metadata?.name ?? natGateway.id;
  const address = natGatewayWithAddress?.address;
  return (
    <span className="pf-v6-u-text-nowrap">
      {name}
      {address ? ` (${address})` : null}
    </span>
  );
};

export default VirtualNetworkNatGatewayCell;
