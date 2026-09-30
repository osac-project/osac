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
  label: string;
  errorLabel: string;
  onClose: () => void;
  onSuccess?: () => void;
  mutation: DeleteMutation<TVariables>;
  variables: TVariables;
}

const DeleteResourceModal = <TVariables,>({
  resourceName,
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
      aria-labelledby="delete-confirm-title"
    >
      <ModalHeader
        title={t('Delete {{name}}?', { name: resourceName })}
        titleIconVariant="warning"
        labelId="delete-confirm-title"
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
          isDisabled={isPending}
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
