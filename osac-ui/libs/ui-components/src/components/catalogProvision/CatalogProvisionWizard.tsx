import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react';
import {
  Alert,
  Button,
  Flex,
  Modal,
  ModalBody,
  ModalFooter,
  ModalHeader,
  PageSection,
  PageSectionTypes,
  Stack,
  StackItem,
  Wizard,
  WizardFooterWrapper,
  WizardStep,
  useWizardContext,
} from '@patternfly/react-core';
import { type FormikProps, FormikProvider, useFormik } from 'formik';

import type { CatalogProvisionKind } from './catalogFieldDefinition';
import type {
  CatalogProvisionPayload,
  CatalogProvisionWizardValues,
} from './catalogProvisionTypes';
import { useTranslation } from '../../hooks/useTranslation';
import { CatalogItem } from '../catalog/catalogItemDisplay';
import { FieldValidationProvider } from '../Form/FieldValidationContext';
import { useBareMetalInstanceAdapter } from './wizard/adapters/bareMetalInstanceAdapter';
import { useClusterAdapter } from './wizard/adapters/clusterAdapter';
import { useComputeInstanceAdapter } from './wizard/adapters/computeInstanceAdapter';
import type { CatalogProvisionAdapter } from './wizard/adapters/types';
import {
  STEP_LABEL_KEYS,
  WIZARD_STEP_IDS,
  type WizardStepId,
  getWizardOrderedSteps,
} from './wizard/stepIds';
import { CatalogStep } from './wizard/steps/WizardSteps';
import { WizardValidationProvider } from './wizard/WizardValidationContext';

export type {
  CatalogProvisionPayload,
  CatalogProvisionWizardValues,
} from './catalogProvisionTypes';

const hasWizardUnsavedProgress = (values: { catalogItemId?: string }): boolean =>
  Boolean(values.catalogItemId?.trim());

export type CatalogProvisionWizardCloseHandler = {
  requestClose: () => void;
  pending: boolean;
};

interface Props {
  kind: CatalogProvisionKind;
  initialCatalogItemId?: string;
  onProvision: (payload: CatalogProvisionPayload) => void | Promise<void>;
  onClosed?: () => void;
  onCloseHandlerChange?: (handler: CatalogProvisionWizardCloseHandler) => void;
}

type ErasedCatalogAdapter = CatalogProvisionAdapter<
  CatalogItem,
  CatalogProvisionWizardValues,
  CatalogProvisionPayload
>;

interface WizardFooterProps {
  formik: FormikProps<CatalogProvisionWizardValues>;
  catalogItem: CatalogItem | null;
  setActiveStepId: (stepId: WizardStepId) => void;
  setProvisionError: (message: string | undefined) => void;
  setValidationAlert: (visible: boolean) => void;
  pending: boolean;
  setPending: (pending: boolean) => void;
  onProvision: Props['onProvision'];
  buildCreatePayload: ErasedCatalogAdapter['buildCreatePayload'];
  close: (options?: { notifyClosed?: boolean }) => void;
  requestClose: () => void;
}

const isWizardStepId = (stepId: string | number | undefined): stepId is WizardStepId =>
  typeof stepId === 'string' && WIZARD_STEP_IDS.includes(stepId);

