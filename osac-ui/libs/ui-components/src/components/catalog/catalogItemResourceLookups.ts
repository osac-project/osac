import type { BareMetalInstanceType, ClusterVersion, DiskImage, InstanceType } from '@osac/types';
import { BareMetalInstanceTypes, InstanceTypes } from '@osac/types';
import { useListResource } from '@osac/ui-components/api/use-resource';
import { useClusterVersions } from '@osac/ui-components/api/v1/cluster-versions';
import { useDiskImages } from '@osac/ui-components/api/v1/disk-image';

export interface CatalogItemResourceLookups {
  diskImages: DiskImage[];
  computeInstanceTypes: InstanceType[];
  bareMetalInstanceTypes: BareMetalInstanceType[];
  clusterVersions: ClusterVersion[];
}

type UseCatalogItemResourceLookupsOptions = {
  enabled?: boolean;
};

export const useCatalogItemResourceLookups = (
  options: UseCatalogItemResourceLookupsOptions = {},
): CatalogItemResourceLookups => {
  const enabled = options.enabled ?? true;
  const { data: diskImages = [] } = useDiskImages({}, { enabled });
  const { data: computeInstanceTypes } = useListResource(InstanceTypes, {}, { enabled });
  const { data: bareMetalInstanceTypesData } = useListResource(
    BareMetalInstanceTypes,
    {},
    { enabled },
  );
  const { data: clusterVersions = [] } = useClusterVersions({}, { enabled });

  return {
    diskImages,
    computeInstanceTypes: computeInstanceTypes?.items ?? [],
    bareMetalInstanceTypes: bareMetalInstanceTypesData?.items ?? [],
    clusterVersions,
  };
};
