import { useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { PageSection, PageSectionTypes, Wizard, WizardStep } from '@patternfly/react-core';
import { Formik } from 'formik';

import { ExternalIPs } from '@osac/types';
import { useCreateResource } from '@osac/ui-components/api/use-resource';
import { FieldValidationProvider } from '@osac/ui-components/components/Form/FieldValidationContext';
import LeaveFormConfirmation from '@osac/ui-components/components/Form/LeaveFormConfirmation';
import { OSACWizardFooter } from '@osac/ui-components/components/Wizard/OSACWizardFooter';
import { useTranslation } from '@osac/ui-components/hooks/useTranslation';

import ExternalIpConfigurationStep from './ExternalIpConfigurationStep';
import ExternalIpGeneralStep from './ExternalIpGeneralStep';
import ExternalIpReviewStep from './ExternalIpReviewStep';
import { buildExternalIpCreatePayload } from './payload';
import { externalIpStepHasErrors, getExternalIpSchema } from './validation';
import { EXTERNAL_IPS_LIST_PATH, type ExternalIpFormValues, getExternalIpValues } from './values';

const ExternalIpWizard = () => {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const { mutateAsync: createExternalIp, error } = useCreateResource(ExternalIPs);
  const [currentStep, setCurrentStep] = useState('general');

  const onSubmit = async (values: ExternalIpFormValues) => {
    try {
      await createExternalIp({ object: buildExternalIpCreatePayload(values) });
      navigate(EXTERNAL_IPS_LIST_PATH);
    } catch {
      // Surfaced via the mutation's own `error` state in the wizard footer.
    }
  };

  return (
    <Formik<ExternalIpFormValues>
      initialValues={getExternalIpValues()}
      validationSchema={getExternalIpSchema(t)}
      onSubmit={onSubmit}
    >
      <FieldValidationProvider>
        <LeaveFormConfirmation />
        <PageSection
          hasBodyWrapper={false}
          isFilled
          type={PageSectionTypes.wizard}
          aria-label={t('Create external IP wizard')}
        >
          <Wizard
            navAriaLabel={t('Create external IP steps')}
            isVisitRequired
            footer={
              <OSACWizardFooter
                onCancel={() => navigate(EXTERNAL_IPS_LIST_PATH)}
                stepHasErrors={externalIpStepHasErrors}
                error={error}
                submitLabel={t('Create external IP')}
                errorTitle={t('Failed to create external IP')}
              />
            }
            onStepChange={(_, step) => setCurrentStep(step.id as string)}
          >
            <WizardStep id="general" name={t('General')}>
              {currentStep === 'general' && <ExternalIpGeneralStep />}
            </WizardStep>
            <WizardStep id="configuration" name={t('Configuration')}>
              {currentStep === 'configuration' && <ExternalIpConfigurationStep />}
            </WizardStep>
            <WizardStep id="review" name={t('Review')}>
              {currentStep === 'review' && <ExternalIpReviewStep />}
            </WizardStep>
          </Wizard>
        </PageSection>
      </FieldValidationProvider>
    </Formik>
  );
};

export default ExternalIpWizard;
