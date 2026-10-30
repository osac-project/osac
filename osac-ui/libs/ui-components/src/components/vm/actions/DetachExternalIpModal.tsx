import type { ExternalIPAttachment } from '@osac/types';

import { useTranslation } from '../../../hooks/useTranslation';
import ExternalIpDetachModal from '../../ExternalIp/DetachExternalIpModal';

interface DetachExternalIpModalProps {
  attachment: ExternalIPAttachment;
  onClose: () => void;
}

const DetachExternalIpModal = ({ attachment, onClose }: DetachExternalIpModalProps) => {
  const { t } = useTranslation();

  return (
    <ExternalIpDetachModal
      attachment={attachment}
      label={t('This detaches the external IP from the virtual machine.')}
      onClose={onClose}
    />
  );
};

export default DetachExternalIpModal;
