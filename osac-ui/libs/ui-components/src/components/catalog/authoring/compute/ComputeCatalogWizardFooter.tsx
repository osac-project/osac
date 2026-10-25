import { useFormikContext } from 'formik';
import type { FormikErrors } from 'formik';

import type { ComputeCatalogItemValues } from './values';
import { useTranslation } from '../../../../hooks/useTranslation';
import { OSACWizardFooter } from '../../../Wizard/OSACWizardFooter';

interface ComputeCatalogWizardFooterProps {
  currentStep: string;
  error: unknown;
  isEdit: boolean;
  onCancel: () => void;
  onErrorReset: () => void;
  stepHasErrors: (stepId: string, errors: FormikErrors<unknown>) => boolean;
}

const ComputeCatalogWizardFooter = ({
  currentStep,
  error,
  isEdit,
  onCancel,
  onErrorReset,
  stepHasErrors,
}: ComputeCatalogWizardFooterProps) => {
  const { t } = useTranslation();
  const { dirty } = useFormikContext<ComputeCatalogItemValues>();

  return (
    <OSACWizardFooter
      onCancel={onCancel}
      stepHasErrors={stepHasErrors}
      isEdit={isEdit}
      error={error}
      onErrorReset={onErrorReset}
      isNextDisabled={isEdit && currentStep === 'review' && !dirty}
      submitLabel={isEdit ? t('Save changes') : t('Create catalog item')}
    />
  );
};

export default ComputeCatalogWizardFooter;
