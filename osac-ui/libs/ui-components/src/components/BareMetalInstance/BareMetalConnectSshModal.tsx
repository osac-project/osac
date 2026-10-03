import {
  Button,
  ClipboardCopy,
  FormGroup,
  Modal,
  ModalBody,
  ModalFooter,
  ModalHeader,
  Stack,
  StackItem,
} from '@patternfly/react-core';

import { buildBareMetalSshCommand } from './bareMetalInstanceDisplay';
import { useTranslation } from '../../hooks/useTranslation';

interface BareMetalConnectSshModalProps {
  host: string;
  username: string;
  onClose: () => void;
}

const BareMetalConnectSshModal = ({ host, username, onClose }: BareMetalConnectSshModalProps) => {
  const { t } = useTranslation();
  const command = buildBareMetalSshCommand(username, host);

  return (
    <Modal variant="small" isOpen onClose={onClose} aria-labelledby="bare-metal-connect-ssh-title">
      <ModalHeader title={t('Connect via SSH')} labelId="bare-metal-connect-ssh-title" />
      <ModalBody>
        <Stack hasGutter>
          <StackItem>
            {t('Use the SSH public key from launch to connect to this host when it is running.')}
          </StackItem>
          <StackItem>
            <FormGroup label={t('Host')} fieldId="bare-metal-ssh-host">
              {host}
            </FormGroup>
          </StackItem>
          <StackItem>
            <FormGroup label={t('Command')} fieldId="bare-metal-ssh-command">
              <ClipboardCopy isReadOnly hoverTip={t('Copy')} clickTip={t('Copied')}>
                {command}
              </ClipboardCopy>
            </FormGroup>
          </StackItem>
        </Stack>
      </ModalBody>
      <ModalFooter>
        <Button variant="secondary" onClick={onClose}>
          {t('Close')}
        </Button>
      </ModalFooter>
    </Modal>
  );
};

export default BareMetalConnectSshModal;
