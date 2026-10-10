import type { ReactNode } from 'react';

import type { ExternalIPAttachment } from '@osac/types';

import { useDeleteExternalIPAttachment } from '../../api/v1/external-ip';
import { useTranslation } from '../../hooks/useTranslation';
import DeleteResourceModal from '../Resource/DeleteResourceModal';

export interface ExternalIpDetachModalProps {
  attachment: ExternalIPAttachment;
  externalIpAddress?: string;
  label: ReactNode;
  onClose: () => void;
}

const ExternalIpDetachModal = ({
  attachment,
  externalIpAddress,
  label,
  onClose,
}: ExternalIpDetachModalProps) => {
  const { t } = useTranslation();
  const deleteAttachment = useDeleteExternalIPAttachment();
  const resourceName = externalIpAddress || attachment.spec?.externalIp?.id || attachment.id;

  return (
    <DeleteResourceModal
      resourceName={resourceName}
      label={label}
      errorLabel={t('Failed to detach external IP')}
      onClose={onClose}
      onSuccess={onClose}
      mutation={deleteAttachment}
      variables={{ id: attachment.id }}
    />
  );
};

export default ExternalIpDetachModal;
