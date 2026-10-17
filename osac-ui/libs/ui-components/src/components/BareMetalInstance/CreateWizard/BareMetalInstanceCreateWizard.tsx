import { useCallback, useEffect, useMemo, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { PageSection, PageSectionTypes, Wizard, WizardStep } from '@patternfly/react-core';
import { FormikProvider, useFormik } from 'formik';

import { BareMetalInstanceCatalogItem } from '@osac/types';
import {
  useBareMetalInstanceCatalogItems,
  useCreateBareMetalInstance,
} from '@osac/ui-components/api/v1/baremetal-instance';
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
import type { CatalogItem } from '../../catalog/catalogItemDisplay';

export interface BareMetalInstanceCreateWizardCloseHandler {
  requestClose: () => void;
  pending: boolean;
}

interface Props {
  initialCatalogItemId?: string;
  onCloseHandlerChange?: (handler: BareMetalInstanceCreateWizardCloseHandler) => void;
  onClosed?: () => void;
}

const BareMetalInstanceWizardBody = ({
  stepId,
  catalogItem,
}: {
  stepId: string;
  catalogItem: BareMetalInstanceCatalogItem | null;
}) => {
  if (stepId === 'catalog') {
    return null;
  }
  if (stepId === 'general') {
    return <BareMetalGeneralStep catalogItem={catalogItem} />;
  }
  if (stepId === 'configuration') {
    return <BareMetalConfigurationStep catalogItem={catalogItem} />;
  }
  if (stepId === 'networking') {
    return <BareMetalNetworkingStep catalogItem={catalogItem} />;
  }
  return <BareMetalReviewStep catalogItem={catalogItem} />;
};

const BareMetalInstanceWizardContent = ({
  initialCatalogItemId,
  onCloseHandlerChange,
  onClosed,
}: Props) => {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const provision = useCreateBareMetalInstance();
  const catalogQuery = useBareMetalInstanceCatalogItems();
  const [activeStep, setActiveStep] = useState<BareMetalWizardStepId>('catalog');
  const [validationAlert, setValidationAlert] = useState(false);
  const [selectedCatalogItem, setSelectedCatalogItem] =
    useState<BareMetalInstanceCatalogItem | null>(null);
  const catalogItems = useMemo(() => catalogQuery.data ?? [], [catalogQuery.data]);
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
      if (!selectedCatalogItem) {
        return;
      }
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

  const validationSchema = useMemo(
    () => buildBareMetalInstanceStepSchema(activeStep, t),
    [activeStep, t],
  );

  const formik = useFormik<BareMetalInstanceWizardValues>({
    initialValues: getBareMetalInstanceInitialValues(),
    validationSchema,
    onSubmit: handleSubmit,
    validateOnChange: false,
  });

  const handleCatalogItemSelected = useCallback(
    (item: CatalogItem) => {
      const bareMetalItem = item as BareMetalInstanceCatalogItem;
      setSelectedCatalogItem(bareMetalItem);
      formik.resetForm({ values: getBareMetalInstanceInitialValues(bareMetalItem) });
    },
    [formik],
  );

  const pending = formik.isSubmitting;

  useEffect(() => {
    const item = catalogItems.find((entry) => entry.id === initialCatalogItemId);
    if (item && !selectedCatalogItem) {
      setSelectedCatalogItem(item);
      formik.resetForm({ values: getBareMetalInstanceInitialValues(item) });
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
      const schema = buildBareMetalInstanceStepSchema(stepId as BareMetalWizardStepId, t);
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
              setActiveStep(String(step.id) as BareMetalWizardStepId);
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
                  <BareMetalInstanceWizardBody stepId={step} catalogItem={selectedCatalogItem} />
                )}
              </WizardStep>
            ))}
          </Wizard>
        </PageSection>
      </FieldValidationProvider>
    </FormikProvider>
  );
};

const BareMetalInstanceCreateWizard = (props: Props) => (
  <BareMetalInstanceWizardContent {...props} />
);

export default BareMetalInstanceCreateWizard;
