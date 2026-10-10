import {
  BareMetalInstanceCatalogItem,
  BareMetalInstanceCatalogItems,
  ClusterCatalogItem,
  ClusterCatalogItems,
  ComputeInstanceCatalogItem,
  ComputeInstanceCatalogItems,
  ServiceTier,
} from '@osac/types';
import { useListResource } from '@osac/ui-components/api/use-resource';
import {
  CatalogListFilterCriteria,
  buildCatalogListFilter,
} from '@osac/ui-components/api/v1/catalog-item-list-filter';
import { useSession } from '@osac/ui-components/hooks/use-session';

export const useCatalogItems = (
  filterCriteria: CatalogListFilterCriteria,
  typeFilter: readonly ServiceTier[],
  fetchAllTypes: boolean,
): {
  error: unknown;
  isLoading: boolean;
  hasSuccessfulQuery: boolean;
  vms: ComputeInstanceCatalogItem[];
  clusters: ClusterCatalogItem[];
  bms: BareMetalInstanceCatalogItem[];
  typeCounts: Record<ServiceTier, number>;
  unfilteredTotalItems: number;
} => {
  const { enabledServices } = useSession();
  const listFilter = buildCatalogListFilter(filterCriteria);
  const listParams = listFilter ? { filter: listFilter } : {};

  const shouldFetchKind = (kind: ServiceTier) =>
    enabledServices.includes(kind) && (fetchAllTypes || typeFilter.includes(kind));

  const vmsQuery = useListResource(ComputeInstanceCatalogItems, listParams, {
    enabled: shouldFetchKind(ServiceTier.VMAAS),
  });
  const clustersQuery = useListResource(ClusterCatalogItems, listParams, {
    enabled: shouldFetchKind(ServiceTier.CAAS),
  });
  const bmsQuery = useListResource(BareMetalInstanceCatalogItems, listParams, {
    enabled: shouldFetchKind(ServiceTier.BMAAS),
  });

  const vmsTotalQuery = useListResource(
    ComputeInstanceCatalogItems,
    { limit: 0 },
    { enabled: enabledServices.includes(ServiceTier.VMAAS) },
  );
  const clustersTotalQuery = useListResource(
    ClusterCatalogItems,
    { limit: 0 },
    { enabled: enabledServices.includes(ServiceTier.CAAS) },
  );
  const bmsTotalQuery = useListResource(
    BareMetalInstanceCatalogItems,
    { limit: 0 },
    { enabled: enabledServices.includes(ServiceTier.BMAAS) },
  );

  const isLoading =
    vmsQuery.isLoading ||
    clustersQuery.isLoading ||
    bmsQuery.isLoading ||
    vmsTotalQuery.isLoading ||
    clustersTotalQuery.isLoading ||
    bmsTotalQuery.isLoading;

  const error =
    vmsQuery.error ||
    clustersQuery.error ||
    bmsQuery.error ||
    vmsTotalQuery.error ||
    clustersTotalQuery.error ||
    bmsTotalQuery.error;

  const hasSuccessfulQuery = [vmsQuery, clustersQuery, bmsQuery].some(
    (query) => !query.isLoading && !query.error,
  );

  const totalVmsCount = vmsTotalQuery.data?.total ?? 0;
  const totalBmsCount = bmsTotalQuery.data?.total ?? 0;
  const totalClustersCount = clustersTotalQuery.data?.total ?? 0;

  return {
    error,
    isLoading,
    hasSuccessfulQuery,
    vms: shouldFetchKind(ServiceTier.VMAAS) ? (vmsQuery.data?.items ?? []) : [],
    clusters: shouldFetchKind(ServiceTier.CAAS) ? (clustersQuery.data?.items ?? []) : [],
    bms: shouldFetchKind(ServiceTier.BMAAS) ? (bmsQuery.data?.items ?? []) : [],
    typeCounts: {
      [ServiceTier.VMAAS]: totalVmsCount,
      [ServiceTier.CAAS]: totalClustersCount,
      [ServiceTier.BMAAS]: totalBmsCount,
      [ServiceTier.UNSPECIFIED]: 0,
      [ServiceTier.MAAS]: 0,
    },
    unfilteredTotalItems: totalVmsCount + totalClustersCount + totalBmsCount,
  };
};
