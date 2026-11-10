import { useCallback, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { PageSection, PageSectionTypes, Wizard, WizardStep } from '@patternfly/react-core';
import { Formik } from 'formik';

import {
  BareMetalInstanceTypes,
  type BareMetalInstanceType as PrivateBareMetalInstanceType,
} from '@osac/types/private';
import { useCreateResource, useUpdateResource } from '@osac/ui-components/api/use-resource';

import { toRequestBody } from './payload';
import AcceleratorsStep from './steps/AcceleratorsStep';
import CapabilitiesStep from './steps/CapabilitiesStep';
import CpuMemoryStep from './steps/CpuMemoryStep';
import DisksStep from './steps/DisksStep';
import GeneralStep from './steps/GeneralStep';
import NetworkingStep from './steps/NetworkingStep';
import ReviewStep from './steps/ReviewStep';
import { bareMetalStepHasErrors, getBareMetalInstanceTypeSchema } from './validation';
import { type BareMetalInstanceTypeFormValues, getBareMetalInstanceTypeValues } from './values';
import { useTranslation } from '../../../hooks/useTranslation';
import { FieldValidationProvider } from '../../Form/FieldValidationContext';
import LeaveFormConfirmation from '../../Form/LeaveFormConfirmation';
import { OSACWizardFooter } from '../../Wizard/OSACWizardFooter';
import { BAREMETAL_INSTANCE_TYPES_LIST_ROUTE } from '../AdminBareMetalInstanceTypeListPage';

interface BareMetalInstanceTypeFormProps {
  bareMetalInstanceType?: PrivateBareMetalInstanceType;
}

const BareMetalInstanceTypeForm = ({ bareMetalInstanceType }: BareMetalInstanceTypeFormProps) => {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const isEdit = Boolean(bareMetalInstanceType);
  const {
    mutateAsync: create,
    error: createError,
    reset: resetCreate,
  } = useCreateResource(BareMetalInstanceTypes);
  const {
    mutateAsync: update,
    error: updateError,
    reset: resetUpdate,
  } = useUpdateResource(BareMetalInstanceTypes);
  const [currentStep, setCurrentStep] = useState<string>('general');

  const handleErrorReset = useCallback(() => {
    resetCreate();
    resetUpdate();
  }, [resetCreate, resetUpdate]);

  const onSubmit = async (values: BareMetalInstanceTypeFormValues) => {
    try {
      if (bareMetalInstanceType) {
        await update({ object: { id: bareMetalInstanceType.id, ...toRequestBody(values) } });
      } else {
        await create({ object: toRequestBody(values) });
      }
      navigate(BAREMETAL_INSTANCE_TYPES_LIST_ROUTE);
    } catch {
      // tanstack handles the error
    }
  };

  return (
    <Formik
      initialValues={getBareMetalInstanceTypeValues(bareMetalInstanceType)}
      validationSchema={getBareMetalInstanceTypeSchema(t)}
      onSubmit={onSubmit}
    >
      <FieldValidationProvider>
        <LeaveFormConfirmation />
        <PageSection
          hasBodyWrapper={false}
          isFilled
          type={PageSectionTypes.wizard}
          aria-label={t('Bare metal instance type wizard')}
        >
          <Wizard
            navAriaLabel={t('Bare metal instance type steps')}
            isVisitRequired
            footer={
              <OSACWizardFooter
                onCancel={() => navigate(BAREMETAL_INSTANCE_TYPES_LIST_ROUTE)}
                stepHasErrors={bareMetalStepHasErrors}
                isEdit={isEdit}
                error={isEdit ? updateError : createError}
                onErrorReset={handleErrorReset}
              />
            }
            onStepChange={(_, step) => setCurrentStep(step.id as string)}
          >
            <WizardStep id="general" name={t('General')}>
              {currentStep === 'general' && <GeneralStep isEdit={isEdit} />}
            </WizardStep>
            <WizardStep id="cpu-memory" name={t('CPU & Memory')}>
              {currentStep === 'cpu-memory' && <CpuMemoryStep />}
            </WizardStep>
            <WizardStep id="accelerators" name={t('Accelerators')}>
              {currentStep === 'accelerators' && <AcceleratorsStep />}
            </WizardStep>
            <WizardStep id="disks" name={t('Disks')}>
              {currentStep === 'disks' && <DisksStep />}
            </WizardStep>
            <WizardStep id="networking" name={t('Networking')}>
              {currentStep === 'networking' && <NetworkingStep />}
            </WizardStep>
            <WizardStep id="capabilities" name={t('Capabilities')}>
              {currentStep === 'capabilities' && <CapabilitiesStep />}
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

export default BareMetalInstanceTypeForm;
