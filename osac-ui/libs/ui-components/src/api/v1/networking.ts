import { useMemo } from 'react';

import {
  type ExternalIP,
  ExternalIPState,
  ExternalIPs,
  type NATGateway,
  NATGateways,
  type SecurityGroup,
  SecurityGroupState,
  type Subnet,
  SubnetState,
  type VirtualNetwork,
  VirtualNetworkState,
} from '@osac/types';

import { type CelFilter, cel } from '../cel';
import { useListResource } from '../use-resource';

export interface NatGatewayWithAddress {
  natGateway: NATGateway;
  address?: string;
}

// Scope + ready filter: use where only attachable subnets are valid (e.g. the VM
// provisioning wizard). Not for the detail page — it hides non-ready subnets.
export const virtualNetworkFilterForSubnetList = (virtualNetworkId: string) =>
  cel<Subnet>((filter) =>
    filter.and(
      filter.field('spec.virtualNetwork.id').equals(virtualNetworkId),
      filter.field('status.state').equals(SubnetState.READY),
    ),
  );

export const securityGroupFilterForVirtualNetworkList = (virtualNetworkId: string) =>
  cel<SecurityGroup>((filter) =>
    filter.and(
      filter.field('spec.virtualNetwork.id').equals(virtualNetworkId),
      filter.field('status.state').equals(SecurityGroupState.READY),
    ),
  );

export const VIRTUAL_NETWORK_READY_LIST_FILTER = cel<VirtualNetwork>((filter) =>
  filter.field('status.state').equals(VirtualNetworkState.READY),
);

const DEFAULT_NETWORK_LABEL_FILTER = 'this.metadata.labels["osac.openshift.io/default"] == "true"';

export const isDefaultNetworkingResource = (resource?: {
  metadata?: { labels?: Record<string, string> };
}): boolean => resource?.metadata?.labels?.['osac.openshift.io/default'] === 'true';

// Tenant defaults are tenant-wide: they live in the tenant's default project ("")
// and apply to every project of the tenant. The server scopes a tenant user's lists
// to their tenant, so the filters carry no tenant clause.
export const defaultSubnetListFilter = () =>
  cel<Subnet>((filter) =>
    filter.and(
      DEFAULT_NETWORK_LABEL_FILTER as CelFilter<Subnet>,
      filter.field('metadata.project').equals(''),
      filter.field('status.state').equals(SubnetState.READY),
      'has(this.spec.ipv4_cidr)' as CelFilter<Subnet>,
    ),
  );

export const defaultSecurityGroupListFilter = (virtualNetworkId: string) =>
  cel<SecurityGroup>((filter) =>
    filter.and(
      DEFAULT_NETWORK_LABEL_FILTER as CelFilter<SecurityGroup>,
      filter.field('metadata.project').equals(''),
      filter.field('spec.virtualNetwork.id').equals(virtualNetworkId),
      filter.field('status.state').equals(SecurityGroupState.READY),
    ),
  );

export const unallocatedExternalIpFilter = (project?: string) =>
  cel<ExternalIP>((filter) =>
    filter.and(
      project !== undefined ? filter.field('metadata.project').equals(project) : undefined,
      filter.field('status.state').equals(ExternalIPState.EXTERNAL_IP_STATE_ALLOCATED),
      filter.field('status.attached').equals(false),
    ),
  );

export const externalIpIdsFilter = (ids: readonly string[]) =>
  cel<ExternalIP>((filter) => filter.field('id').isIn(ids));

export const virtualNetworkScopeFilter = (virtualNetworkId: string) =>
  cel<Subnet>((filter) => filter.field('spec.virtualNetwork.id').equals(virtualNetworkId));

export const virtualNetworksScopeFilter = (virtualNetworkIds: string[]) =>
  cel<Subnet>((filter) => filter.field('spec.virtualNetwork.id').isIn(virtualNetworkIds));

const buildExternalIpAddressById = (externalIps: readonly ExternalIP[]) => {
  const byId: Record<string, string> = {};
  for (const ip of externalIps) {
    if (ip.id && ip.status?.address) {
      byId[ip.id] = ip.status.address;
    }
  }
  return byId;
};

