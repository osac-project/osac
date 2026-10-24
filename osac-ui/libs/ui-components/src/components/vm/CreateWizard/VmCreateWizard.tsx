import { useCallback, useMemo, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { PageSection, PageSectionTypes, Wizard, WizardStep } from '@patternfly/react-core';
import { Formik } from 'formik';

import { ComputeInstanceCatalogItem, ComputeInstanceCatalogItems } from '@osac/types';
import { useGetResource } from '@osac/ui-components/api/use-resource';
import { useProvisionComputeInstance } from '@osac/ui-components/api/v1/compute-instance';
import { FieldValidationProvider } from '@osac/ui-components/components/Form/FieldValidationContext';
import LeaveFormConfirmation from '@osac/ui-components/components/Form/LeaveFormConfirmation';
import { CatalogStep } from '@osac/ui-components/components/ProvisioningWizard/CatalogStep';
import { OSACWizardFooter } from '@osac/ui-components/components/Wizard/OSACWizardFooter';
import { useTranslation } from '@osac/ui-components/hooks/useTranslation';

import type { ComputeInstanceWizardValues } from './fields';
import { getComputeInstanceInitialValues } from './fields';
import { buildComputeInstanceCreatePayload } from './payload';
import { type VmWizardStepId, buildComputeInstanceStepSchema } from './schemas';
import VmAccessStep from './VmAccessStep';
import { VmConfigurationStep } from './VmConfigurationStep';
import VmGeneralStep from './VmGeneralStep';
import { VmNetworkingStep } from './VmNetworkingStep';
import { VmReviewStep } from './VmReviewStep';
import { VmStorageStep } from './VmStorageStep';

interface Props {
  initialCatalogItemId?: string;
  onClosed: () => void;
}

const VmCreateWizard = ({ initialCatalogItemId, onClosed }: Props) => {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const provision = useProvisionComputeInstance();
  const catalogItemQuery = useGetResource(
    ComputeInstanceCatalogItems,
    { id: initialCatalogItemId ?? '' },
    { enabled: Boolean(initialCatalogItemId) },
  );
  const [activeStep, setActiveStep] = useState<VmWizardStepId>('catalog');
  const [validationAlert, setValidationAlert] = useState(false);
  const [selectedCatalogItem, setSelectedCatalogItem] = useState<ComputeInstanceCatalogItem>();
  const steps = [
    'catalog',
    'general',
    'configuration',
    'storage',
    'networking',
    'access',
    'review',
  ] as const;
  const stepLabels: Record<VmWizardStepId, string> = {
    catalog: t('Catalog item'),
    general: t('General'),
    configuration: t('Configuration'),
    storage: t('Storage'),
    networking: t('Networking'),
    access: t('Access & initial run'),
    review: t('Review'),
  };

  const handleSubmit = useCallback(
    async (values: ComputeInstanceWizardValues) => {
      if (!values.catalogItemReference.id || !values.catalogItemReference.name) {
        return;
      }
      try {
        const created = await provision.mutateAsync(
          buildComputeInstanceCreatePayload(
            values,
            selectedCatalogItem ?? catalogItemQuery.data?.object,
          ),
        );
        if (created?.id) {
          navigate(`/vms/${created.id}`);
        }
      } catch {
        // Surfaced via provision.error in the footer; nothing further to do here.
      }
    },
    [catalogItemQuery.data?.object, navigate, provision, selectedCatalogItem],
  );

  const initialValues = useMemo(
    () => getComputeInstanceInitialValues(catalogItemQuery.data?.object),
    [catalogItemQuery.data?.object],
  );
  const schema = useMemo(() => buildComputeInstanceStepSchema(activeStep, t), [activeStep, t]);
  const stepHasErrors = useCallback(
    (_stepId: string, errors: Record<string, unknown>) => {
      return Boolean(schema && Object.keys(errors).length > 0);
    },
    [schema],
  );
  return (
    <Formik<ComputeInstanceWizardValues>
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
              setActiveStep(String(step.id) as VmWizardStepId);
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
                {step === 'catalog' && (
                  <CatalogStep<ComputeInstanceCatalogItem, ComputeInstanceWizardValues>
                    catalogService={ComputeInstanceCatalogItems}
                    isInitializing={Boolean(initialCatalogItemId) && catalogItemQuery.isPending}
                    getInitialFormValues={getComputeInstanceInitialValues}
                    onCatalogItemSelected={setSelectedCatalogItem}
                  />
                )}
                {step === 'general' && <VmGeneralStep />}
                {step === 'configuration' && <VmConfigurationStep />}
                {step === 'storage' && <VmStorageStep />}
                {step === 'networking' && <VmNetworkingStep />}
                {step === 'access' && <VmAccessStep />}
                {step === 'review' && <VmReviewStep />}
              </WizardStep>
            ))}
          </Wizard>
        </PageSection>
      </FieldValidationProvider>
    </Formik>
  );
};

export default VmCreateWizard;
