import type { TFunction } from 'i18next';

import { type VirtualNetwork, VirtualNetworkState } from '@osac/types';
import { type CelFilter, cel } from '@osac/ui-components/api/cel';

export const getVirtualNetworkName = (virtualNetwork: VirtualNetwork) =>
  virtualNetwork.metadata?.name ?? virtualNetwork.id;

export const STATE_FILTER_PARAM = 'state';

export const STATE_FILTER_VALUES = ['ready', 'provisioning', 'failed', 'unknown'] as const;

export type VirtualNetworkStateFilterValue = (typeof STATE_FILTER_VALUES)[number];

export const isVirtualNetworkStateFilterValue = (
  value: string,
): value is VirtualNetworkStateFilterValue =>
  STATE_FILTER_VALUES.includes(value as VirtualNetworkStateFilterValue);

export const VIRTUAL_NETWORK_STATE_FILTER_TO_ENUM: Record<
  VirtualNetworkStateFilterValue,
  VirtualNetworkState
> = {
  ready: VirtualNetworkState.READY,
  provisioning: VirtualNetworkState.PENDING,
  failed: VirtualNetworkState.FAILED,
  unknown: VirtualNetworkState.UNSPECIFIED,
};

export const getVirtualNetworkStateFilterLabels = (
  t: TFunction,
): Record<VirtualNetworkStateFilterValue, string> => ({
  ready: t('Ready'),
  provisioning: t('Provisioning'),
  failed: t('Failed'),
  unknown: t('Unknown'),
});

export const buildVirtualNetworkListFilter = (
  stateFilters: VirtualNetworkState[],
  nameSearch: string,
): CelFilter<VirtualNetwork> | undefined =>
  cel<VirtualNetwork>((b) =>
    b.and(
      stateFilters.length > 0 ? b.field('status.state').isIn(stateFilters) : undefined,
      nameSearch ? b.field('metadata.name').contains(nameSearch) : undefined,
    ),
  );
