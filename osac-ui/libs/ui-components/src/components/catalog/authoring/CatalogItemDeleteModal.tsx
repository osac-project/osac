import {
  BareMetalInstanceCatalogItems,
  ClusterCatalogItems,
  ComputeInstanceCatalogItems,
} from '@osac/types';

import { useDeleteResource } from '../../../api/use-resource';
import { useTranslation } from '../../../hooks/useTranslation';
import DeleteResourceModal from '../../Resource/DeleteResourceModal';
import type { CatalogItem } from '../catalogItemDisplay';

interface CatalogItemDeleteModalProps {
  item: CatalogItem;
  onClose: VoidFunction;
  onSuccess: VoidFunction;
}

const CatalogItemDeleteModal = ({ item, onClose, onSuccess }: CatalogItemDeleteModalProps) => {
  const { t } = useTranslation();
  const deleteVm = useDeleteResource(ComputeInstanceCatalogItems);
  const deleteCluster = useDeleteResource(ClusterCatalogItems);
  const deleteBm = useDeleteResource(BareMetalInstanceCatalogItems);

  const mutation =
    item.$typeName === 'osac.public.v1.ClusterCatalogItem'
      ? deleteCluster
      : item.$typeName === 'osac.public.v1.BareMetalInstanceCatalogItem'
        ? deleteBm
        : deleteVm;

  return (
    <DeleteResourceModal
      resourceName={item.metadata?.name ?? item.id}
      label={t(
        'This permanently deletes the catalog item. It does not affect resources that tenants have already provisioned from it.',
      )}
      errorLabel={t('Failed to delete catalog item')}
      onClose={onClose}
      onSuccess={onSuccess}
      mutation={mutation}
      variables={{ id: item.id }}
    />
  );
};

export default CatalogItemDeleteModal;
