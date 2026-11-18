import type { VirtualNetwork } from '@osac/types';

import { isVirtualNetworkReady } from './virtualNetworkReadiness';

/** The VirtualNetworks a picker may offer: only READY networks. */
export const selectableVirtualNetworks = (
  virtualNetworks: readonly VirtualNetwork[],
): VirtualNetwork[] =>
  virtualNetworks.filter((virtualNetwork) => isVirtualNetworkReady(virtualNetwork));
