import { type StorageBackend, StorageBackends } from '@osac/types/private';
import { useDeleteResource } from '@osac/ui-components/api/use-resource';
import DeleteResourceModal from '@osac/ui-components/components/Resource/DeleteResourceModal.tsx';

import { useTranslation } from '../../hooks/useTranslation';

interface StorageBackendDeleteConfirmModalProps {
  backend: StorageBackend;
  onClose: () => void;
  onSuccess: () => void;
}

const StorageBackendDeleteConfirmModal = ({
  backend,
  onClose,
  onSuccess,
}: StorageBackendDeleteConfirmModalProps) => {
  const { t } = useTranslation();
  const deleteBackend = useDeleteResource(StorageBackends);
  const backendName = backend.metadata?.name ?? backend.id;

  return (
    <DeleteResourceModal
      resourceName={backendName}
      label={t('This permanently deletes the storage backend. This action cannot be undone.')}
      errorLabel={t('Failed to delete storage backend')}
      onClose={onClose}
      onSuccess={onSuccess}
      mutation={deleteBackend}
      variables={{ id: backend.id }}
    />
  );
};

export default StorageBackendDeleteConfirmModal;
