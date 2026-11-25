import type { Subnet } from '@osac/types';

import { type CelFilter, cel } from '../../api/cel';

export const SUBNETS_LIST_PATH = '/networking/subnets';
export const SUBNETS_CREATE_PATH = `${SUBNETS_LIST_PATH}/create`;

/** URL parameter of the list's virtual-network filter, reused to pre-fill the create page. */
export const VIRTUAL_NETWORK_FILTER_PARAM = 'virtualNetwork';

export const virtualNetworkDetailsPath = (id: string): string =>
  `/networking/virtual-networks/${encodeURIComponent(id)}`;

export const subnetsCreatePath = (virtualNetworkId?: string): string =>
  virtualNetworkId
    ? `${SUBNETS_CREATE_PATH}?${VIRTUAL_NETWORK_FILTER_PARAM}=${encodeURIComponent(virtualNetworkId)}`
    : SUBNETS_CREATE_PATH;

/** Where Create and Cancel return: the network the create started from, or the list. */
export const subnetReturnPath = (virtualNetworkId?: string): string =>
  virtualNetworkId ? virtualNetworkDetailsPath(virtualNetworkId) : SUBNETS_LIST_PATH;

export const buildSubnetListFilter = (
  projectFilter: CelFilter<Subnet> | undefined,
  search: string,
  virtualNetworkId?: string,
): CelFilter<Subnet> | undefined =>
  cel<Subnet>((builder) =>
    builder.and(
      projectFilter,
      search ? builder.field('metadata.name').contains(search) : undefined,
      virtualNetworkId
        ? builder.field('spec.virtualNetwork.id').equals(virtualNetworkId)
        : undefined,
    ),
  );
