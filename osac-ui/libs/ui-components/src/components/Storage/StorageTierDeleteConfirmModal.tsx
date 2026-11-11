import { type StorageTier, StorageTiers } from '@osac/types/private';
import { useDeleteResource } from '@osac/ui-components/api/use-resource';
import DeleteResourceModal from '@osac/ui-components/components/Resource/DeleteResourceModal.tsx';

import { useTranslation } from '../../hooks/useTranslation';

interface StorageTierDeleteConfirmModalProps {
  tier: StorageTier;
  onClose: () => void;
  onSuccess: () => void;
}

const StorageTierDeleteConfirmModal = ({
  tier,
  onClose,
  onSuccess,
}: StorageTierDeleteConfirmModalProps) => {
  const { t } = useTranslation();
  const deleteTier = useDeleteResource(StorageTiers);
  const tierName = tier.metadata?.name ?? tier.id;

  return (
    <DeleteResourceModal
      resourceName={tierName}
      label={t('This permanently deletes the storage tier. This action cannot be undone.')}
      errorLabel={t('Failed to delete storage tier')}
      onClose={onClose}
      onSuccess={onSuccess}
      mutation={deleteTier}
      variables={{ id: tier.id }}
    />
  );
};

export default StorageTierDeleteConfirmModal;
