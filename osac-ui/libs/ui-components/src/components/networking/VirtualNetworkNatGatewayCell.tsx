import { Link } from 'react-router-dom';

import type { NatGatewayWithAddress } from '@osac/ui-components/api/v1/networking';

import { natGatewayTabPath } from './natGatewayPaths';

interface VirtualNetworkNatGatewayCellProps {
  natGatewayWithAddress?: NatGatewayWithAddress;
}

/**
 * A virtual network's NAT gateway on one line: its name and, in parentheses, its address.
 * The name opens the virtual network's NAT gateway tab; an external IP has no page.
 */
const VirtualNetworkNatGatewayCell = ({
  natGatewayWithAddress,
}: VirtualNetworkNatGatewayCellProps) => {
  const natGateway = natGatewayWithAddress?.natGateway;
  const address = natGatewayWithAddress?.address;

  if (!natGateway) {
    return <>—</>;
  }

  const name = natGateway.metadata?.name || natGateway.id;
  const virtualNetworkId = natGateway.spec?.virtualNetwork?.id;
  return (
    <span className="pf-v6-u-text-nowrap">
      {virtualNetworkId ? <Link to={natGatewayTabPath(virtualNetworkId)}>{name}</Link> : name}
      {address ? (
        <>
          {' ('}
          <code>{address}</code>
          {')'}
        </>
      ) : null}
    </span>
  );
};

export default VirtualNetworkNatGatewayCell;
