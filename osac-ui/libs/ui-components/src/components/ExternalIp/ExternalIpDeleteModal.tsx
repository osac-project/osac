import { type ExternalIP, ExternalIPs } from '@osac/types';
import DeleteResourceModal from '@osac/ui-components/components/Resource/DeleteResourceModal.tsx';

import { useDeleteResource } from '../../api/use-resource';
import { useTranslation } from '../../hooks/useTranslation';

interface ExternalIpDeleteModalProps {
  externalIp: ExternalIP;
  onClose: () => void;
  onSuccess: () => void;
}

const ExternalIpDeleteModal = ({ externalIp, onClose, onSuccess }: ExternalIpDeleteModalProps) => {
  const { t } = useTranslation();
  const deleteExternalIp = useDeleteResource(ExternalIPs);

  return (
    <DeleteResourceModal
      resourceName={externalIp.metadata?.name ?? ''}
      label={t(
        'This permanently releases the external IP back to its pool. This action cannot be undone.',
      )}
      errorLabel={t('Failed to delete external IP')}
      onClose={onClose}
      onSuccess={onSuccess}
      mutation={deleteExternalIp}
      variables={{ id: externalIp.id }}
    />
  );
};

export default ExternalIpDeleteModal;