const buildNatGatewaysByVirtualNetworkId = (
  natGateways: readonly NATGateway[],
  externalIps: readonly ExternalIP[],
): Record<string, NatGatewayWithAddress> => {
  const addressByExternalIpId = buildExternalIpAddressById(externalIps);
  const byVirtualNetworkId: Record<string, NatGatewayWithAddress> = {};

  for (const natGateway of natGateways) {
    const virtualNetworkId = natGateway.spec?.virtualNetwork?.id;
    if (virtualNetworkId && !byVirtualNetworkId[virtualNetworkId]) {
      byVirtualNetworkId[virtualNetworkId] = {
        natGateway,
        address: natGateway.spec?.externalIp?.id
          ? addressByExternalIpId[natGateway.spec.externalIp.id]
          : undefined,
      };
    }
  }

  return byVirtualNetworkId;
};

// NAT Gateway attachments reference the External IP by ID instead of exposing its address,
// so this hook joins the virtual-network-scoped NAT Gateway and External IP query results.
export const useNatGateway = (virtualNetworkId: string) => {
  const natGatewaysQuery = useListResource(NATGateways, {
    filter: virtualNetworkScopeFilter(virtualNetworkId),
  });
  const { data: natGatewaysResponse } = natGatewaysQuery;
  const natGateway = natGatewaysResponse?.items?.[0];
  const externalIpId = natGateway?.spec?.externalIp?.id;
  // NAT Gateways expose only the External IP ID. Scope the join query to that ID
  // and request one result so pagination cannot omit the address we need.
  const externalIpsQuery = useListResource(
    ExternalIPs,
    externalIpId ? { filter: externalIpIdsFilter([externalIpId]), limit: 1 } : {},
    { enabled: Boolean(externalIpId) },
  );
  const { data: externalIpsResponse } = externalIpsQuery;
  const natAddress = externalIpsResponse?.items?.find((ip) => ip.id === externalIpId)?.status
    ?.address;

  return {
    natGateway,
    natAddress,
    isLoading: natGatewaysQuery.isLoading || externalIpsQuery.isLoading,
    error: natGatewaysQuery.error ?? externalIpsQuery.error,
  };
};

// The list view needs a display-ready NAT Gateway and address for each virtual network.
// Since those values come from separate backend resources, this hook joins both query results
// and indexes the result by virtual network ID for direct component lookup.
export const useNatGateways = () => {
  const natGatewaysQuery = useListResource(NATGateways);
  const { data: natGatewaysResponse } = natGatewaysQuery;
  const externalIpIds = useMemo(
    () =>
      (natGatewaysResponse?.items ?? [])
        .map((natGateway) => natGateway.spec?.externalIp?.id)
        .filter((id): id is string => Boolean(id)),
    [natGatewaysResponse?.items],
  );
  // NAT Gateways expose only External IP IDs. Fetch exactly those records and
  // size the page to the reference count so no joined address is omitted.
  const externalIpsQuery = useListResource(
    ExternalIPs,
    externalIpIds.length > 0
      ? { filter: externalIpIdsFilter(externalIpIds), limit: externalIpIds.length }
      : {},
    { enabled: externalIpIds.length > 0 },
  );
  const { data: externalIpsResponse } = externalIpsQuery;

  return {
    natGatewaysByVirtualNetworkId: useMemo(
      () =>
        buildNatGatewaysByVirtualNetworkId(
          natGatewaysResponse?.items ?? [],
          externalIpsResponse?.items ?? [],
        ),
      [natGatewaysResponse?.items, externalIpsResponse?.items],
    ),
    isLoading: natGatewaysQuery.isLoading || externalIpsQuery.isLoading,
    error: natGatewaysQuery.error ?? externalIpsQuery.error,
  };
};

export const resourceDisplayName = (metadata?: { name?: string }, id?: string): string =>
  metadata?.name?.trim() || id || '—';

export const formatResourceIdsForReview = (
  ids: string[],
  resources: Array<{ id: string; metadata?: { name?: string } }>,
): string => {
  if (ids.length === 0) {
    return '—';
  }

  return ids
    .map((id) => {
      const resource = resources.find((item) => item.id === id);
      return resourceDisplayName(resource?.metadata, id);
    })
    .join(', ');
};

export const formatResourceIdForReview = (
  id: string,
  resources: Array<{ id: string; metadata?: { name?: string } }>,
): string => formatResourceIdsForReview(id ? [id] : [], resources);

export const securityGroupFilterForVirtualNetwork = (virtualNetworkId: string) =>
  cel<SecurityGroup>((filter) => filter.field('spec.virtualNetwork.id').equals(virtualNetworkId));
