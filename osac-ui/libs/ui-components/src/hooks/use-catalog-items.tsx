import {
  BareMetalInstanceCatalogItem,
  ClusterCatalogItem,
  ComputeInstanceCatalogItem,
} from '@osac/types';
import { useBareMetalInstanceCatalogItems } from '@osac/ui-components/api/v1/baremetal-instance';
import {
  CatalogListFilterCriteria,
  buildCatalogListFilter,
} from '@osac/ui-components/api/v1/catalog-item-list-filter';
import { useClusterCatalogItems } from '@osac/ui-components/api/v1/cluster-catalog-item';
import { useComputeInstanceCatalogItems } from '@osac/ui-components/api/v1/compute-instance-catalog-item';
import { CatalogItemKind } from '@osac/ui-components/components/catalog/catalogItemDisplay';

export const useCatalogItems = (
  filterCriteria: CatalogListFilterCriteria,
  typeFilter: readonly CatalogItemKind[],
  fetchAllTypes: boolean,
): {
  error: unknown;
  isLoading: boolean;
  hasSuccessfulQuery: boolean;
  vms: ComputeInstanceCatalogItem[];
  clusters: ClusterCatalogItem[];
  bms: BareMetalInstanceCatalogItem[];
  typeCounts: Record<CatalogItemKind, number>;
  unfilteredTotalItems: number;
} => {
  const listFilter = buildCatalogListFilter(filterCriteria);
  const listParams = listFilter ? { filter: listFilter } : {};

  const shouldFetchKind = (kind: CatalogItemKind) => fetchAllTypes || typeFilter.includes(kind);

  const vmsQuery = useComputeInstanceCatalogItems(listParams, shouldFetchKind('vm'));
  const clustersQuery = useClusterCatalogItems(listParams, shouldFetchKind('cluster'));
  const bmsQuery = useBareMetalInstanceCatalogItems(listParams, shouldFetchKind('bm'));

  const vmsTotalQuery = useComputeInstanceCatalogItems({ limit: 0 });
  const clustersTotalQuery = useClusterCatalogItems({ limit: 0 });
  const bmsTotalQuery = useBareMetalInstanceCatalogItems({ limit: 0 });

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
    vms: shouldFetchKind('vm') ? (vmsQuery.data?.items ?? []) : [],
    clusters: shouldFetchKind('cluster') ? (clustersQuery.data?.items ?? []) : [],
    bms: shouldFetchKind('bm') ? (bmsQuery.data?.items ?? []) : [],
    typeCounts: {
      vm: totalVmsCount,
      cluster: totalClustersCount,
      bm: totalBmsCount,
    },
    unfilteredTotalItems: totalVmsCount + totalClustersCount + totalBmsCount,
  };
};
