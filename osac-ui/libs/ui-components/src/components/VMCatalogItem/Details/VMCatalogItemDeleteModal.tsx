import { type ComputeInstanceCatalogItem, ComputeInstanceCatalogItems } from '@osac/types';

import { useDeleteResource } from '../../../api/use-resource';
import { useTranslation } from '../../../hooks/useTranslation';
import DeleteResourceModal from '../../Resource/DeleteResourceModal';

interface VMCatalogItemDeleteModalProps {
  item: ComputeInstanceCatalogItem;
  onClose: VoidFunction;
  onSuccess: VoidFunction;
}

const VMCatalogItemDeleteModal = ({ item, onClose, onSuccess }: VMCatalogItemDeleteModalProps) => {
  const { t } = useTranslation();
  const mutation = useDeleteResource(ComputeInstanceCatalogItems);

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

export default VMCatalogItemDeleteModal;
