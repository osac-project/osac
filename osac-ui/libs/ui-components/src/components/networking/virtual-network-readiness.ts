import { VirtualNetworkState } from '@osac/types';

import { isDefaultNetworkingResource } from '../../api/v1/networking';

interface NetworkState {
  status?: { state?: VirtualNetworkState };
}

interface NetworkOwnership {
  metadata?: { project?: string; labels?: Record<string, string> };
}

/** Only a READY network accepts new subnets. */
export const isReadyVirtualNetwork = (network?: NetworkState): boolean =>
  network?.status?.state === VirtualNetworkState.READY;

/**
 * Tenant defaults live in the tenant's default project (`""`), so a default
 * network of another project is not the tenant-wide default.
 */
export const isTenantDefaultVirtualNetwork = (network?: NetworkOwnership): boolean =>
  isDefaultNetworkingResource(network) && (network?.metadata?.project ?? '') === '';
