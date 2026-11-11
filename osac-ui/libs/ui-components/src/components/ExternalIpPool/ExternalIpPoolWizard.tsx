import { useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { PageSection, PageSectionTypes, Wizard, WizardStep } from '@patternfly/react-core';
import { Formik } from 'formik';

import { ExternalIPPools } from '@osac/types/private';
import { useCreateResource } from '@osac/ui-components/api/use-resource';
import { FieldValidationProvider } from '@osac/ui-components/components/Form/FieldValidationContext';
import LeaveFormConfirmation from '@osac/ui-components/components/Form/LeaveFormConfirmation';
import { OSACWizardFooter } from '@osac/ui-components/components/Wizard/OSACWizardFooter';
import { useTranslation } from '@osac/ui-components/hooks/useTranslation';

import { toCreateRequest } from './payload';
import PoolStep from './PoolStep';
import ReviewStep from './ReviewStep';
import { externalIpPoolStepHasErrors, getExternalIpPoolSchema } from './validation';
import {
  EXTERNAL_IP_POOLS_LIST_PATH,
  type ExternalIpPoolFormValues,
  externalIpPoolDetailsPath,
  getExternalIpPoolValues,
} from './values';

const ExternalIpPoolWizard = () => {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const { mutateAsync: createPool, error } = useCreateResource(ExternalIPPools);
  const [currentStep, setCurrentStep] = useState('pool');

  const onSubmit = async (values: ExternalIpPoolFormValues) => {
    try {
      const response = await createPool(toCreateRequest(values));
      const id = response.object?.id;
      navigate(id ? externalIpPoolDetailsPath(id) : EXTERNAL_IP_POOLS_LIST_PATH);
    } catch {
      // Surfaced via the mutation's own `error` state in the wizard footer.
    }
  };

  return (
    <Formik<ExternalIpPoolFormValues>
      initialValues={getExternalIpPoolValues()}
      validationSchema={getExternalIpPoolSchema(t)}
      onSubmit={onSubmit}
    >
      <FieldValidationProvider>
        <LeaveFormConfirmation />
        <PageSection
          hasBodyWrapper={false}
          isFilled
          type={PageSectionTypes.wizard}
          aria-label={t('Create external IP pool wizard')}
        >
          <Wizard
            navAriaLabel={t('Create external IP pool steps')}
            isVisitRequired
            footer={
              <OSACWizardFooter
                onCancel={() => navigate(EXTERNAL_IP_POOLS_LIST_PATH)}
                stepHasErrors={externalIpPoolStepHasErrors}
                error={error}
              />
            }
            onStepChange={(_, step) => setCurrentStep(step.id as string)}
          >
            <WizardStep id="pool" name={t('External IP pool')}>
              {currentStep === 'pool' && <PoolStep />}
            </WizardStep>
            <WizardStep id="review" name={t('Review')}>
              {currentStep === 'review' && <ReviewStep />}
            </WizardStep>
          </Wizard>
        </PageSection>
      </FieldValidationProvider>
    </Formik>
  );
};

export default ExternalIpPoolWizard;
