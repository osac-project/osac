import type { TFunction } from 'i18next';

/**
 * Why a VirtualNetwork cannot be deleted, from the dependents the UI has
 * already loaded. The server's deletion guard is authoritative; this only
 * spares the user a round trip. Returns undefined when the UI knows of no
 * dependents, so the confirmation (and then the server) decides.
 */
export const virtualNetworkDeleteBlockedReason = (
  t: TFunction,
  dependents: {
    subnetCount: number;
    securityGroupCount: number;
    natGatewayName?: string;
    fabricDomainCount?: number;
  },
): string | undefined => {
  const parts: string[] = [];
  if (dependents.subnetCount > 0) {
    parts.push(t('{{count}} subnet', { count: dependents.subnetCount }));
  }
  if (dependents.securityGroupCount > 0) {
    parts.push(t('{{count}} security group', { count: dependents.securityGroupCount }));
  }
  if (dependents.natGatewayName) {
    parts.push(t('the NAT gateway {{name}}', { name: dependents.natGatewayName }));
  }
  if ((dependents.fabricDomainCount ?? 0) > 0) {
    parts.push(t('{{count}} fabric domain', { count: dependents.fabricDomainCount }));
  }

  if (parts.length === 0) {
    return undefined;
  }
  return t('Delete {{dependents}} first.', { dependents: parts.join(', ') });
};
