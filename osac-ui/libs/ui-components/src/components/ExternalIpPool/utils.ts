import type { TFunction } from 'i18next';

import { type ExternalIPPool, ExternalIPPoolState } from '@osac/types/private';
import { type CelFilter, cel } from '@osac/ui-components/api/cel';

export const STATE_FILTER_VALUES = ['pending', 'ready', 'failed', 'deleting'] as const;

export type ExternalIpPoolStateFilterValue = (typeof STATE_FILTER_VALUES)[number];

export const STATE_FILTER_PARAM = 'state';

export const isExternalIpPoolStateFilterValue = (
  value: string,
): value is ExternalIpPoolStateFilterValue =>
  STATE_FILTER_VALUES.includes(value as ExternalIpPoolStateFilterValue);

export const EXTERNAL_IP_POOL_STATE_FILTER_TO_ENUM: Record<
  ExternalIpPoolStateFilterValue,
  ExternalIPPoolState
> = {
  pending: ExternalIPPoolState.EXTERNAL_IP_POOL_STATE_PENDING,
  ready: ExternalIPPoolState.EXTERNAL_IP_POOL_STATE_READY,
  failed: ExternalIPPoolState.EXTERNAL_IP_POOL_STATE_FAILED,
  deleting: ExternalIPPoolState.EXTERNAL_IP_POOL_STATE_DELETING,
};

export const getExternalIpPoolStateLabels = (
  t: TFunction,
): Record<ExternalIPPoolState, string> => ({
  [ExternalIPPoolState.EXTERNAL_IP_POOL_STATE_UNSPECIFIED]: t('Unknown'),
  [ExternalIPPoolState.EXTERNAL_IP_POOL_STATE_PENDING]: t('Provisioning'),
  [ExternalIPPoolState.EXTERNAL_IP_POOL_STATE_READY]: t('Ready'),
  [ExternalIPPoolState.EXTERNAL_IP_POOL_STATE_FAILED]: t('Failed'),
  [ExternalIPPoolState.EXTERNAL_IP_POOL_STATE_DELETING]: t('Deleting'),
});

export const buildExternalIpPoolListFilter = (
  stateFilters: ExternalIPPoolState[],
  search: string,
): CelFilter<ExternalIPPool> | undefined =>
  cel<ExternalIPPool>((b) =>
    b.and(
      stateFilters.length > 0 ? b.field('status.state').isIn(stateFilters) : undefined,
      search ? b.field('metadata.name').contains(search) : undefined,
    ),
  );

export const formatExternalIpPoolCidrs = (t: TFunction, cidrs?: string[]): string => {
  if (!cidrs || cidrs.length === 0) {
    return '—';
  }
  if (cidrs.length === 1) {
    return cidrs[0];
  }
  return t('{{count}} CIDRs', { count: cidrs.length });
};

const SHARED_TENANT = 'shared';
const EMPTY_VALUE = '—';

export const getPoolTenant = (pool: ExternalIPPool, t: TFunction): string => {
  const tenant = pool.metadata?.tenant;
  return tenant === SHARED_TENANT ? t('Shared') : (tenant ?? EMPTY_VALUE);
};

const formatCount = (value?: bigint): string => (value === undefined ? EMPTY_VALUE : `${value}`);

export const getPoolCapacity = (pool: ExternalIPPool, t: TFunction) =>
  t('{{available}} of {{capacity}} available', {
    available: formatCount(pool.status?.available),
    capacity: formatCount(pool.status?.total),
  });
