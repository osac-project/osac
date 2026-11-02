import {
  BareMetalInstanceCatalogItems,
  ClusterCatalogItems,
  ComputeInstanceCatalogItems,
} from '@osac/types';

import { useDeleteResource } from '../../../api/use-resource';
import { useTranslation } from '../../../hooks/useTranslation';
import DeleteResourceModal from '../../Resource/DeleteResourceModal';
import { type CatalogItem, selectCatalogItemValue } from '../catalogItemDisplay';

interface CatalogItemDeleteModalProps {
  item: CatalogItem;
  onClose: VoidFunction;
  onSuccess: VoidFunction;
}

const CatalogItemDeleteModal = ({ item, onClose, onSuccess }: CatalogItemDeleteModalProps) => {
  const { t } = useTranslation();
  const mutation = selectCatalogItemValue(item, {
    vm: useDeleteResource(ComputeInstanceCatalogItems),
    cluster: useDeleteResource(ClusterCatalogItems),
    bm: useDeleteResource(BareMetalInstanceCatalogItems),
  });

  return (
    <DeleteResourceModal
      resourceName={item.metadata?.name || ''}
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
