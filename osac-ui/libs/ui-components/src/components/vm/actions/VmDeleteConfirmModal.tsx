import type { ComputeInstance } from '@osac/types';
import DeleteResourceModal from '@osac/ui-components/components/Resource/DeleteResourceModal.tsx';

import { useDeleteComputeInstance } from '../../../api/v1/compute-instance';
import { useTranslation } from '../../../hooks/useTranslation';

interface VmDeleteConfirmModalProps {
  vm: ComputeInstance;
  onClose: () => void;
  onSuccess: () => void;
}

const VmDeleteConfirmModal = ({ vm, onClose, onSuccess }: VmDeleteConfirmModalProps) => {
  const { t } = useTranslation();
  const deleteVm = useDeleteComputeInstance();
  const name = vm.metadata?.name ?? vm.id;

  return (
    <DeleteResourceModal
      resourceName={name}
      label={t('This permanently deletes the compute instance. This action cannot be undone.')}
      errorLabel={t('Failed to delete compute instance')}
      onClose={onClose}
      onSuccess={onSuccess}
      mutation={deleteVm}
      variables={vm.id}
    />
  );
};

export default VmDeleteConfirmModal;