const CatalogProvisionWizardFooter = ({
  formik,
  catalogItem,
  setActiveStepId,
  setProvisionError,
  setValidationAlert,
  pending,
  setPending,
  onProvision,
  buildCreatePayload,
  close,
  requestClose,
}: WizardFooterProps) => {
  const { t } = useTranslation();
  const { activeStep, goToStepByIndex } = useWizardContext();
  const activeStepId = isWizardStepId(activeStep?.id) ? activeStep.id : 'catalog';

  // Keep Formik's validationSchema in sync with the visible step before blur/Next run.
  useLayoutEffect(() => {
    setActiveStepId(activeStepId);
  }, [activeStepId, setActiveStepId]);
  const stepIndex = activeStep?.index ?? 1;
  const isFirst = stepIndex <= 1;
  const isReview = activeStepId === 'review';

  const handleBack = useCallback(() => {
    if (isFirst || pending) {
      return;
    }
    setProvisionError(undefined);
    setValidationAlert(false);
    goToStepByIndex(stepIndex - 1);
  }, [goToStepByIndex, isFirst, pending, setProvisionError, setValidationAlert, stepIndex]);

  const { values } = formik;

  // validateForm() uses the step-scoped validationSchema wired in CatalogProvisionWizardInner.
  const validateCurrentStep = useCallback(async () => {
    const errors = await formik.validateForm();
    if (Object.keys(errors).length > 0) {
      setValidationAlert(true);
      return false;
    }
    setValidationAlert(false);
    return true;
  }, [formik, setValidationAlert]);

  const handleNextOrCreate = useCallback(() => {
    if (pending) {
      return;
    }

    if (isReview) {
      if (!catalogItem) {
        setProvisionError(t('catalogProvision.validation.catalogItemRequired'));
        return;
      }

      setPending(true);
      setProvisionError(undefined);
      const payload = buildCreatePayload(values, catalogItem);
      void Promise.resolve(onProvision(payload))
        .then(() => {
          close({ notifyClosed: false });
        })
        .catch((error) => {
          setProvisionError(
            error instanceof Error ? error.message : t('catalogProvision.errors.provisionFailed'),
          );
        })
        .finally(() => {
          setPending(false);
        });
      return;
    }

    void validateCurrentStep().then((isValid) => {
      if (!isValid) {
        return;
      }
      setProvisionError(undefined);
      goToStepByIndex(stepIndex + 1);
    });
  }, [
    buildCreatePayload,
    catalogItem,
    close,
    goToStepByIndex,
    isReview,
    onProvision,
    pending,
    setPending,
    setProvisionError,
    stepIndex,
    t,
    validateCurrentStep,
    values,
  ]);

  return (
    <Flex
      justifyContent={{ default: 'justifyContentFlexStart' }}
      alignItems={{ default: 'alignItemsCenter' }}
      flexWrap={{ default: 'wrap' }}
      gap={{ default: 'gapMd' }}
    >
      <Button
        variant="secondary"
        onClick={handleBack}
        isDisabled={isFirst || pending}
        isAriaDisabled={isFirst || pending}
      >
        {t('catalogProvision.actions.back')}
      </Button>
      <Button
        type="button"
        variant="primary"
        onClick={handleNextOrCreate}
        isDisabled={pending}
        isLoading={pending}
      >
        {isReview ? t('catalogProvision.actions.create') : t('catalogProvision.actions.next')}
      </Button>
      <Button variant="link" onClick={requestClose} isDisabled={pending}>
        {t('catalogProvision.actions.cancel')}
      </Button>
    </Flex>
  );
};

interface WizardBodyProps {
  adapter: ErasedCatalogAdapter;
  stepId: WizardStepId;
  catalogItem: CatalogItem | null;
  provisionError?: string;
  validationAlert: boolean;
}

const WizardStepBody = ({
  adapter,
  stepId,
  catalogItem,
  provisionError,
  validationAlert,
}: WizardBodyProps) => {
  const { t } = useTranslation();
  const ConfigurationStep = adapter.ConfigurationStep;
  const StorageStep = adapter.StorageStep;
  const AccessStep = adapter.AccessStep;
  const NetworkingStep = adapter.NetworkingStep;
  const GeneralStepComponent = adapter.GeneralStep;
  const ReviewStepComponent = adapter.ReviewStep;

  return (
    <FieldValidationProvider showErrors={validationAlert}>
      <Stack hasGutter>
        {validationAlert ? (
          <StackItem>
            <Alert variant="danger" isInline title={t('catalogProvision.validation.stepInvalid')} />
          </StackItem>
        ) : null}
        {provisionError ? (
          <StackItem>
            <Alert variant="danger" isInline title={t('catalogProvision.errors.provisionTitle')}>
              {provisionError}
            </Alert>
          </StackItem>
        ) : null}
        {stepId === 'catalog' ? <CatalogStep adapter={adapter} /> : null}
        {stepId === 'general' ? <GeneralStepComponent catalogItem={catalogItem} /> : null}
        {stepId === 'configuration' ? <ConfigurationStep catalogItem={catalogItem} /> : null}
        {stepId === 'storage' && StorageStep ? <StorageStep catalogItem={catalogItem} /> : null}
        {stepId === 'networking' ? <NetworkingStep catalogItem={catalogItem} /> : null}
        {stepId === 'access' && AccessStep ? <AccessStep catalogItem={catalogItem} /> : null}
        {stepId === 'review' ? <ReviewStepComponent catalogItem={catalogItem} /> : null}
      </Stack>
    </FieldValidationProvider>
  );
};

interface InnerProps extends Props {
  adapter: ErasedCatalogAdapter;
  initialValues: CatalogProvisionWizardValues;
}

