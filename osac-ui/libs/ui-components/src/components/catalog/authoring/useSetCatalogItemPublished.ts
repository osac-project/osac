import {
  BareMetalInstanceCatalogItems,
  ClusterCatalogItems,
  ComputeInstanceCatalogItems,
} from '@osac/types';

import { useUpdateResource } from '../../../api/use-resource';
import { useTranslation } from '../../../hooks/useTranslation';
import { getErrorMessage } from '../../../utils/error';
import { useToast } from '../../Toast/useToast';
import type { CatalogItem } from '../catalogItemDisplay';

interface SetCatalogItemPublishedResult {
  setPublished: (published: boolean) => void;
  isPending: boolean;
}

/** Publishes or unpublishes a catalog item by updating only its `published` field. */
export const useSetCatalogItemPublished = (item: CatalogItem): SetCatalogItemPublishedResult => {
  const { t } = useTranslation();
  const { addToast } = useToast();
  const updateVm = useUpdateResource(ComputeInstanceCatalogItems);
  const updateCluster = useUpdateResource(ClusterCatalogItems);
  const updateBm = useUpdateResource(BareMetalInstanceCatalogItems);

  const errorTitle = (published: boolean) =>
    published ? t('Failed to publish catalog item') : t('Failed to unpublish catalog item');

  const mutation =
    item.$typeName === 'osac.public.v1.ClusterCatalogItem'
      ? updateCluster
      : item.$typeName === 'osac.public.v1.BareMetalInstanceCatalogItem'
        ? updateBm
        : updateVm;

  return {
    isPending: mutation.isPending,
    setPublished: (published: boolean) =>
      mutation.mutate(
        { object: { id: item.id, published } },
        {
          onError: (error) =>
            addToast({
              variant: 'danger',
              title: errorTitle(published),
              description: getErrorMessage(error),
            }),
        },
      ),
  };
};
