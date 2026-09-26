import { keepPreviousData } from '@tanstack/react-query';

import { type ClusterCatalogItem, ClusterCatalogItems } from '@osac/types';

import { useApiFetch } from '../api-context';
import { type ApiListResult, type ListParams, apiQueryKey } from '../types';
import { useApiQuery } from '../use-api-query';

export const useClusterCatalogItems = (params: ListParams = {}, enabled = true) => {
  const client = useApiFetch(ClusterCatalogItems);
  return useApiQuery<Awaited<ReturnType<typeof client.list>>, ApiListResult<ClusterCatalogItem>>({
    queryKey: apiQueryKey('v1/cluster_catalog_items', undefined, params),
    placeholderData: keepPreviousData,
    queryFn: () => client.list(params),
    select: (data) => ({
      items: data.items,
      size: data.size,
      total: data.total,
    }),
    enabled,
  });
};

export const useClusterCatalogItem = (id: string | undefined) => {
  const client = useApiFetch(ClusterCatalogItems);
  return useApiQuery({
    queryKey: apiQueryKey('v1/cluster_catalog_items', id ? [id] : undefined),
    queryFn: () => client.get({ id: id ?? '' }),
    select: (data) => data.object,
    enabled: Boolean(id),
  });
};
