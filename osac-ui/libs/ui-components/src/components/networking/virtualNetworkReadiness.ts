import type { TFunction } from 'i18next';

import { type VirtualNetwork, VirtualNetworkState } from '@osac/types';

export const isVirtualNetworkReady = (virtualNetwork?: VirtualNetwork): boolean =>
  virtualNetwork?.status?.state === VirtualNetworkState.READY;

/**
 * Why a child of a VirtualNetwork cannot be created yet. Children can only be
 * created once the network is READY; the same copy is used by the tab creates.
 */
export const virtualNetworkNotReadyReason = (t: TFunction): string =>
  t('Available when the virtual network is Ready.');
