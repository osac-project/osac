import { useMemo } from 'react';
import { type MessageInitShape } from '@bufbuild/protobuf';
import { useMutation } from '@tanstack/react-query';

import {
  type ExternalIP,
  ExternalIPState,
  ExternalIPs,
  type NATGateway,
  NATGateways,
  type SecurityGroup,
  SecurityGroupSchema,
  SecurityGroupState,
  SecurityGroups,
  type Subnet,
  SubnetSchema,
  SubnetState,
  Subnets,
  type VirtualNetwork,
  VirtualNetworkSchema,
  VirtualNetworkState,
  VirtualNetworks,
} from '@osac/types';

import { useApiFetch } from '../api-context';
import { type CelFilter, cel } from '../cel';
import { type ListParams, apiQueryKey } from '../types';
import { type ApiQueryClient, useApiQuery, useApiQueryClient } from '../use-api-query';
import { useListAllResources, useListResource } from '../use-resource';

type NetworkingQueryOptions = {
  enabled?: boolean;
};

export interface NatGatewayWithAddress {
  natGateway: NATGateway;
  address?: string;
}

const LIST_ALL_PAGE_SIZE = 100;

interface ListPage<Item> {
  items: Item[];
  total: number;
}

/**
 * Loads every page of a list. The networking lists feed counts and joins, so a
 * single page would silently drop resources.
 */
export const listAllPages = async <Item>(
  listPage: (request: {
    filter?: string;
    limit: number;
    offset: number;
  }) => Promise<ListPage<Item>>,
  filter?: string,
): Promise<{ items: Item[] }> => {
  let page = await listPage({ filter, limit: LIST_ALL_PAGE_SIZE, offset: 0 });
  const items = [...page.items];
  while (items.length < page.total && page.items.length > 0) {
    page = await listPage({ filter, limit: LIST_ALL_PAGE_SIZE, offset: items.length });
    items.push(...page.items);
  }
  return { items };
};

export const useVirtualNetworks = (
  params: Pick<ListParams, 'filter'> = {},
  options: NetworkingQueryOptions = {},
) => {
  const client = useApiFetch(VirtualNetworks);
  return useApiQuery({
    queryKey: apiQueryKey('v1/virtual_networks', undefined, params),
    queryFn: () => listAllPages((request) => client.list(request), params.filter),
    select: (data) => data.items,
    enabled: options.enabled ?? true,
  });
};

export const useSubnets = (
  params: Pick<ListParams, 'filter'> = {},
  options: NetworkingQueryOptions = {},
) => {
  const client = useApiFetch(Subnets);
  return useApiQuery({
    queryKey: apiQueryKey('v1/subnets', undefined, params),
    queryFn: () => listAllPages((request) => client.list(request), params.filter),
    select: (data) => data.items,
    enabled: options.enabled ?? true,
  });
};

export const useSecurityGroups = (
  params: Pick<ListParams, 'filter'> = {},
  options: NetworkingQueryOptions = {},
) => {
  const client = useApiFetch(SecurityGroups);
  return useApiQuery({
    queryKey: apiQueryKey('v1/security_groups', undefined, params),
    queryFn: () => listAllPages((request) => client.list(request), params.filter),
    select: (data) => data.items,
    enabled: options.enabled ?? true,
  });
};

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

export const defaultSubnetListFilter = (project: string) =>
  cel<Subnet>((filter) =>
    filter.and(
      DEFAULT_NETWORK_LABEL_FILTER as CelFilter<Subnet>,
      filter.field('metadata.project').equals(project),
      filter.field('status.state').equals(SubnetState.READY),
      'has(this.spec.ipv4_cidr)' as CelFilter<Subnet>,
    ),
  );

export const defaultSecurityGroupListFilter = (project: string, virtualNetworkId: string) =>
  cel<SecurityGroup>((filter) =>
    filter.and(
      DEFAULT_NETWORK_LABEL_FILTER as CelFilter<SecurityGroup>,
      filter.field('metadata.project').equals(project),
      filter.field('spec.virtualNetwork.id').equals(virtualNetworkId),
      filter.field('status.state').equals(SecurityGroupState.READY),
    ),
  );

// Server side of the ExternalIP picker rule. A NATGateway consumer never sets
// status.attached, so callers must also drop the IDs from useExternalIpConsumers.
export const unallocatedExternalIpFilter = (project?: string) =>
  cel<ExternalIP>((filter) =>
    filter.and(
      project !== undefined ? filter.field('metadata.project').equals(project) : undefined,
      filter.field('status.state').equals(ExternalIPState.EXTERNAL_IP_STATE_ALLOCATED),
      filter.field('status.attached').equals(false),
      '!("osac.openshift.io/auto-created" in this.metadata.labels)' as CelFilter<ExternalIP>,
      '!("osac.openshift.io/default" in this.metadata.labels)' as CelFilter<ExternalIP>,
    ),
  );

/**
 * Limits a list to one project and, when given, one tenant. Pickers use it so the first
 * page already holds only resources the server accepts.
 */
