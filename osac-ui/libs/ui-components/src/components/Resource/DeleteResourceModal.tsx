import type { ReactNode } from 'react';
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

import { deleteResourceModalIds } from './deleteResourceModalIds';
import { useTranslation } from '../../hooks/useTranslation';
import { getErrorMessage } from '../../utils/error';

interface DeleteMutation<TVariables> {
  mutate: (variables: TVariables, options?: { onSuccess?: () => void }) => void;
  isPending: boolean;
  error: Error | null;
  reset: () => void;
}

interface DeleteResourceModalProps<TVariables> {
  resourceName: string;
  /** When set, the confirmation cannot submit (a dependent appeared meanwhile). */
  disabledReason?: string;
  label: ReactNode;
  errorLabel: string;
  onClose: () => void;
  onSuccess?: () => void;
  mutation: DeleteMutation<TVariables>;
  variables: TVariables;
}

const DeleteResourceModal = <TVariables,>({
  resourceName,
  disabledReason,
  label,
  errorLabel,
  onClose,
  onSuccess,
  mutation,
  variables,
}: DeleteResourceModalProps<TVariables>) => {
  const { t } = useTranslation();
  const { mutate, reset, isPending, error } = mutation;

  const handleClosing = (closeFn?: () => void) => {
    reset();
    closeFn?.();
  };

  return (
    <Modal
      variant="small"
      isOpen
      onClose={isPending ? undefined : () => handleClosing(onClose)}
      aria-labelledby={deleteResourceModalIds.title}
    >
      <ModalHeader
        title={t('Delete {{name}}?', { name: resourceName })}
        titleIconVariant="warning"
        labelId={deleteResourceModalIds.title}
      />
      <ModalBody>
        <Stack hasGutter>
          <StackItem>{label}</StackItem>
          {!!error && (
            <StackItem>
              <Alert variant="danger" title={errorLabel} isInline>
                {getErrorMessage(error)}
              </Alert>
            </StackItem>
          )}
        </Stack>
      </ModalBody>
      <ModalFooter>
        <Button
          variant="danger"
          onClick={() => {
            reset();
            mutate(variables, { onSuccess: () => handleClosing(onSuccess) });
          }}
          isDisabled={isPending || Boolean(disabledReason)}
          isLoading={isPending}
        >
          {t('Delete')}
        </Button>
        <Button variant="link" onClick={() => handleClosing(onClose)} isDisabled={isPending}>
          {t('Cancel')}
        </Button>
      </ModalFooter>
    </Modal>
  );
};

export default DeleteResourceModal;
