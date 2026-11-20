import type { SecurityGroup } from '@osac/types';

import { useDeleteSecurityGroup } from '../../api/v1/networking';
import { useTranslation } from '../../hooks/useTranslation';
import DeleteResourceModal from '../Resource/DeleteResourceModal';

interface SecurityGroupDeleteConfirmModalProps {
  securityGroup: SecurityGroup;
  onClose: () => void;
  onSuccess: () => void;
}

const SecurityGroupDeleteConfirmModal = ({
  securityGroup,
  onClose,
  onSuccess,
}: SecurityGroupDeleteConfirmModalProps) => {
  const { t } = useTranslation();
  const deleteSecurityGroup = useDeleteSecurityGroup();
  const securityGroupName = securityGroup.metadata?.name ?? securityGroup.id;

  return (
    <DeleteResourceModal
      resourceName={securityGroupName}
      label={t(
        'This will permanently delete the security group and all its rules. This action cannot be undone.',
      )}
      errorLabel={t('Failed to delete security group')}
      onClose={onClose}
      onSuccess={onSuccess}
      mutation={deleteSecurityGroup}
      variables={securityGroup.id}
    />
  );
};

export default SecurityGroupDeleteConfirmModal;
