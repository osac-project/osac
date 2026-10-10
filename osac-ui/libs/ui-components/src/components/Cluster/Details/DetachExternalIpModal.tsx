import { ExternalIPAttachmentEndpoint } from '@osac/types';
import type { ExternalIPAttachment } from '@osac/types';

import { useTranslation } from '../../../hooks/useTranslation';
import ExternalIpDetachModal from '../../ExternalIp/DetachExternalIpModal';

export interface DetachExternalIpModalProps {
  attachment: ExternalIPAttachment;
  externalIpAddress?: string;
  endpoint: ExternalIPAttachmentEndpoint;
  onClose: () => void;
}

const DetachExternalIpModal = ({
  attachment,
  externalIpAddress,
  endpoint,
  onClose,
}: DetachExternalIpModalProps) => {
  const { t } = useTranslation();
  const label =
    endpoint === ExternalIPAttachmentEndpoint.EXTERNAL_IP_ATTACHMENT_ENDPOINT_API
      ? t('This detaches the external IP from the API endpoint.')
      : t('This detaches the external IP from the Ingress endpoint.');

  return (
    <ExternalIpDetachModal
      attachment={attachment}
      externalIpAddress={externalIpAddress}
      label={label}
      onClose={onClose}
    />
  );
};

export default DetachExternalIpModal;