export const projectScopeFilter = (project: string, tenant?: string) =>
  cel<{ metadata?: { project?: string; tenant?: string } }>((filter) =>
    filter.and(
      filter.field('metadata.project').equals(project),
      tenant !== undefined ? filter.field('metadata.tenant').equals(tenant) : undefined,
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
  const natGatewaysQuery = useListAllResources(NATGateways);
  const natGateways = natGatewaysQuery.data?.items;
  const externalIpIds = useMemo(
    () =>
      (natGateways ?? [])
        .map((natGateway) => natGateway.spec?.externalIp?.id)
        .filter((id): id is string => Boolean(id)),
    [natGateways],
  );
  // NAT Gateways expose only External IP IDs. Fetch every page of exactly those records.
  const externalIpsQuery = useListAllResources(ExternalIPs, externalIpIdsFilter(externalIpIds), {
    enabled: externalIpIds.length > 0,
  });
  const externalIps = externalIpIds.length > 0 ? externalIpsQuery.data?.items : undefined;

  return {
    natGateways: natGateways ?? [],
    natGatewaysByVirtualNetworkId: useMemo(
      () => buildNatGatewaysByVirtualNetworkId(natGateways ?? [], externalIps ?? []),
      [natGateways, externalIps],
    ),
    addressByExternalIpId: useMemo(
      () => buildExternalIpAddressById(externalIps ?? []),
      [externalIps],
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

export const useVirtualNetwork = (id: string) => {
  const client = useApiFetch(VirtualNetworks);
  return useApiQuery({
    queryKey: apiQueryKey('v1/virtual_networks', [id]),
    queryFn: () => client.get({ id }),
    select: (data) => data.object,
    enabled: Boolean(id),
  });
};

export const useSubnet = (id: string) => {
  const client = useApiFetch(Subnets);
  return useApiQuery({
    queryKey: apiQueryKey('v1/subnets', [id]),
    queryFn: () => client.get({ id }),
    select: (data) => data.object,
    enabled: Boolean(id),
  });
};

export const useSecurityGroup = (id: string) => {
  const client = useApiFetch(SecurityGroups);
  return useApiQuery({
    queryKey: apiQueryKey('v1/security_groups', [id]),
    queryFn: () => client.get({ id }),
    select: (data) => data.object,
    enabled: Boolean(id),
  });
};

export const invalidateVirtualNetworksQueries = (qc: ApiQueryClient) =>
  qc.invalidateQueries({ queryKey: apiQueryKey('v1/virtual_networks') });

export const invalidateSubnetsQueries = (qc: ApiQueryClient) =>
  qc.invalidateQueries({ queryKey: apiQueryKey('v1/subnets') });

export const invalidateSecurityGroupsQueries = (qc: ApiQueryClient) =>
  qc.invalidateQueries({ queryKey: apiQueryKey('v1/security_groups') });

export const useCreateVirtualNetwork = () => {
  const client = useApiFetch(VirtualNetworks);
  const qc = useApiQueryClient();
  return useMutation({
    mutationFn: async (input: MessageInitShape<typeof VirtualNetworkSchema>) => {
      const resp = await client.create({
        object: input,
      });
      const vn = resp.object;
      if (!vn?.id) {
        throw new Error('Create response missing id');
      }
      return vn;
    },
    onSuccess: () => invalidateVirtualNetworksQueries(qc),
  });
};

export const useDeleteVirtualNetwork = () => {
  const client = useApiFetch(VirtualNetworks);
  const qc = useApiQueryClient();
  return useMutation({
    mutationFn: (id: string) => client.delete({ id }),
    onSuccess: () => invalidateVirtualNetworksQueries(qc),
  });
};

export const useCreateSubnet = () => {
  const client = useApiFetch(Subnets);
  const qc = useApiQueryClient();
  return useMutation({
    mutationFn: async (input: MessageInitShape<typeof SubnetSchema>) => {
      const resp = await client.create({
        object: input,
      });
      const subnet = resp.object;
      if (!subnet?.id) {
        throw new Error('Create response missing id');
      }
      return subnet;
    },
    onSuccess: () => invalidateSubnetsQueries(qc),
  });
};

export const useDeleteSubnet = () => {
  const client = useApiFetch(Subnets);
  const qc = useApiQueryClient();
  return useMutation({
    mutationFn: (id: string) => client.delete({ id }),
    onSuccess: () => invalidateSubnetsQueries(qc),
  });
};

export const securityGroupFilterForVirtualNetwork = (virtualNetworkId: string) =>
  cel<SecurityGroup>((filter) => filter.field('spec.virtualNetwork.id').equals(virtualNetworkId));

export const useCreateSecurityGroup = () => {
  const client = useApiFetch(SecurityGroups);
  const qc = useApiQueryClient();
  return useMutation({
    mutationFn: async (body: MessageInitShape<typeof SecurityGroupSchema>) => {
      const resp = await client.create({ object: body });
      const sg = resp.object;
      if (!sg?.id) {
        throw new Error('Create response missing id');
      }
      return sg;
    },
    onSuccess: () => invalidateSecurityGroupsQueries(qc),
  });
};

export const useUpdateSecurityGroup = () => {
  const client = useApiFetch(SecurityGroups);
  const qc = useApiQueryClient();
  return useMutation({
    mutationFn: ({ object }: { object: MessageInitShape<typeof SecurityGroupSchema> }) => {
      return client.update({ object });
    },
    onSuccess: () => invalidateSecurityGroupsQueries(qc),
  });
};

export const useDeleteSecurityGroup = () => {
  const client = useApiFetch(SecurityGroups);
  const qc = useApiQueryClient();
  return useMutation({
    mutationFn: (id: string) => client.delete({ id }),
    onSuccess: () => invalidateSecurityGroupsQueries(qc),
  });
};
