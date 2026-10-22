import { useCallback, useMemo, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { PageSection, PageSectionTypes, Wizard, WizardStep } from '@patternfly/react-core';
import { Formik } from 'formik';

import { ClusterCatalogItem, ClusterCatalogItems } from '@osac/types';
import { useGetResource } from '@osac/ui-components/api/use-resource';
import { useProvisionCluster } from '@osac/ui-components/api/v1/cluster';
import { FieldValidationProvider } from '@osac/ui-components/components/Form/FieldValidationContext';
import LeaveFormConfirmation from '@osac/ui-components/components/Form/LeaveFormConfirmation';
import { CatalogStep } from '@osac/ui-components/components/ProvisioningWizard/CatalogStep';
import { OSACWizardFooter } from '@osac/ui-components/components/Wizard/OSACWizardFooter';
import { useTranslation } from '@osac/ui-components/hooks/useTranslation';

import ClusterConfigurationStep from './ClusterConfigurationStep';
import ClusterGeneralStep from './ClusterGeneralStep';
import { ClusterNetworkingStep } from './ClusterNetworkingStep';
import { ClusterReviewStep } from './ClusterReviewStep';
import { type ClusterWizardValues, getInitialFormValues } from './fields';
import { buildClusterCreatePayload } from './payload';
import { type ClusterWizardStepId, buildClusterStepSchema } from './schemas';

interface Props {
  initialCatalogItemId?: string;
  onClosed: () => void;
}

const ClusterCreateWizard = ({ initialCatalogItemId, onClosed }: Props) => {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const provision = useProvisionCluster();
  const catalogItemQuery = useGetResource(
    ClusterCatalogItems,
    { id: initialCatalogItemId ?? '' },
    { enabled: Boolean(initialCatalogItemId) },
  );
  const [activeStep, setActiveStep] = useState<ClusterWizardStepId>('catalog');
  const [validationAlert, setValidationAlert] = useState(false);
  const steps = ['catalog', 'general', 'configuration', 'networking', 'review'] as const;
  const stepLabels: Record<ClusterWizardStepId, string> = {
    catalog: t('Catalog item'),
    general: t('General'),
    configuration: t('Configuration'),
    networking: t('Networking'),
    review: t('Review'),
  };

  const handleSubmit = useCallback(
    async (values: ClusterWizardValues) => {
      try {
        const created = await provision.mutateAsync(buildClusterCreatePayload(values));
        if (created?.id) {
          navigate(`/clusters/${created.id}`);
        }
      } catch {
        // Surfaced via provision.error in the footer; nothing further to do here.
      }
    },
    [navigate, provision],
  );

  const initialValues = useMemo(
    () => getInitialFormValues(catalogItemQuery.data?.object),
    [catalogItemQuery.data?.object],
  );

  const schema = useMemo(() => buildClusterStepSchema(activeStep, t), [activeStep, t]);
  const stepHasErrors = useCallback(
    (_stepId: string, errors: Record<string, unknown>) => {
      return Boolean(schema && Object.keys(errors).length > 0);
    },
    [schema],
  );
  return (
    <Formik<ClusterWizardValues>
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
              setActiveStep(String(step.id) as ClusterWizardStepId);
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
                  <CatalogStep<ClusterCatalogItem, ClusterWizardValues>
                    catalogService={ClusterCatalogItems}
                    isInitializing={Boolean(initialCatalogItemId) && catalogItemQuery.isPending}
                    getInitialFormValues={getInitialFormValues}
                  />
                )}
                {step === 'general' && <ClusterGeneralStep />}
                {step === 'configuration' && <ClusterConfigurationStep />}
                {step === 'networking' && <ClusterNetworkingStep />}
                {step === 'review' && <ClusterReviewStep />}
              </WizardStep>
            ))}
          </Wizard>
        </PageSection>
      </FieldValidationProvider>
    </Formik>
  );
};

export default ClusterCreateWizard;