const CatalogProvisionWizardInner = ({
  adapter,
  initialCatalogItemId,
  initialValues,
  onProvision,
  onClosed,
  onCloseHandlerChange,
}: InnerProps) => {
  const { t } = useTranslation();
  const orderedSteps = useMemo(() => getWizardOrderedSteps(adapter.kind), [adapter.kind]);
  const [wizardResetKey, setWizardResetKey] = useState(0);
  const [activeStepId, setActiveStepId] = useState<WizardStepId>('catalog');
  const [schemaCatalogItemId, setSchemaCatalogItemId] = useState(initialValues.catalogItemId);
  const [provisionError, setProvisionError] = useState<string | undefined>();
  const [validationAlert, setValidationAlert] = useState(false);
  const [pending, setPending] = useState(false);
  const [showCancelConfirm, setShowCancelConfirm] = useState(false);
  const { data: catalogItems = [] } = adapter.useCatalogItems();

  useEffect(() => {
    setActiveStepId('catalog');
  }, [wizardResetKey]);

  const selectedCatalogItem: CatalogItem | null = schemaCatalogItemId
    ? (catalogItems.find((item) => item.id === schemaCatalogItemId) ?? null)
    : null;

  // Swapped when the wizard step changes (see footer useLayoutEffect). Step-scoped
  // so validateOnBlur and Next's validateForm() never validate fields from other steps.
  const validationSchema = useMemo(
    () => adapter.getStepValidationSchema(selectedCatalogItem, activeStepId),
    [adapter, activeStepId, selectedCatalogItem],
  );

  const formik = useFormik<CatalogProvisionWizardValues>({
    initialValues,
    validationSchema,
    validateOnBlur: true,
    validateOnChange: false,
    onSubmit: () => undefined,
  });

  useEffect(() => {
    setSchemaCatalogItemId(formik.values.catalogItemId);
  }, [formik.values.catalogItemId]);

  const resetLocal = useCallback(() => {
    setProvisionError(undefined);
    setValidationAlert(false);
    setShowCancelConfirm(false);
    setWizardResetKey((key) => key + 1);
  }, []);

  const close = useCallback(
    (options?: { notifyClosed?: boolean }) => {
      resetLocal();
      if (options?.notifyClosed !== false) {
        onClosed?.();
      }
    },
    [onClosed, resetLocal],
  );

  return (
    <FormikProvider value={formik}>
      <CatalogProvisionWizardForm
        adapter={adapter}
        formik={formik}
        initialCatalogItemId={initialCatalogItemId}
        orderedSteps={orderedSteps}
        wizardResetKey={wizardResetKey}
        setActiveStepId={setActiveStepId}
        provisionError={provisionError}
        setProvisionError={setProvisionError}
        validationAlert={validationAlert}
        setValidationAlert={setValidationAlert}
        pending={pending}
        setPending={setPending}
        showCancelConfirm={showCancelConfirm}
        setShowCancelConfirm={setShowCancelConfirm}
        onProvision={onProvision}
        close={close}
        onCloseHandlerChange={onCloseHandlerChange}
        t={t}
      />
    </FormikProvider>
  );
};

interface FormProps {
  adapter: ErasedCatalogAdapter;
  formik: FormikProps<CatalogProvisionWizardValues>;
  initialCatalogItemId?: string;
  orderedSteps: readonly WizardStepId[];
  wizardResetKey: number;
  setActiveStepId: (stepId: WizardStepId) => void;
  provisionError?: string;
  setProvisionError: (message: string | undefined) => void;
  validationAlert: boolean;
  setValidationAlert: (visible: boolean) => void;
  pending: boolean;
  setPending: (pending: boolean) => void;
  showCancelConfirm: boolean;
  setShowCancelConfirm: (visible: boolean) => void;
  onProvision: Props['onProvision'];
  close: (options?: { notifyClosed?: boolean }) => void;
  onCloseHandlerChange?: Props['onCloseHandlerChange'];
  t: ReturnType<typeof useTranslation>['t'];
}

