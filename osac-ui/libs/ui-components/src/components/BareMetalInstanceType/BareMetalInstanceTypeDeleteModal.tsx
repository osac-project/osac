import { type BareMetalInstanceType, BareMetalInstanceTypes } from '@osac/types/private';
import { useDeleteResource } from '@osac/ui-components/api/use-resource';
import DeleteResourceModal from '@osac/ui-components/components/Resource/DeleteResourceModal';

import { useTranslation } from '../../hooks/useTranslation';

interface BareMetalInstanceTypeDeleteModalProps {
  instanceType: BareMetalInstanceType;
  onClose: VoidFunction;
  onSuccess: VoidFunction;
}

const BareMetalInstanceTypeDeleteModal = ({
  instanceType,
  onClose,
  onSuccess,
}: BareMetalInstanceTypeDeleteModalProps) => {
  const { t } = useTranslation();
  const deleteBareMetalInstanceType = useDeleteResource(BareMetalInstanceTypes);

  return (
    <DeleteResourceModal
      resourceName={instanceType.metadata?.name || instanceType.id}
      label={t(
        'This permanently deletes the bare metal instance type. This action cannot be undone.',
      )}
      errorLabel={t('Failed to delete bare metal instance type')}
      onClose={onClose}
      onSuccess={onSuccess}
      mutation={deleteBareMetalInstanceType}
      variables={{ id: instanceType.id }}
    />
  );
};

export default BareMetalInstanceTypeDeleteModal;
