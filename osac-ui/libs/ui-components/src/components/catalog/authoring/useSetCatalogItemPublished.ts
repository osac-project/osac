import {
  BareMetalInstanceCatalogItems,
  ClusterCatalogItems,
  ComputeInstanceCatalogItems,
} from '@osac/types';

import { useUpdateResource } from '../../../api/use-resource';
import { useTranslation } from '../../../hooks/useTranslation';
import { getErrorMessage } from '../../../utils/error';
import { useToast } from '../../Toast/useToast';
import { type CatalogItem, selectCatalogItemValue } from '../catalogItemDisplay';

interface SetCatalogItemPublishedResult {
  setPublished: (published: boolean) => void;
  isPending: boolean;
}

/** Publishes or unpublishes a catalog item by updating only its `published` field. */
export const useSetCatalogItemPublished = (item: CatalogItem): SetCatalogItemPublishedResult => {
  const { t } = useTranslation();
  const { addToast } = useToast();
  const mutation = selectCatalogItemValue(item, {
    vm: useUpdateResource(ComputeInstanceCatalogItems),
    cluster: useUpdateResource(ClusterCatalogItems),
    bm: useUpdateResource(BareMetalInstanceCatalogItems),
  });

  const errorTitle = (published: boolean) =>
    published ? t('Failed to publish catalog item') : t('Failed to unpublish catalog item');

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
