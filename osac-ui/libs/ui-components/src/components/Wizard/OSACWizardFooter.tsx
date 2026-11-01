import { useCallback, useEffect, useRef, useState } from 'react';
import {
  ActionList,
  ActionListGroup,
  ActionListItem,
  Alert,
  Button,
  Stack,
  StackItem,
  WizardFooterWrapper,
  useWizardContext,
} from '@patternfly/react-core';
import type { FormikErrors } from 'formik';
import { useFormikContext } from 'formik';

import { getApiErrorMessage } from '@osac/ui-components/utils/error';

import { useTranslation } from '../../hooks/useTranslation';
import { useFieldValidation } from '../Form/FieldValidationContext';

interface OSACWizardFooterProps {
  onCancel: () => void;
  stepHasErrors: (stepId: string, errors: FormikErrors<unknown>) => boolean;
  isEdit?: boolean;
  error: unknown;
  onErrorReset?: () => void;
  isNextDisabled?: boolean;
  submitLabel?: string;
  errorTitle?: string;
}

export const OSACWizardFooter = ({
  onCancel,
  stepHasErrors,
  isEdit,
  error,
  onErrorReset,
  isNextDisabled = false,
  submitLabel,
  errorTitle,
}: OSACWizardFooterProps) => {
  const { t } = useTranslation();
  const { activeStep, goToStepByIndex, steps } = useWizardContext();
  const { submitForm, validateForm, isSubmitting } = useFormikContext();
  const { setShowErrors } = useFieldValidation();
  const [validationAlert, setValidationAlert] = useState(false);
  const actionInFlight = useRef(false);
  const [isActionPending, setIsActionPending] = useState(false);
  const isBusy = isSubmitting || isActionPending;

  const stepIndex = activeStep?.index ?? 1;
  const isFirst = stepIndex <= 1;
  const isLast = stepIndex >= steps.length;

  useEffect(() => {
    setValidationAlert(false);
    setShowErrors(false);
    onErrorReset?.();
  }, [activeStep.id, onErrorReset, setShowErrors]);

  const handleBack = useCallback(() => {
    if (isFirst || isBusy || actionInFlight.current) {
      return;
    }
    goToStepByIndex(stepIndex - 1);
  }, [goToStepByIndex, isFirst, isBusy, stepIndex]);

  const handleNextOrCreate = useCallback(async () => {
    if (isSubmitting || actionInFlight.current) {
      return;
    }
    actionInFlight.current = true;
    setIsActionPending(true);
    try {
      const errors = await validateForm();
      if (isLast ? Object.keys(errors).length > 0 : stepHasErrors(String(activeStep.id), errors)) {
        setValidationAlert(true);
        setShowErrors(true);
        return;
      }
      setValidationAlert(false);
      setShowErrors(false);
      if (isLast) {
        await submitForm();
      } else {
        goToStepByIndex(stepIndex + 1);
      }
    } catch {
      setValidationAlert(true);
      setShowErrors(true);
    } finally {
      actionInFlight.current = false;
      setIsActionPending(false);
    }
  }, [
    activeStep.id,
    isSubmitting,
    submitForm,
    validateForm,
    goToStepByIndex,
    isLast,
    setShowErrors,
    stepHasErrors,
    stepIndex,
  ]);

  const submitButtonLabel = submitLabel ?? (isEdit ? t('Edit') : t('Create'));

  return (
    <WizardFooterWrapper>
      <Stack hasGutter>
        {validationAlert && (
          <StackItem>
            <Alert
              variant="danger"
              isInline
              title={t('Fix the highlighted errors before continuing.')}
            />
          </StackItem>
        )}
        {!!error && isLast && (
          <StackItem>
            <Alert
              variant="danger"
              isInline
              title={
                errorTitle ??
                (isEdit ? t('Failed to edit resource') : t('Failed to create resource'))
              }
            >
              {getApiErrorMessage(error, t)}
            </Alert>
          </StackItem>
        )}
        <StackItem>
          <ActionList>
            <ActionListGroup>
              <ActionListItem>
                <Button
                  variant="secondary"
                  onClick={handleBack}
                  isDisabled={isFirst || isBusy}
                  isAriaDisabled={isFirst || isBusy}
                >
                  {t('Back')}
                </Button>
              </ActionListItem>
              <ActionListItem>
                <Button
                  type="button"
                  variant="primary"
                  onClick={() => void handleNextOrCreate()}
                  isDisabled={isBusy || isNextDisabled}
                  isLoading={isBusy}
                >
                  {isLast ? submitButtonLabel : t('Next')}
                </Button>
              </ActionListItem>
              <ActionListItem>
                <Button variant="link" onClick={onCancel} isDisabled={isBusy}>
                  {t('Cancel')}
                </Button>
              </ActionListItem>
            </ActionListGroup>
          </ActionList>
        </StackItem>
      </Stack>
    </WizardFooterWrapper>
  );
};
