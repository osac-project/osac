import type { SecurityGroup } from '@osac/types';
import { type CelFilter, cel } from '@osac/ui-components/api/cel';

export const SECURITY_GROUPS_LIST_PATH = '/networking/security-groups';

export const securityGroupDetailsPath = (id: string) =>
  `${SECURITY_GROUPS_LIST_PATH}/${encodeURIComponent(id)}`;

export const buildSecurityGroupListFilter = (
  search: string,
): CelFilter<SecurityGroup> | undefined =>
  cel<SecurityGroup>((group) =>
    search ? group.field('metadata.name').contains(search) : undefined,
  );

export const uniqueVirtualNetworkIds = (securityGroups: SecurityGroup[]): string[] => [
  ...new Set(
    securityGroups
      .map((group) => group.spec?.virtualNetwork?.id)
      .filter((id): id is string => Boolean(id)),
  ),
];
