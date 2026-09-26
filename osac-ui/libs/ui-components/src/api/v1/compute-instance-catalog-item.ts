import { keepPreviousData } from '@tanstack/react-query';

import { type ComputeInstanceCatalogItem, ComputeInstanceCatalogItems } from '@osac/types';

import { useApiFetch } from '../api-context';
import { type ApiListResult, type ListParams, apiQueryKey } from '../types';
import { useApiQuery } from '../use-api-query';

export const useComputeInstanceCatalogItems = (params: ListParams = {}, enabled = true) => {
  const client = useApiFetch(ComputeInstanceCatalogItems);
  return useApiQuery<
    Awaited<ReturnType<typeof client.list>>,
    ApiListResult<ComputeInstanceCatalogItem>
  >({
    queryKey: apiQueryKey('v1/compute_instance_catalog_items', undefined, params),
    queryFn: () => client.list(params),
    placeholderData: keepPreviousData,
    select: (data) => ({
      items: data.items,
      size: data.size,
      total: data.total,
    }),
    enabled,
  });
};

export const useComputeInstanceCatalogItem = (id: string | undefined) => {
  const client = useApiFetch(ComputeInstanceCatalogItems);
  const trimmedId = id?.trim() ?? '';
  return useApiQuery({
    queryKey: apiQueryKey('v1/compute_instance_catalog_items', trimmedId ? [trimmedId] : undefined),
    queryFn: () => client.get({ id: trimmedId }),
    select: (data) => data.object,
    enabled: Boolean(trimmedId),
  });
};
