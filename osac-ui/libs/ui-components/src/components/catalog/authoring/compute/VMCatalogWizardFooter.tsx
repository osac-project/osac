import { useFormikContext } from 'formik';
import type { FormikErrors } from 'formik';

import type { ComputeCatalogItemValues } from './values';
import { useTranslation } from '../../../../hooks/useTranslation';
import { OSACWizardFooter } from '../../../Wizard/OSACWizardFooter';

interface VMCatalogWizardFooterProps {
  currentStep: string;
  error: unknown;
  isEdit: boolean;
  onCancel: () => void;
  onErrorReset: () => void;
  stepHasErrors: (stepId: string, errors: FormikErrors<unknown>) => boolean;
}

const VMCatalogWizardFooter = ({
  currentStep,
  error,
  isEdit,
  onCancel,
  onErrorReset,
  stepHasErrors,
}: VMCatalogWizardFooterProps) => {
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

export default VMCatalogWizardFooter;
