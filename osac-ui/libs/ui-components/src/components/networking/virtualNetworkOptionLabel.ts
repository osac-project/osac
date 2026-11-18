import type { VirtualNetwork } from '@osac/types';

/**
 * A VirtualNetwork option for a picker: the name, then the CIDR, then the
 * tenant for a provider admin who sees several tenants.
 */
export const virtualNetworkOptionLabel = (
  virtualNetwork: VirtualNetwork,
  tenantName?: string,
): string => {
  const name = virtualNetwork.metadata?.name || virtualNetwork.id;
  const cidr = virtualNetwork.spec?.ipv4Cidr;
  return [name, cidr, tenantName ? `tenant: ${tenantName}` : undefined]
    .filter((part): part is string => Boolean(part))
    .join(' · ');
};
