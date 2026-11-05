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
import { withCatalogItemTenant } from '@osac/ui-components/components/ProvisioningWizard/useWorkloadTenant';
import { OSACWizardFooter } from '@osac/ui-components/components/Wizard/OSACWizardFooter';
import { useSession } from '@osac/ui-components/hooks/use-session';
import { useTranslation } from '@osac/ui-components/hooks/useTranslation';

import BareMetalAccessStep from './BareMetalAccessStep';
import BareMetalConfigurationStep from './BareMetalConfigurationStep';
import BareMetalGeneralStep from './BareMetalGeneralStep';
import { BareMetalNetworkingStep } from './BareMetalNetworkingStep';
import { BareMetalReviewStep } from './BareMetalReviewStep';
import type { BareMetalInstanceWizardValues } from './fields';
import { createEmptyBareMetalInstanceValues, getBareMetalInstanceInitialValues } from './fields';
import { buildBareMetalInstanceCreatePayload } from './payload';
import { type BareMetalWizardStepId, buildBareMetalInstanceStepSchema } from './schemas';

interface Props {
  initialCatalogItemId?: string;
  onClosed: () => void;
}

const BareMetalInstanceCreateWizard = ({ initialCatalogItemId, onClosed }: Props) => {
  const { t } = useTranslation();
  const isAdmin = useSession().role === 'admin';
  const navigate = useNavigate();
  const provision = useCreateBareMetalInstance();
  const catalogItemQuery = useGetResource(
    BareMetalInstanceCatalogItems,
    { id: initialCatalogItemId ?? '' },
    { enabled: Boolean(initialCatalogItemId) },
  );
  const [activeStep, setActiveStep] = useState<BareMetalWizardStepId>('catalog');
  const [selectedCatalogItem, setSelectedCatalogItem] = useState<BareMetalInstanceCatalogItem>();
  const steps = ['catalog', 'general', 'configuration', 'networking', 'access', 'review'] as const;
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
    () =>
      withCatalogItemTenant(
        getBareMetalInstanceInitialValues(catalogItemQuery.data?.object),
        isAdmin,
        catalogItemQuery.data?.object?.metadata?.tenant,
      ),
    [catalogItemQuery.data?.object, isAdmin],
  );
  const schema = useMemo(
    () => buildBareMetalInstanceStepSchema(activeStep, t, { requireTenant: isAdmin }),
    [activeStep, isAdmin, t],
  );
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
                {activeStep === 'catalog' && (
                  <CatalogStep<BareMetalInstanceCatalogItem, BareMetalInstanceWizardValues>
                    catalogService={BareMetalInstanceCatalogItems}
                    isInitializing={Boolean(initialCatalogItemId) && catalogItemQuery.isPending}
                    getInitialFormValues={getBareMetalInstanceInitialValues}
                    getEmptyFormValues={createEmptyBareMetalInstanceValues}
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
