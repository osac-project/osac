import { useMemo } from 'react';

import {
  type BareMetalInstance,
  BareMetalInstances,
  type Cluster,
  Clusters,
  type ComputeInstance,
  ComputeInstances,
  FabricDomains,
  ServiceTier,
} from '@osac/types';

import { useSession } from '../../hooks/use-session';
import { cel } from '../cel';
import { useListAllResources } from '../use-resource';
import { useSubnets, virtualNetworkScopeFilter } from './networking';

export type NetworkConsumerKind = 'computeInstance' | 'baremetalInstance' | 'cluster';

/** A workload and the Subnets and SecurityGroups its stored network attachments reference. */
export interface NetworkConsumer {
  kind: NetworkConsumerKind;
  id: string;
  name: string;
  subnetIds: string[];
  securityGroupIds: string[];
}

interface AttachmentRefs {
  subnet?: { id?: string };
  securityGroups?: Array<{ id?: string }>;
}

const toConsumer = (
  kind: NetworkConsumerKind,
  workload: { id: string; metadata?: { name?: string } },
  attachments: readonly (AttachmentRefs | undefined)[],
): NetworkConsumer => ({
  kind,
  id: workload.id,
  name: workload.metadata?.name || workload.id,
  subnetIds: attachments.flatMap((a) => (a?.subnet?.id ? [a.subnet.id] : [])),
  securityGroupIds: attachments.flatMap((a) =>
    (a?.securityGroups ?? []).flatMap((sg) => (sg.id ? [sg.id] : [])),
  ),
});

export const buildNetworkConsumers = ({
  computeInstances = [],
  bareMetalInstances = [],
  clusters = [],
}: {
  computeInstances?: readonly ComputeInstance[];
  bareMetalInstances?: readonly BareMetalInstance[];
  clusters?: readonly Cluster[];
}): NetworkConsumer[] => [
  ...computeInstances.map((vm) =>
    toConsumer('computeInstance', vm, vm.spec?.networkAttachments ?? []),
  ),
  ...bareMetalInstances.map((bm) =>
    toConsumer('baremetalInstance', bm, bm.spec?.networkAttachments ?? []),
  ),
  ...clusters.map((cluster) => toConsumer('cluster', cluster, [cluster.spec?.networkAttachment])),
];

/**
 * Loads every workload of the enabled services (every page) and the Subnets and
 * SecurityGroups their network attachments reference. Deletion guards on the server
 * also count workloads the caller cannot see; the UI only knows the visible ones.
 */
export const useNetworkConsumers = () => {
  const { enabledServices } = useSession();
  const vms = useListAllResources(ComputeInstances, undefined, {
    enabled: enabledServices.includes(ServiceTier.VMAAS),
  });
  const bms = useListAllResources(BareMetalInstances, undefined, {
    enabled: enabledServices.includes(ServiceTier.BMAAS),
  });
  const clusters = useListAllResources(Clusters, undefined, {
    enabled: enabledServices.includes(ServiceTier.CAAS),
  });

  const consumers = useMemo(
    () =>
      buildNetworkConsumers({
        computeInstances: vms.data?.items,
        bareMetalInstances: bms.data?.items,
        clusters: clusters.data?.items,
      }),
    [vms.data, bms.data, clusters.data],
  );

  return {
    consumers,
    isLoading: vms.isLoading || bms.isLoading || clusters.isLoading,
  };
};

/**
 * Clusters whose network attachment is on a Subnet of the virtual network. Loads only
 * Clusters and that network's Subnets, and only when CaaS is enabled.
 */
export const useClustersOnVirtualNetwork = (virtualNetworkId?: string) => {
  const { enabledServices } = useSession();
  const enabled = Boolean(virtualNetworkId) && enabledServices.includes(ServiceTier.CAAS);
  const clustersQuery = useListAllResources(Clusters, undefined, { enabled });
  const subnetsQuery = useSubnets(
    virtualNetworkId ? { filter: virtualNetworkScopeFilter(virtualNetworkId) } : {},
    { enabled },
  );
  const clusters = useMemo(() => {
    const subnetIds = new Set((subnetsQuery.data ?? []).map((s) => s.id));
    return buildNetworkConsumers({ clusters: clustersQuery.data?.items }).filter((c) =>
      c.subnetIds.some((id) => subnetIds.has(id)),
    );
  }, [clustersQuery.data, subnetsQuery.data]);
  return { clusters, isLoading: clustersQuery.isLoading || subnetsQuery.isLoading };
};

export const fabricDomainsOfVirtualNetworkFilter = (virtualNetworkId: string) =>
  cel<{ spec?: { virtualNetwork?: string } }>((filter) =>
    filter.field('spec.virtualNetwork').equals(virtualNetworkId),
  );

/** FabricDomains of the virtual network; loaded only when bare metal is enabled. */
export const useFabricDomainsOfVirtualNetwork = (virtualNetworkId: string) => {
  const { enabledServices } = useSession();
  return useListAllResources(FabricDomains, fabricDomainsOfVirtualNetworkFilter(virtualNetworkId), {
    enabled: Boolean(virtualNetworkId) && enabledServices.includes(ServiceTier.BMAAS),
  });
};
