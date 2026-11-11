export const NAT_GATEWAYS_LIST_PATH = '/networking/nat-gateways';

/**
 * The create route of the NAT gateways list; Create and Cancel return to the list.
 * The route under the virtual network returns to its NAT gateway tab instead.
 */
export const natGatewayCreatePath = (virtualNetworkId: string) =>
  `${NAT_GATEWAYS_LIST_PATH}/create/${encodeURIComponent(virtualNetworkId)}`;

/** A virtual network has at most one NAT gateway, so its NAT gateway tab is the gateway's page. */
export const natGatewayTabPath = (virtualNetworkId: string) =>
  `/networking/virtual-networks/${encodeURIComponent(virtualNetworkId)}?tab=nat-gateway`;
