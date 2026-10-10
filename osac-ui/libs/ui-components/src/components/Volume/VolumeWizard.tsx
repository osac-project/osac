import { useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { PageSection, PageSectionTypes, Wizard, WizardStep } from '@patternfly/react-core';
import { Formik } from 'formik';

import { Volumes } from '@osac/types';

import ConfigurationStep from './ConfigurationStep';
import GeneralStep from './GeneralStep';
import { buildVolumeCreatePayload } from './payload';
import ReviewStep from './ReviewStep';
import { getVolumeValidationSchema, volumeStepHasErrors } from './validation';
import { VOLUMES_LIST_PATH, type VolumeFormValues, getVolumeValues } from './values';
import { useCreateResource } from '../../api/use-resource';
import { useTranslation } from '../../hooks/useTranslation';
import { FieldValidationProvider } from '../Form/FieldValidationContext';
import LeaveFormConfirmation from '../Form/LeaveFormConfirmation';
import { OSACWizardFooter } from '../Wizard/OSACWizardFooter';

type VolumeWizardStep = 'general' | 'configuration' | 'review';

const VolumeWizard = () => {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const [currentStep, setCurrentStep] = useState<VolumeWizardStep>('general');
  const {
    mutateAsync: createVolume,
    error: createError,
    reset: resetCreate,
  } = useCreateResource(Volumes);
  const initialValues = getVolumeValues();

  const onSubmit = async (values: VolumeFormValues) => {
    try {
      const response = await createVolume({ object: buildVolumeCreatePayload(values) });
      navigate(
        response.object?.id ? `${VOLUMES_LIST_PATH}/${response.object.id}` : VOLUMES_LIST_PATH,
      );
    } catch {
      // The mutation error is rendered by OSACWizardFooter.
    }
  };

  return (
    <Formik<VolumeFormValues>
      initialValues={initialValues}
      validationSchema={getVolumeValidationSchema(t)}
      onSubmit={onSubmit}
    >
      <FieldValidationProvider>
        <LeaveFormConfirmation />
        <PageSection
          hasBodyWrapper={false}
          isFilled
          type={PageSectionTypes.wizard}
          aria-label={t('Volume wizard')}
        >
          <Wizard
            navAriaLabel={t('Volume wizard steps')}
            isVisitRequired
            footer={
              <OSACWizardFooter
                onCancel={() => navigate(VOLUMES_LIST_PATH)}
                stepHasErrors={volumeStepHasErrors}
                error={createError}
                onErrorReset={resetCreate}
                submitLabel={t('Create volume')}
              />
            }
            onStepChange={(_, step) => setCurrentStep(step.id as VolumeWizardStep)}
          >
            <WizardStep id="general" name={t('General')}>
              {currentStep === 'general' && <GeneralStep />}
            </WizardStep>
            <WizardStep id="configuration" name={t('Configuration')}>
              {currentStep === 'configuration' && <ConfigurationStep />}
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

export default VolumeWizard;
