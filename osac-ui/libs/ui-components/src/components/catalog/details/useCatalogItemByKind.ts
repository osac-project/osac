import {
  BareMetalInstanceCatalogItems,
  ClusterCatalogItems,
  ComputeInstanceCatalogItems,
} from '@osac/types';

import { useGetResource } from '../../../api/use-resource';
import { type CatalogItemKind } from '../catalogItemDisplay.ts';

export const useCatalogItemByKind = (kind: CatalogItemKind | undefined, id: string) => {
  const vm = useGetResource(ComputeInstanceCatalogItems, { id }, { enabled: kind === 'vm' });
  const cluster = useGetResource(ClusterCatalogItems, { id }, { enabled: kind === 'cluster' });
  const bm = useGetResource(BareMetalInstanceCatalogItems, { id }, { enabled: kind === 'bm' });

  switch (kind) {
    case 'vm':
      return { ...vm, data: vm.data?.object };
    case 'cluster':
      return { ...cluster, data: cluster.data?.object };
    case 'bm':
      return { ...bm, data: bm.data?.object };
    default:
      return {
        data: undefined,
        isLoading: false,
        isError: false,
        error: undefined,
        refetch: () => undefined,
      };
  }
};
