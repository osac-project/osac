import { type BareMetalInstanceCatalogItem, BareMetalInstanceCatalogItems } from '@osac/types';

import { useDeleteResource } from '../../../api/use-resource';
import { useTranslation } from '../../../hooks/useTranslation';
import DeleteResourceModal from '../../Resource/DeleteResourceModal';

interface BareMetalInstanceCatalogItemDeleteModalProps {
  item: BareMetalInstanceCatalogItem;
  onClose: VoidFunction;
  onSuccess: VoidFunction;
}

const BareMetalInstanceCatalogItemDeleteModal = ({
  item,
  onClose,
  onSuccess,
}: BareMetalInstanceCatalogItemDeleteModalProps) => {
  const { t } = useTranslation();
  const mutation = useDeleteResource(BareMetalInstanceCatalogItems);

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

export default BareMetalInstanceCatalogItemDeleteModal;
