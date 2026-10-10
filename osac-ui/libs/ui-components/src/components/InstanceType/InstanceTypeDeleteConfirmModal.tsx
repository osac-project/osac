import { InstanceTypes, type InstanceType as PrivateInstanceType } from '@osac/types/private';
import { useDeleteResource } from '@osac/ui-components/api/use-resource';
import DeleteResourceModal from '@osac/ui-components/components/Resource/DeleteResourceModal.tsx';

import { useTranslation } from '../../hooks/useTranslation';

interface InstanceTypeDeleteConfirmModalProps {
  instanceType: PrivateInstanceType;
  onClose: () => void;
  onSuccess: () => void;
}

const InstanceTypeDeleteConfirmModal = ({
  instanceType,
  onClose,
  onSuccess,
}: InstanceTypeDeleteConfirmModalProps) => {
  const { t } = useTranslation();
  const deleteInstanceType = useDeleteResource(InstanceTypes);
  const name = instanceType.metadata?.name ?? instanceType.id;

  return (
    <DeleteResourceModal
      resourceName={name}
      label={t('This permanently deletes the instance type. This action cannot be undone.')}
      errorLabel={t('Failed to delete instance type')}
      onClose={onClose}
      onSuccess={onSuccess}
      mutation={deleteInstanceType}
      variables={{ id: instanceType.id }}
    />
  );
};

export default InstanceTypeDeleteConfirmModal;
