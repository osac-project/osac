import { useMemo } from 'react';

import type { ExternalIP, ExternalIPAttachment, ExternalIPPool, NATGateway } from '@osac/types';
import { ExternalIPAttachments, ExternalIPPools, ExternalIPs, NATGateways } from '@osac/types';

import { cel, escapeCelStringLiteral } from '../cel';
import type { CelFilter } from '../cel';
import { type ListParams } from '../types';
import { useListResource } from '../use-resource';

export type ExternalIpAttachedTargetKind =
  | 'computeInstance'
  | 'cluster'
  | 'baremetalInstance'
  | 'natGateway';

export interface ExternalIpAttachedTarget {
  kind: ExternalIpAttachedTargetKind;
  name: string;
  href: string;
}

export const uniqueIds = (values: Array<string | undefined>): string[] => [
  ...new Set(values.filter((id): id is string => Boolean(id))),
];

export const poolIdsFilter = (ids: readonly string[]) =>
  cel<ExternalIPPool>((filter) => filter.field('id').isIn(ids));

export const attachmentExternalIpIdsFilter = (ids: readonly string[]) =>
  cel<ExternalIPAttachment>((filter) => filter.field('spec.externalIp.id').isIn(ids));

export const computeInstanceAttachmentFilter = (computeInstanceId: string) =>
  `this.spec.compute_instance.id == "${escapeCelStringLiteral(computeInstanceId)}"` as CelFilter;

export const natGatewayExternalIpIdsFilter = (ids: readonly string[]) =>
  cel<NATGateway>((filter) => filter.field('spec.externalIp.id').isIn(ids));

export const externalIpPoolId = (externalIp: ExternalIP): string | undefined =>
  externalIp.spec?.pool?.id || externalIp.status?.pool || undefined;

export const attachedTargetFromAttachment = (
  attachment: ExternalIPAttachment,
): ExternalIpAttachedTarget | undefined => {
  const target = attachment.spec?.target;
  if (target?.case === 'computeInstance' && target.value.id) {
    return {
      kind: 'computeInstance',
      name: target.value.name,
      href: `/vms/${encodeURIComponent(target.value.id)}`,
    };
  }
  if (target?.case === 'cluster' && target.value.id) {
    return {
      kind: 'cluster',
      name: target.value.name,
      href: `/clusters/${encodeURIComponent(target.value.id)}`,
    };
  }
  if (target?.case === 'baremetalInstance' && target.value.id) {
    return {
      kind: 'baremetalInstance',
      name: target.value.name,
      href: `/bare-metal/${encodeURIComponent(target.value.id)}`,
    };
  }
  return undefined;
};

export const attachedTargetFromNatGateway = (
  natGateway: NATGateway,
): ExternalIpAttachedTarget | undefined => {
  const virtualNetworkId = natGateway.spec?.virtualNetwork?.id;
  if (!natGateway.id || !virtualNetworkId) {
    return undefined;
  }
  const name = natGateway.metadata?.name || natGateway.spec?.virtualNetwork?.name || natGateway.id;
  return {
    kind: 'natGateway',
    name,
    href: `/networking/virtual-networks/${encodeURIComponent(virtualNetworkId)}`,
  };
};

export const buildAttachedTargetsByExternalIpId = (
  attachments: readonly ExternalIPAttachment[],
  natGateways: readonly NATGateway[] = [],
): Record<string, ExternalIpAttachedTarget> => {
  const byExternalIpId: Record<string, ExternalIpAttachedTarget> = {};
  for (const natGateway of natGateways) {
    const externalIpId = natGateway.spec?.externalIp?.id;
    const target = attachedTargetFromNatGateway(natGateway);
    if (externalIpId && target) {
      byExternalIpId[externalIpId] = target;
    }
  }
  for (const attachment of attachments) {
    const externalIpId = attachment.spec?.externalIp?.id;
    const target = attachedTargetFromAttachment(attachment);
    if (externalIpId && target) {
      byExternalIpId[externalIpId] = target;
    }
  }
  return byExternalIpId;
};

export const buildPoolsById = (pools: readonly ExternalIPPool[]): Record<string, ExternalIPPool> =>
  Object.fromEntries(pools.map((pool) => [pool.id, pool]));

export const useExternalIpJoins = (externalIps: readonly ExternalIP[]) => {
  const poolIds = useMemo(() => uniqueIds(externalIps.map(externalIpPoolId)), [externalIps]);
  const externalIpIds = useMemo(
    () => externalIps.map((externalIp) => externalIp.id),
    [externalIps],
  );

  const poolsQuery = useListResource(
    ExternalIPPools,
    poolIds.length > 0 ? { filter: poolIdsFilter(poolIds), limit: poolIds.length } : {},
    { enabled: poolIds.length > 0 },
  );
  const attachmentsQuery = useListResource(
    ExternalIPAttachments,
    externalIpIds.length > 0
      ? { filter: attachmentExternalIpIdsFilter(externalIpIds), limit: externalIpIds.length }
      : {},
    { enabled: externalIpIds.length > 0 },
  );
  const natGatewaysQuery = useListResource(
    NATGateways,
    externalIpIds.length > 0
      ? { filter: natGatewayExternalIpIdsFilter(externalIpIds), limit: externalIpIds.length }
      : {},
    { enabled: externalIpIds.length > 0 },
  );

  return {
    poolsById: useMemo(
      () => buildPoolsById(poolsQuery.data?.items ?? []),
      [poolsQuery.data?.items],
    ),
    attachedTargetsByExternalIpId: useMemo(
      () =>
        buildAttachedTargetsByExternalIpId(
          attachmentsQuery.data?.items ?? [],
          natGatewaysQuery.data?.items ?? [],
        ),
      [attachmentsQuery.data?.items, natGatewaysQuery.data?.items],
    ),
    isLoading: poolsQuery.isLoading || attachmentsQuery.isLoading || natGatewaysQuery.isLoading,
    error: poolsQuery.error ?? attachmentsQuery.error ?? natGatewaysQuery.error,
  };
};

export const useExternalIpsData = (params: ListParams = {}) => {
  const externalIpsQuery = useListResource(ExternalIPs, params);
  const externalIps = externalIpsQuery.data?.items ?? [];
  const joins = useExternalIpJoins(externalIps);
  const hasDisplayedIps = externalIps.length > 0;

  return {
    externalIps,
    poolsById: joins.poolsById,
    attachedTargetsByExternalIpId: joins.attachedTargetsByExternalIpId,
    isLoading: externalIpsQuery.isLoading || (hasDisplayedIps && joins.isLoading),
    error: externalIpsQuery.error ?? joins.error,
  };
};
