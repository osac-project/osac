import { useBareMetalInstanceCatalogItem } from '../../../api/v1/baremetal-instance.ts';
import { useClusterCatalogItem } from '../../../api/v1/cluster-catalog-item.ts';
import { useComputeInstanceCatalogItem } from '../../../api/v1/compute-instance-catalog-item.ts';
import { type CatalogItemKind } from '../catalogItemDisplay.ts';

export const useCatalogItemByKind = (kind: CatalogItemKind | undefined, id: string | undefined) => {
  const vm = useComputeInstanceCatalogItem(kind === 'vm' ? id : undefined);
  const cluster = useClusterCatalogItem(kind === 'cluster' ? id?.trim() : undefined);
  const bm = useBareMetalInstanceCatalogItem(kind === 'bm' ? id : undefined);

  switch (kind) {
    case 'vm':
      return vm;
    case 'cluster':
      return cluster;
    case 'bm':
      return bm;
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
