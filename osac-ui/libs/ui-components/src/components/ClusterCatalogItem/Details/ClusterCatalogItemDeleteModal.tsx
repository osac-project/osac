import { type ClusterCatalogItem, ClusterCatalogItems } from '@osac/types';

import { useDeleteResource } from '../../../api/use-resource';
import { useTranslation } from '../../../hooks/useTranslation';
import DeleteResourceModal from '../../Resource/DeleteResourceModal';

interface ClusterCatalogItemDeleteModalProps {
  item: ClusterCatalogItem;
  onClose: VoidFunction;
  onSuccess: VoidFunction;
}

const ClusterCatalogItemDeleteModal = ({
  item,
  onClose,
  onSuccess,
}: ClusterCatalogItemDeleteModalProps) => {
  const { t } = useTranslation();
  const mutation = useDeleteResource(ClusterCatalogItems);

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

export default ClusterCatalogItemDeleteModal;
