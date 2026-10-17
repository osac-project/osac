import { useCallback, useEffect, useMemo, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { PageSection, PageSectionTypes, Wizard, WizardStep } from '@patternfly/react-core';
import { FormikProvider, useFormik } from 'formik';

import { ComputeInstanceCatalogItem } from '@osac/types';
import { useProvisionComputeInstance } from '@osac/ui-components/api/v1/compute-instance';
import { useComputeInstanceCatalogItems } from '@osac/ui-components/api/v1/compute-instance-catalog-item';
import { FieldValidationProvider } from '@osac/ui-components/components/Form/FieldValidationContext';
import LeaveFormConfirmation from '@osac/ui-components/components/Form/LeaveFormConfirmation';
import { CatalogStep } from '@osac/ui-components/components/ProvisioningWizard/CatalogStep';
import { OSACWizardFooter } from '@osac/ui-components/components/Wizard/OSACWizardFooter';
import { useTranslation } from '@osac/ui-components/hooks/useTranslation';

import type { ComputeInstanceWizardValues } from './fields';
import { buildComputeInstanceCreatePayload, getComputeInstanceInitialValues } from './payload';
import { type VmWizardStepId, buildComputeInstanceStepSchema } from './schemas';
import { VmConfigurationStep } from './VmConfigurationStep';
import VmGeneralStep from './VmGeneralStep';
import { VmNetworkingStep } from './VmNetworkingStep';
import { VmReviewStep } from './VmReviewStep';
import { VmStorageStep } from './VmStorageStep';
import type { CatalogItem } from '../../catalog/catalogItemDisplay';

export interface VmCreateWizardCloseHandler {
  requestClose: () => void;
  pending: boolean;
}

interface Props {
  initialCatalogItemId?: string;
  onCloseHandlerChange?: (handler: VmCreateWizardCloseHandler) => void;
  onClosed?: () => void;
}

const VmWizardBody = ({
  stepId,
  catalogItem,
}: {
  stepId: string;
  catalogItem: ComputeInstanceCatalogItem | null;
}) => {
  if (stepId === 'catalog') {
    return null;
  }
  if (stepId === 'general') {
    return <VmGeneralStep catalogItem={catalogItem} />;
  }
  if (stepId === 'configuration') {
    return <VmConfigurationStep catalogItem={catalogItem} />;
  }
  if (stepId === 'storage') {
    return <VmStorageStep />;
  }
  if (stepId === 'networking') {
    return <VmNetworkingStep catalogItem={catalogItem} />;
  }
  return <VmReviewStep catalogItem={catalogItem} />;
};

const VmWizardContent = ({ initialCatalogItemId, onCloseHandlerChange, onClosed }: Props) => {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const provision = useProvisionComputeInstance();
  const catalogQuery = useComputeInstanceCatalogItems();
  const [activeStep, setActiveStep] = useState<VmWizardStepId>('catalog');
  const [validationAlert, setValidationAlert] = useState(false);
  const [selectedCatalogItem, setSelectedCatalogItem] = useState<ComputeInstanceCatalogItem | null>(
    null,
  );
  const catalogItems = useMemo(() => catalogQuery.data ?? [], [catalogQuery.data]);
  const steps = ['catalog', 'general', 'configuration', 'storage', 'networking', 'review'] as const;
  const stepLabels: Record<VmWizardStepId, string> = {
    catalog: t('Catalog item'),
    general: t('General'),
    configuration: t('Configuration'),
    storage: t('Storage'),
    networking: t('Networking'),
    review: t('Review'),
  };

  const handleSubmit = useCallback(
    async (values: ComputeInstanceWizardValues) => {
      if (!selectedCatalogItem) {
        return;
      }
      try {
        const created = await provision.mutateAsync(
          buildComputeInstanceCreatePayload(values, selectedCatalogItem),
        );
        if (created?.id) {
          navigate(`/vms/${created.id}`);
        }
      } catch {
        // Surfaced via provision.error in the footer; nothing further to do here.
      }
    },
    [navigate, provision, selectedCatalogItem],
  );

  const validationSchema = useMemo(
    () => buildComputeInstanceStepSchema(activeStep, t),
    [activeStep, t],
  );

  const formik = useFormik<ComputeInstanceWizardValues>({
    initialValues: getComputeInstanceInitialValues(),
    validationSchema,
    onSubmit: handleSubmit,
    validateOnChange: false,
  });

  const handleCatalogItemSelected = useCallback(
    (item: CatalogItem) => {
      const computeItem = item as ComputeInstanceCatalogItem;
      setSelectedCatalogItem(computeItem);
      formik.resetForm({
        values: getComputeInstanceInitialValues(computeItem),
      });
    },
    [formik],
  );

  const pending = formik.isSubmitting;

  useEffect(() => {
    const item = catalogItems.find((entry) => entry.id === initialCatalogItemId);
    if (item && !selectedCatalogItem) {
      setSelectedCatalogItem(item);
      formik.resetForm({ values: getComputeInstanceInitialValues(item) });
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
      const schema = buildComputeInstanceStepSchema(stepId as VmWizardStepId, t);
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
              setActiveStep(String(step.id) as VmWizardStepId);
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
                  <VmWizardBody stepId={step} catalogItem={selectedCatalogItem} />
                )}
              </WizardStep>
            ))}
          </Wizard>
        </PageSection>
      </FieldValidationProvider>
    </FormikProvider>
  );
};

const VmCreateWizard = (props: Props) => <VmWizardContent {...props} />;

export default VmCreateWizard;
