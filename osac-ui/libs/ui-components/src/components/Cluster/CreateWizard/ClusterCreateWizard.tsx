import { useCallback, useEffect, useMemo, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { PageSection, PageSectionTypes, Wizard, WizardStep } from '@patternfly/react-core';
import { FormikProvider, useFormik } from 'formik';

import { ClusterCatalogItem } from '@osac/types';
import { useProvisionCluster } from '@osac/ui-components/api/v1/cluster';
import { useClusterCatalogItems } from '@osac/ui-components/api/v1/cluster-catalog-item';
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
import type { CatalogItem } from '../../catalog/catalogItemDisplay';

export interface ClusterCreateWizardCloseHandler {
  requestClose: () => void;
  pending: boolean;
}

interface Props {
  initialCatalogItemId?: string;
  onCloseHandlerChange?: (handler: ClusterCreateWizardCloseHandler) => void;
  onClosed?: () => void;
}

const ClusterWizardBody = ({
  stepId,
  catalogItem,
}: {
  stepId: string;
  catalogItem: ClusterCatalogItem | null;
}) => {
  if (stepId === 'catalog') {
    return null;
  }
  if (stepId === 'general') {
    return <ClusterGeneralStep catalogItem={catalogItem} />;
  }
  if (stepId === 'configuration') {
    return <ClusterConfigurationStep catalogItem={catalogItem} />;
  }
  if (stepId === 'networking') {
    return <ClusterNetworkingStep catalogItem={catalogItem} />;
  }
  return <ClusterReviewStep catalogItem={catalogItem} />;
};

const ClusterWizardContent = ({ initialCatalogItemId, onCloseHandlerChange, onClosed }: Props) => {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const provision = useProvisionCluster();
  const catalogQuery = useClusterCatalogItems();
  const [activeStep, setActiveStep] = useState<ClusterWizardStepId>('catalog');
  const [validationAlert, setValidationAlert] = useState(false);
  const [selectedCatalogItem, setSelectedCatalogItem] = useState<ClusterCatalogItem | null>(null);
  const catalogItems = useMemo(() => catalogQuery.data ?? [], [catalogQuery.data]);
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
      if (!selectedCatalogItem) {
        return;
      }
      try {
        const created = await provision.mutateAsync(
          buildClusterCreatePayload(values, selectedCatalogItem),
        );
        if (created?.id) {
          navigate(`/clusters/${created.id}`);
        }
      } catch {
        // Surfaced via provision.error in the footer; nothing further to do here.
      }
    },
    [navigate, provision, selectedCatalogItem],
  );

  const validationSchema = useMemo(() => buildClusterStepSchema(activeStep, t), [activeStep, t]);

  const formik = useFormik<ClusterWizardValues>({
    initialValues: getInitialFormValues(),
    validationSchema,
    onSubmit: handleSubmit,
    validateOnChange: false,
  });

  const handleCatalogItemSelected = useCallback(
    (item: CatalogItem) => {
      const clusterItem = item as ClusterCatalogItem;
      setSelectedCatalogItem(clusterItem);
      formik.resetForm({ values: getInitialFormValues(clusterItem) });
    },
    [formik],
  );

  const pending = formik.isSubmitting;

  useEffect(() => {
    const item = catalogItems.find((entry) => entry.id === initialCatalogItemId);
    if (item && !selectedCatalogItem) {
      setSelectedCatalogItem(item);
      formik.resetForm({ values: getInitialFormValues(item) });
    }
  }, [catalogItems, formik, initialCatalogItemId, selectedCatalogItem, t]);

  const requestClose = useCallback(() => {
    if (!pending) {
      onClosed?.();
    }
  }, [onClosed, pending]);

  useEffect(() => {
    onCloseHandlerChange?.({ requestClose, pending });
  }, [onCloseHandlerChange, pending, requestClose]);

  const stepHasErrors = useCallback(
    (stepId: string, errors: Record<string, unknown>) => {
      const schema = buildClusterStepSchema(stepId as ClusterWizardStepId, t);
      return Boolean(schema && Object.keys(errors).length > 0);
    },
    [t],
  );

  return (
    <FormikProvider value={formik}>
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
                onCancel={requestClose}
                stepHasErrors={stepHasErrors}
                error={provision.error}
                onErrorReset={provision.reset}
              />
            }
          >
            {steps.map((step) => (
              <WizardStep key={step} id={step} name={stepLabels[step]}>
                {step === 'catalog' ? (
                  <CatalogStep
                    catalogItems={catalogItems}
                    catalogLoading={catalogQuery.isPending}
                    catalogError={catalogQuery.isError}
                    refetchCatalogItems={() => void catalogQuery.refetch()}
                    onCatalogItemSelected={handleCatalogItemSelected}
                  />
                ) : (
                  <ClusterWizardBody stepId={step} catalogItem={selectedCatalogItem} />
                )}
              </WizardStep>
            ))}
          </Wizard>
        </PageSection>
      </FieldValidationProvider>
    </FormikProvider>
  );
};

const ClusterCreateWizard = (props: Props) => <ClusterWizardContent {...props} />;

export default ClusterCreateWizard;
