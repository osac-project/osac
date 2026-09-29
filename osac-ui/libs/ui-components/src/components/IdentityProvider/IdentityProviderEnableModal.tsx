import { useState } from 'react';
import {
  Alert,
  Button,
  Modal,
  ModalBody,
  ModalFooter,
  ModalHeader,
  Stack,
  StackItem,
} from '@patternfly/react-core';

import { IdentityProvider, IdentityProviders } from '@osac/types';
import { useApiFetch } from '@osac/ui-components/api/api-context';
import { useUpdateResource } from '@osac/ui-components/api/use-resource';
import { pollIdentityProviderUntilSynced } from '@osac/ui-components/api/v1/identity-provider';

import { getIdpName } from './utils';
import { useTranslation } from '../../hooks/useTranslation';
import { getErrorMessage } from '../../utils/error';

interface IdentityProviderEnableModalProps {
  idp: IdentityProvider;
  onClose: () => void;
  onSuccess: () => void;
}

const IdentityProviderEnableModal = ({
  idp,
  onClose,
  onSuccess,
}: IdentityProviderEnableModalProps) => {
  const { t } = useTranslation();
  const { mutate, isPending, error } = useUpdateResource(IdentityProviders);
  const idpClient = useApiFetch(IdentityProviders);
  const [isPolling, setIsPolling] = useState(false);
  const [pollError, setPollError] = useState<Error | null>(null);

  const isEnabled = !!idp.spec?.enabled;
  const idpName = getIdpName(idp);
  const isLoading = isPending || isPolling;
  const displayError = pollError || error;

  return (
    <Modal
      variant="small"
      isOpen
      onClose={isLoading ? undefined : onClose}
      aria-labelledby="idp-enable-confirm-title"
    >
      <ModalHeader
        title={
          isEnabled ? t('Disable {{idpName}}?', { idpName }) : t('Enable {{idpName}}?', { idpName })
        }
        labelId="idp-enable-confirm-title"
      />
      <ModalBody>
        <Stack hasGutter>
          <StackItem>
            {isEnabled
              ? t('Are you sure you want to disable Identity provider {{idpName}}', { idpName })
              : t('Are you sure you want to enable Identity provider {{idpName}}', { idpName })}
          </StackItem>
          {displayError && (
            <StackItem>
              <Alert
                variant="danger"
                title={
                  isEnabled
                    ? t('Failed to disable Identity provider')
                    : t('Failed to enable Identity provider')
                }
                isInline
              >
                {getErrorMessage(displayError)}
              </Alert>
            </StackItem>
          )}
        </Stack>
      </ModalBody>
      <ModalFooter>
        <Button
          variant="primary"
          onClick={() => {
            setPollError(null);
            mutate(
              {
                object: {
                  id: idp.id,
                  spec: { enabled: !isEnabled },
                },
              },
              {
                onSuccess: async () => {
                  setIsPolling(true);
                  try {
                    await pollIdentityProviderUntilSynced(idpClient, idp.id);
                    onSuccess();
                  } catch (err) {
                    setPollError(err instanceof Error ? err : new Error(String(err)));
                  } finally {
                    setIsPolling(false);
                  }
                },
              },
            );
          }}
          isDisabled={isLoading}
          isLoading={isLoading}
        >
          {isEnabled ? t('Disable') : t('Enable')}
        </Button>
        <Button variant="link" onClick={onClose} isDisabled={isLoading}>
          {t('Cancel')}
        </Button>
      </ModalFooter>
    </Modal>
  );
};

export default IdentityProviderEnableModal;
