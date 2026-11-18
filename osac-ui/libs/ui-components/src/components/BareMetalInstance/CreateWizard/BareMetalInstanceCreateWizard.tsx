import { useCallback, useMemo, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { PageSection, PageSectionTypes, Wizard, WizardStep } from '@patternfly/react-core';
import { Formik } from 'formik';

import { BareMetalInstanceCatalogItem, BareMetalInstanceCatalogItems } from '@osac/types';
import { useCreateBareMetalInstance } from '@osac/ui-components/api/v1/baremetal-instance';
import { FieldValidationProvider } from '@osac/ui-components/components/Form/FieldValidationContext';
import LeaveFormConfirmation from '@osac/ui-components/components/Form/LeaveFormConfirmation';
import { CatalogStep } from '@osac/ui-components/components/ProvisioningWizard/CatalogStep';
import { OSACWizardFooter } from '@osac/ui-components/components/Wizard/OSACWizardFooter';
import { useTranslation } from '@osac/ui-components/hooks/useTranslation';

import BareMetalAccessStep from './BareMetalAccessStep';
import BareMetalConfigurationStep from './BareMetalConfigurationStep';
import BareMetalGeneralStep from './BareMetalGeneralStep';
import { BareMetalNetworkingStep } from './BareMetalNetworkingStep';
import { BareMetalReviewStep } from './BareMetalReviewStep';
import type { BareMetalInstanceWizardValues } from './fields';
import { getBareMetalInstanceInitialValues } from './fields';
import { buildBareMetalInstanceCreatePayload } from './payload';
import { type BareMetalWizardStepId, buildBareMetalInstanceStepSchema } from './schemas';

interface Props {
  /** Catalog item snapshot for a catalog-origin launch; omit for list-origin creation. */
  catalogItem?: BareMetalInstanceCatalogItem;
  onClosed: () => void;
}

const BareMetalInstanceCreateWizard = ({ catalogItem, onClosed }: Props) => {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const provision = useCreateBareMetalInstance();
  // The item the form is built from: the launch page's snapshot in the
  // catalog-origin flow, or the item picked in the Catalog step in the
  // list-origin flow. Frozen at mount so a later cache update cannot reset
  // an in-progress form through enableReinitialize.
  const [selectedCatalogItem, setSelectedCatalogItem] = useState(catalogItem);
  const includeCatalogStep = !catalogItem;
  const [activeStep, setActiveStep] = useState<BareMetalWizardStepId>(
    includeCatalogStep ? 'catalog' : 'general',
  );
  const steps: BareMetalWizardStepId[] = [
    ...(includeCatalogStep ? (['catalog'] as const) : []),
    'general',
    'configuration',
    'networking',
    'access',
    'review',
  ];
  const stepLabels: Record<BareMetalWizardStepId, string> = {
    catalog: t('Catalog item'),
    general: t('General'),
    configuration: t('Configuration'),
    networking: t('Networking'),
    access: t('Access & initial run'),
    review: t('Review'),
  };

  const handleSubmit = useCallback(
    async (values: BareMetalInstanceWizardValues) => {
      try {
        const created = await provision.mutateAsync(
          buildBareMetalInstanceCreatePayload(values, selectedCatalogItem),
        );
        if (created?.id) {
          navigate(`/bare-metal/${created.id}`);
        }
      } catch {
        // Surfaced via provision.error in the footer; nothing further to do here.
      }
    },
    [navigate, provision, selectedCatalogItem],
  );

  const initialValues = useMemo(
    () => getBareMetalInstanceInitialValues(selectedCatalogItem),
    [selectedCatalogItem],
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
    >
      <FieldValidationProvider>
        <LeaveFormConfirmation />
        <PageSection hasBodyWrapper={false} type={PageSectionTypes.wizard}>
          <Wizard
            isVisitRequired
            onStepChange={(_, step) => setActiveStep(String(step.id) as BareMetalWizardStepId)}
            footer={
              <OSACWizardFooter
                onCancel={onClosed}
                stepHasErrors={stepHasErrors}
                error={provision.error}
                onErrorReset={provision.reset}
              />
            }
          >
            {steps.map((step, index) => (
              <WizardStep
                key={step}
                id={step}
                name={stepLabels[step]}
                isDisabled={index > steps.indexOf(activeStep)}
              >
                {includeCatalogStep && activeStep === 'catalog' && (
                  <CatalogStep<BareMetalInstanceCatalogItem, BareMetalInstanceWizardValues>
                    catalogService={BareMetalInstanceCatalogItems}
                    getInitialFormValues={getBareMetalInstanceInitialValues}
                    onCatalogItemSelected={setSelectedCatalogItem}
                  />
                )}
                {activeStep === 'general' && <BareMetalGeneralStep />}
                {activeStep === 'configuration' && <BareMetalConfigurationStep />}
                {activeStep === 'networking' && <BareMetalNetworkingStep />}
                {activeStep === 'access' && <BareMetalAccessStep />}
                {activeStep === 'review' && <BareMetalReviewStep />}
              </WizardStep>
            ))}
          </Wizard>
        </PageSection>
      </FieldValidationProvider>
    </Formik>
  );
};

export default BareMetalInstanceCreateWizard;
