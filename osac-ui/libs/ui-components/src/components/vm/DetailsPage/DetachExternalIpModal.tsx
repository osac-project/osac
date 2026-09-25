import { ExternalIPAttachments } from '@osac/types';
import type { ExternalIPAttachment } from '@osac/types';

import { useDeleteResource } from '../../../api/use-resource';
import { useTranslation } from '../../../hooks/useTranslation';
import DeleteResourceModal from '../../Resource/DeleteResourceModal';

interface DetachExternalIpModalProps {
  attachment: ExternalIPAttachment;
  externalIpAddress?: string;
  onClose: () => void;
}

const DetachExternalIpModal = ({
  attachment,
  externalIpAddress,
  onClose,
}: DetachExternalIpModalProps) => {
  const { t } = useTranslation();
  const deleteAttachment = useDeleteResource(ExternalIPAttachments);
  const resourceName = externalIpAddress || attachment.spec?.externalIp?.id || attachment.id;

  return (
    <DeleteResourceModal
      resourceName={resourceName}
      label={t('This detaches the external IP from the virtual machine.')}
      errorLabel={t('Failed to detach external IP')}
      onClose={onClose}
      onSuccess={onClose}
      mutation={deleteAttachment}
      variables={{ id: attachment.id }}
    />
  );
};

export default DetachExternalIpModal;
