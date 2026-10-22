import { ExternalIPAttachmentEndpoint } from '@osac/types';
import type { ExternalIPAttachment } from '@osac/types';

import { useTranslation } from '../../../hooks/useTranslation';
import ExternalIpDetachModal from '../../ExternalIp/DetachExternalIpModal';

export interface DetachExternalIpModalProps {
  attachment: ExternalIPAttachment;
  onClose: () => void;
}

const DetachExternalIpModal = ({ attachment, onClose }: DetachExternalIpModalProps) => {
  const { t } = useTranslation();
  const endpoint = attachment.spec?.targetEndpoint;
  let label: string;
  switch (endpoint) {
    case ExternalIPAttachmentEndpoint.EXTERNAL_IP_ATTACHMENT_ENDPOINT_API:
      label = t('This detaches the external IP from the API endpoint.');
      break;
    case ExternalIPAttachmentEndpoint.EXTERNAL_IP_ATTACHMENT_ENDPOINT_INGRESS:
      label = t('This detaches the external IP from the Ingress endpoint.');
      break;
    default:
      label = t('This detaches the external IP from the cluster.');
  }

  return <ExternalIpDetachModal attachment={attachment} label={label} onClose={onClose} />;
};

export default DetachExternalIpModal;