const CatalogProvisionWizardForm = ({
  adapter,
  formik,
  initialCatalogItemId,
  orderedSteps,
  wizardResetKey,
  setActiveStepId,
  provisionError,
  setProvisionError,
  validationAlert,
  setValidationAlert,
  pending,
  setPending,
  showCancelConfirm,
  setShowCancelConfirm,
  onProvision,
  close,
  onCloseHandlerChange,
  t,
}: FormProps) => {
  const { data: catalogItems = [] } = adapter.useCatalogItems();
  const selectedCatalogItem: CatalogItem | null = formik.values.catalogItemId
    ? (catalogItems.find((item) => item.id === formik.values.catalogItemId) ?? null)
    : null;

  const requestClose = useCallback(() => {
    if (pending) {
      return;
    }
    if (hasWizardUnsavedProgress(formik.values)) {
      setShowCancelConfirm(true);
      return;
    }
    close();
  }, [close, formik.values, pending, setShowCancelConfirm]);

  useEffect(() => {
    onCloseHandlerChange?.({ requestClose, pending });
  }, [onCloseHandlerChange, pending, requestClose]);

  const handleStepChange = useCallback(() => {
    setProvisionError(undefined);
    setValidationAlert(false);
  }, [setProvisionError, setValidationAlert]);

  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape' && !pending && !showCancelConfirm) {
        requestClose();
      }
    };
    document.addEventListener('keydown', onKeyDown);
    return () => {
      document.removeEventListener('keydown', onKeyDown);
    };
  }, [pending, requestClose, showCancelConfirm]);

  const deepLinkInitializedRef = useRef(false);

  // Deep-link create (/vms/create/:catalogItemId): apply catalog defaults once the item loads.
  useEffect(() => {
    if (deepLinkInitializedRef.current || !initialCatalogItemId) {
      return;
    }
    const item = catalogItems.find((entry) => entry.id === initialCatalogItemId);
    if (!item) {
      return;
    }
    deepLinkInitializedRef.current = true;
    void adapter.onCatalogItemSelected?.(item, formik);
  }, [adapter, catalogItems, formik, initialCatalogItemId]);

  const clearValidationAlert = useCallback(() => {
    setValidationAlert(false);
  }, [setValidationAlert]);

  return (
    <>
      {showCancelConfirm ? (
        <Modal
          variant="small"
          isOpen
          onClose={() => setShowCancelConfirm(false)}
          aria-labelledby="catalog-provision-wizard-cancel-title"
        >
          <ModalHeader
            title={t('catalogProvision.cancel.title')}
            titleIconVariant="warning"
            labelId="catalog-provision-wizard-cancel-title"
          />
          <ModalBody>{t('catalogProvision.cancel.body')}</ModalBody>
          <ModalFooter>
            <Button variant="link" onClick={() => setShowCancelConfirm(false)}>
              {t('catalogProvision.cancel.keepEditing')}
            </Button>
            <Button variant="primary" onClick={() => close()}>
              {t('catalogProvision.cancel.discard')}
            </Button>
          </ModalFooter>
        </Modal>
      ) : null}
      <WizardValidationProvider clearValidationAlert={clearValidationAlert}>
        <PageSection
          hasBodyWrapper={false}
          type={PageSectionTypes.wizard}
          aria-label={t(adapter.ariaLabelKey)}
        >
          <Wizard
            key={wizardResetKey}
            navAriaLabel={t('catalogProvision.wizard.navAria', {
              title: t(adapter.wizardTitleKey),
            })}
            isVisitRequired
            onStepChange={handleStepChange}
            footer={
              <WizardFooterWrapper>
                <CatalogProvisionWizardFooter
                  formik={formik}
                  catalogItem={selectedCatalogItem}
                  setActiveStepId={setActiveStepId}
                  setProvisionError={setProvisionError}
                  setValidationAlert={setValidationAlert}
                  pending={pending}
                  setPending={setPending}
                  onProvision={onProvision}
                  buildCreatePayload={adapter.buildCreatePayload}
                  close={close}
                  requestClose={requestClose}
                />
              </WizardFooterWrapper>
            }
          >
            {orderedSteps.map((stepId) => (
              <WizardStep key={stepId} id={stepId} name={STEP_LABEL_KEYS(t)[stepId]}>
                <WizardStepBody
                  adapter={adapter}
                  stepId={stepId}
                  catalogItem={selectedCatalogItem}
                  provisionError={provisionError}
                  validationAlert={validationAlert}
                />
              </WizardStep>
            ))}
          </Wizard>
        </PageSection>
      </WizardValidationProvider>
    </>
  );
};

export const CatalogProvisionWizard = ({
  kind,
  initialCatalogItemId,
  onProvision,
  onClosed,
  onCloseHandlerChange,
}: Props) => {
  const vmAdapter = useComputeInstanceAdapter();
  const clusterAdapter = useClusterAdapter();
  const bmAdapter = useBareMetalInstanceAdapter();
  const adapter = (
    kind === 'cluster' ? clusterAdapter : kind === 'bare_metal_instance' ? bmAdapter : vmAdapter
  ) as ErasedCatalogAdapter;
  const initialValues = useMemo(() => {
    const values = adapter.getInitialValues(null);
    if (initialCatalogItemId) {
      values.catalogItemId = initialCatalogItemId;
    }
    return values;
  }, [adapter, initialCatalogItemId]);

  return (
    <CatalogProvisionWizardInner
      kind={kind}
      adapter={adapter}
      initialCatalogItemId={initialCatalogItemId}
      initialValues={initialValues}
      onProvision={onProvision}
      onClosed={onClosed}
      onCloseHandlerChange={onCloseHandlerChange}
    />
  );
};
