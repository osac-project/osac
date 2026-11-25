import { useMemo } from 'react';

import { type Cluster, Clusters, type ComputeInstance, ComputeInstances } from '@osac/types';

import { useListAllResources } from '../use-resource';

export interface NetworkConsumers {
  /** Workload names attached to each subnet id; empty when no workload uses a subnet. */
  workloadNamesBySubnetId: ReadonlyMap<string, string[]>;
  isLoading: boolean;
  failed: boolean;
}

const workloadName = (workload: { id: string; metadata?: { name?: string } }): string =>
  workload.metadata?.name?.trim() || workload.id;

/**
 * Joins the workloads that reference a subnet: a compute instance reports its
 * attachments in `status.networkAttachments[].subnetRef`, and a cluster names its
 * subnet in `spec.networkAttachment.subnet.id`. Every page is loaded, so a
 * workload on a later page is not silently ignored.
 */
export const buildWorkloadNamesBySubnetId = (
  instances: readonly ComputeInstance[],
  clusters: readonly Cluster[],
): Map<string, string[]> => {
  const bySubnetId = new Map<string, string[]>();
  const add = (subnetId: string | undefined, name: string) => {
    if (!subnetId) {
      return;
    }
    const names = bySubnetId.get(subnetId) ?? [];
    if (!names.includes(name)) {
      names.push(name);
      bySubnetId.set(subnetId, names);
    }
  };

  for (const instance of instances) {
    const name = workloadName(instance);
    // The configured attachment is the intent; the status repeats it once the
    // instance reports its network, so both are joined and de-duplicated.
    for (const attachment of instance.spec?.networkAttachments ?? []) {
      add(attachment.subnet?.id, name);
    }
    for (const attachment of instance.status?.computeNetworkAttachmentStatuses ?? []) {
      add(attachment.subnetRef, name);
    }
  }
  for (const cluster of clusters) {
    add(cluster.spec?.networkAttachment?.subnet?.id, workloadName(cluster));
  }

  return bySubnetId;
};

export const useNetworkConsumers = (): NetworkConsumers => {
  const instancesQuery = useListAllResources(ComputeInstances);
  const clustersQuery = useListAllResources(Clusters);

  const workloadNamesBySubnetId = useMemo(
    () =>
      buildWorkloadNamesBySubnetId(
        instancesQuery.data?.items ?? [],
        clustersQuery.data?.items ?? [],
      ),
    [instancesQuery.data?.items, clustersQuery.data?.items],
  );

  return {
    workloadNamesBySubnetId,
    isLoading: instancesQuery.isLoading || clustersQuery.isLoading,
    failed: Boolean(instancesQuery.error || clustersQuery.error),
  };
};
