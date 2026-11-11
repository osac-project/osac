import { useCallback, useMemo } from 'react';

import type { ExternalIP, ExternalIPAttachment, ExternalIPPool, NATGateway } from '@osac/types';
import {
  ExternalIPAttachments,
  ExternalIPPools,
  ExternalIPState,
  ExternalIPs,
  NATGateways,
} from '@osac/types';

import { cel, escapeCelStringLiteral } from '../cel';
import type { CelFilter } from '../cel';
import { useListAllResources } from '../use-resource';

const AUTO_CREATED_LABEL = 'osac.openshift.io/auto-created';
const DEFAULT_LABEL = 'osac.openshift.io/default';

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

export const computeInstanceAttachmentFilter = (computeInstanceId: string) =>
  cel<ExternalIPAttachment>(
    () =>
      `this.spec.compute_instance.id == "${escapeCelStringLiteral(computeInstanceId)}"` as CelFilter<ExternalIPAttachment>,
  );

export const clusterAttachmentFilter = (clusterId: string) =>
  cel<ExternalIPAttachment>((filter) =>
    // spec.cluster is a protobuf oneof case that cannot be addressed by the typed field() builder.
    filter.and(
      `this.spec.cluster.id == "${escapeCelStringLiteral(clusterId)}"` as CelFilter<ExternalIPAttachment>,
    ),
  );

export const attachedTargetFromAttachment = (
  attachment: ExternalIPAttachment,
): ExternalIpAttachedTarget | undefined => {
  const target = attachment.spec?.target;
  if (target?.case === 'computeInstance' && target.value.id) {
    return {
      kind: 'computeInstance',
      name: target.value.name || target.value.id,
      href: `/vms/${encodeURIComponent(target.value.id)}`,
    };
  }
  if (target?.case === 'cluster' && target.value.id) {
    return {
      kind: 'cluster',
      name: target.value.name || target.value.id,
      href: `/clusters/${encodeURIComponent(target.value.id)}`,
    };
  }
  if (target?.case === 'baremetalInstance' && target.value.id) {
    return {
      kind: 'baremetalInstance',
      name: target.value.name || target.value.id,
      href: `/bare-metal/${encodeURIComponent(target.value.id)}`,
    };
  }
  return undefined;
};

export const attachedTargetFromNatGateway = (
  natGateway: NATGateway,
): ExternalIpAttachedTarget | undefined => {
  const virtualNetworkId = natGateway.spec?.virtualNetwork?.id;
  const name = natGateway.metadata?.name || natGateway.id;
  if (!natGateway.id || !virtualNetworkId || !name) {
    return undefined;
  }
  return {
    kind: 'natGateway',
    name,
    // A virtual network has one NAT gateway, so its NAT gateway tab is the gateway's page.
    href: `/networking/virtual-networks/${encodeURIComponent(virtualNetworkId)}?tab=nat-gateway`,
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

/**
 * IDs of the ExternalIPs that a NATGateway or an ExternalIPAttachment references.
 * `status.attached` alone is not enough: the server sets it only for a READY
 * ExternalIPAttachment, never for a NATGateway.
 */
export const buildConsumedExternalIpIds = (
  natGateways: readonly NATGateway[],
  attachments: readonly ExternalIPAttachment[],
): Set<string> =>
  new Set(
    uniqueIds([
      ...natGateways.map((natGateway) => natGateway.spec?.externalIp?.id),
      ...attachments.map((attachment) => attachment.spec?.externalIp?.id),
    ]),
  );

/**
 * The picker rule for an ExternalIP: ALLOCATED, not automatic, not a tenant
 * default, and without a consumer.
 */
export const isSelectableExternalIp = (
  externalIp: ExternalIP,
  consumedExternalIpIds: ReadonlySet<string>,
): boolean =>
  Boolean(externalIp.id) &&
  externalIp.status?.state === ExternalIPState.EXTERNAL_IP_STATE_ALLOCATED &&
  externalIp.status?.attached !== true &&
  externalIp.metadata?.labels?.[AUTO_CREATED_LABEL] === undefined &&
  externalIp.metadata?.labels?.[DEFAULT_LABEL] === undefined &&
  !consumedExternalIpIds.has(externalIp.id);

/** Loads every NATGateway and ExternalIPAttachment to tell which ExternalIPs are in use. */
export const useExternalIpConsumers = () => {
  const natGatewaysQuery = useListAllResources(NATGateways);
  const attachmentsQuery = useListAllResources(ExternalIPAttachments);
  const natGateways = natGatewaysQuery.data?.items;
  const attachments = attachmentsQuery.data?.items;
  const consumedExternalIpIds = useMemo(
    () => buildConsumedExternalIpIds(natGateways ?? [], attachments ?? []),
    [natGateways, attachments],
  );
  const isSelectable = useCallback(
    (externalIp: ExternalIP) => isSelectableExternalIp(externalIp, consumedExternalIpIds),
    [consumedExternalIpIds],
  );

  return {
    natGateways: natGateways ?? [],
    consumedExternalIpIds,
    isSelectable,
    isLoading: natGatewaysQuery.isLoading || attachmentsQuery.isLoading,
    error: natGatewaysQuery.error ?? attachmentsQuery.error,
  };
};

export const buildPoolsById = (pools: readonly ExternalIPPool[]): Record<string, ExternalIPPool> =>
  Object.fromEntries(pools.map((pool) => [pool.id, pool]));

export const useExternalIpJoins = (externalIps: readonly ExternalIP[]) => {
  const enabled = externalIps.length > 0;
  // Every pool, NAT gateway and attachment, without an ID filter: a filter listing every
  // displayed ID outgrows the server's filter size limit on a long list. The NAT gateway and
  // attachment lists are the ones the in-use checks load, so they come from the same cache.
  const poolsQuery = useListAllResources(ExternalIPPools, undefined, { enabled });
  const attachmentsQuery = useListAllResources(ExternalIPAttachments, undefined, { enabled });
  const natGatewaysQuery = useListAllResources(NATGateways, undefined, { enabled });

  return {
    poolsById: useMemo(() => buildPoolsById(poolsQuery.data?.items ?? []), [poolsQuery.data]),
    attachedTargetsByExternalIpId: useMemo(
      () =>
        buildAttachedTargetsByExternalIpId(
          attachmentsQuery.data?.items ?? [],
          natGatewaysQuery.data?.items ?? [],
        ),
      [attachmentsQuery.data, natGatewaysQuery.data],
    ),
    isLoading: poolsQuery.isLoading || attachmentsQuery.isLoading || natGatewaysQuery.isLoading,
    error: poolsQuery.error ?? attachmentsQuery.error ?? natGatewaysQuery.error,
  };
};

/** The External IPs list has no page controls, so it loads every page of the filter. */
export const useExternalIpsData = ({ filter }: { filter?: string } = {}) => {
  const externalIpsQuery = useListAllResources(ExternalIPs, filter);
  const externalIps = useMemo(() => externalIpsQuery.data?.items ?? [], [externalIpsQuery.data]);
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
