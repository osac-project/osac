import { useCallback, useMemo, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { PageSection, PageSectionTypes, Wizard, WizardStep } from '@patternfly/react-core';
import { Formik } from 'formik';

import { BareMetalInstanceCatalogItem, BareMetalInstanceCatalogItems } from '@osac/types';
import { useGetResource } from '@osac/ui-components/api/use-resource';
import { useCreateBareMetalInstance } from '@osac/ui-components/api/v1/baremetal-instance';
import { FieldValidationProvider } from '@osac/ui-components/components/Form/FieldValidationContext';
import LeaveFormConfirmation from '@osac/ui-components/components/Form/LeaveFormConfirmation';
import { CatalogStep } from '@osac/ui-components/components/ProvisioningWizard/CatalogStep';
import { OSACWizardFooter } from '@osac/ui-components/components/Wizard/OSACWizardFooter';
import { useTranslation } from '@osac/ui-components/hooks/useTranslation';

import BareMetalConfigurationStep from './BareMetalConfigurationStep';
import BareMetalGeneralStep from './BareMetalGeneralStep';
import { BareMetalNetworkingStep } from './BareMetalNetworkingStep';
import { BareMetalReviewStep } from './BareMetalReviewStep';
import type { BareMetalInstanceWizardValues } from './fields';
import { getBareMetalInstanceInitialValues } from './fields';
import { buildBareMetalInstanceCreatePayload } from './payload';
import { type BareMetalWizardStepId, buildBareMetalInstanceStepSchema } from './schemas';

interface Props {
  initialCatalogItemId?: string;
  onClosed: () => void;
}

const BareMetalInstanceCreateWizard = ({ initialCatalogItemId, onClosed }: Props) => {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const provision = useCreateBareMetalInstance();
  const catalogItemQuery = useGetResource(
    BareMetalInstanceCatalogItems,
    { id: initialCatalogItemId ?? '' },
    { enabled: Boolean(initialCatalogItemId) },
  );
  const [activeStep, setActiveStep] = useState<BareMetalWizardStepId>('catalog');
  const [validationAlert, setValidationAlert] = useState(false);
  const [selectedCatalogItem, setSelectedCatalogItem] = useState<BareMetalInstanceCatalogItem>();
  const steps = ['catalog', 'general', 'configuration', 'networking', 'review'] as const;
  const stepLabels: Record<BareMetalWizardStepId, string> = {
    catalog: t('Catalog item'),
    general: t('General'),
    configuration: t('Configuration'),
    networking: t('Networking'),
    review: t('Review'),
  };

  const handleSubmit = useCallback(
    async (values: BareMetalInstanceWizardValues) => {
      try {
        const created = await provision.mutateAsync(
          buildBareMetalInstanceCreatePayload(
            values,
            selectedCatalogItem ?? catalogItemQuery.data?.object,
          ),
        );
        if (created?.id) {
          navigate(`/bare-metal/${created.id}`);
        }
      } catch {
        // Surfaced via provision.error in the footer; nothing further to do here.
      }
    },
    [catalogItemQuery.data?.object, navigate, provision, selectedCatalogItem],
  );

  const initialValues = useMemo(
    () => getBareMetalInstanceInitialValues(catalogItemQuery.data?.object),
    [catalogItemQuery.data?.object],
  );
  const schema = useMemo(() => buildBareMetalInstanceStepSchema(activeStep, t), [activeStep, t]);
  const stepHasErrors = useCallback(
    (_stepId: string, errors: Record<string, unknown>) => {
      return Boolean(schema && Object.keys(errors).length > 0);
    },
    [schema],
  );
  return (
    <Formik<BareMetalInstanceWizardValues>
      initialValues={initialValues}
      enableReinitialize
      validationSchema={schema}
      onSubmit={handleSubmit}
      validateOnChange={false}
    >
      <FieldValidationProvider showErrors={validationAlert}>
        <LeaveFormConfirmation />
        <PageSection hasBodyWrapper={false} type={PageSectionTypes.wizard}>
          <Wizard
            isVisitRequired
            onStepChange={(_, step) => {
              setActiveStep(String(step.id) as BareMetalWizardStepId);
              setValidationAlert(false);
            }}
            footer={
              <OSACWizardFooter
                onCancel={onClosed}
                stepHasErrors={stepHasErrors}
                error={provision.error}
                onErrorReset={provision.reset}
              />
            }
          >
            {steps.map((step) => (
              <WizardStep key={step} id={step} name={stepLabels[step]}>
                {activeStep === step && step === 'catalog' && (
                  <CatalogStep<BareMetalInstanceCatalogItem, BareMetalInstanceWizardValues>
                    catalogService={BareMetalInstanceCatalogItems}
                    isInitializing={Boolean(initialCatalogItemId) && catalogItemQuery.isPending}
                    getInitialFormValues={getBareMetalInstanceInitialValues}
                    onCatalogItemSelected={setSelectedCatalogItem}
                  />
                )}
                {activeStep === step && step === 'general' && <BareMetalGeneralStep />}
                {activeStep === step && step === 'configuration' && <BareMetalConfigurationStep />}
                {activeStep === step && step === 'networking' && <BareMetalNetworkingStep />}
                {activeStep === step && step === 'review' && <BareMetalReviewStep />}
              </WizardStep>
            ))}
          </Wizard>
        </PageSection>
      </FieldValidationProvider>
    </Formik>
  );
};

export default BareMetalInstanceCreateWizard;
