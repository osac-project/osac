import type { TFunction } from 'i18next';

import { type VirtualNetwork, VirtualNetworkState } from '@osac/types';

export const isVirtualNetworkReady = (virtualNetwork?: VirtualNetwork): boolean =>
  virtualNetwork?.status?.state === VirtualNetworkState.READY;

/**
 * Why a resource cannot be created in the virtual network yet, or undefined. The
 * server rejects children of a virtual network that is not READY.
 */
export const virtualNetworkNotReadyReason = (
  t: TFunction,
  virtualNetwork?: VirtualNetwork,
): string | undefined =>
  isVirtualNetworkReady(virtualNetwork)
    ? undefined
    : t('Available when the virtual network is Ready.');

export interface VirtualNetworkDependents {
  subnetCount: number;
  securityGroupCount: number;
  natGatewayName?: string;
  fabricDomainCount?: number;
}

/**
 * Why a virtual network cannot be deleted yet, from the dependents the UI already
 * knows, or undefined. The server's deletion guard still decides; it also checks
 * dependents the UI cannot see.
 */
export const virtualNetworkDeleteBlockedReason = (
  t: TFunction,
  {
    subnetCount,
    securityGroupCount,
    natGatewayName,
    fabricDomainCount = 0,
  }: VirtualNetworkDependents,
): string | undefined => {
  const dependents = [
    ...(subnetCount > 0 ? [t('subnets ({{total}})', { total: subnetCount })] : []),
    ...(securityGroupCount > 0
      ? [t('security groups ({{total}})', { total: securityGroupCount })]
      : []),
    ...(natGatewayName ? [t('NAT gateway {{name}}', { name: natGatewayName })] : []),
    ...(fabricDomainCount > 0
      ? [t('fabric domains ({{total}})', { total: fabricDomainCount })]
      : []),
  ];
  return dependents.length > 0
    ? t('Delete its dependents first: {{dependents}}.', { dependents: dependents.join(', ') })
    : undefined;
};

/**
 * The option label of a virtual network in a picker. A cloud provider admin sees every tenant's
 * networks, so the label adds the tenant to tell apart networks with the same name, such as each
 * tenant's "default".
 */
export const virtualNetworkOptionLabel = (
  vn: { id: string; metadata?: { name?: string; tenant?: string }; spec?: { ipv4Cidr?: string } },
  tenantName?: (tenantId?: string) => string,
): string => {
  const name = vn.metadata?.name || vn.id;
  const cidr = vn.spec?.ipv4Cidr ?? '—';
  return tenantName ? `${name} (${cidr}, ${tenantName(vn.metadata?.tenant)})` : `${name} (${cidr})`;
};
