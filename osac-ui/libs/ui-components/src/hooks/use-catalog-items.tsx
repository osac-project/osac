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
  typeCounts: Partial<Record<ServiceTier, number>>;
} => {
  const { enabledServices, role } = useSession();
  const visibilityCriteria: CatalogListFilterCriteria =
    role === 'tenant-user' ? { published: 'published' } : {};
  const listFilter = buildCatalogListFilter({
    ...filterCriteria,
    ...visibilityCriteria,
  });
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

  const isLoading = vmsQuery.isLoading || clustersQuery.isLoading || bmsQuery.isLoading;

  const fetchedQueries = [
    { kind: ServiceTier.VMAAS, enabled: shouldFetchKind(ServiceTier.VMAAS), query: vmsQuery },
    { kind: ServiceTier.CAAS, enabled: shouldFetchKind(ServiceTier.CAAS), query: clustersQuery },
    { kind: ServiceTier.BMAAS, enabled: shouldFetchKind(ServiceTier.BMAAS), query: bmsQuery },
  ].filter((entry) => entry.enabled);

  const selectedQueries = fetchedQueries.filter((entry) => typeFilter.includes(entry.kind));
  const statusQueries = selectedQueries.length > 0 ? selectedQueries : fetchedQueries;
  const error = statusQueries.find(({ query }) => query.error)?.query.error;

  // A kind that was never fetched (not enabled, or not in typeFilter) reports
  // isLoading: false and error: null, which would otherwise count as a vacuous
  // success below and enable search before the fetched kind actually resolves.
  const hasSuccessfulQuery =
    statusQueries.length === 0 ||
    statusQueries.some(({ query }) => !query.isLoading && !query.error);

  const totalVmsCount = vmsQuery.error ? undefined : vmsQuery.data?.total;
  const totalBmsCount = bmsQuery.error ? undefined : bmsQuery.data?.total;
  const totalClustersCount = clustersQuery.error ? undefined : clustersQuery.data?.total;

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
  };
};
