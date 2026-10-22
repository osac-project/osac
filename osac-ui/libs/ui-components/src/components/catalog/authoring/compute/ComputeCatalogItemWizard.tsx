import { useCallback, useMemo, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import {
  Breadcrumb,
  BreadcrumbItem,
  Button,
  PageSection,
  PageSectionTypes,
  Title,
  Wizard,
  WizardStep,
} from '@patternfly/react-core';
import { Formik } from 'formik';

import type { ComputeInstanceCatalogItem } from '@osac/types';
import { ComputeInstanceCatalogItems, ComputeInstanceTemplates } from '@osac/types';

import ComputeCatalogConfigurationStep from './ComputeCatalogConfigurationStep';
import ComputeCatalogReviewStep from './ComputeCatalogReviewStep';
import ComputeCatalogStorageStep from './ComputeCatalogStorageStep';
import {
  buildComputeCatalogItemCreatePayload,
  buildComputeCatalogItemUpdatePayload,
} from './payload';
import {
  type ComputeCatalogWizardStepId,
  buildComputeCatalogStepSchema,
  computeCatalogStepHasErrors,
} from './validation';
import { type ComputeCatalogItemValues, getComputeCatalogItemInitialValues } from './values';
import { useCreateResource, useUpdateResource } from '../../../../api/use-resource';
import { useSession } from '../../../../hooks/use-session';
import { useTranslation } from '../../../../hooks/useTranslation';
import { FieldValidationProvider } from '../../../Form/FieldValidationContext';
import LeaveFormConfirmation from '../../../Form/LeaveFormConfirmation';
import { OSACWizardFooter } from '../../../Wizard/OSACWizardFooter';
import CatalogGeneralStep from '../CatalogGeneralStep';

interface ComputeCatalogItemWizardProps {
  item?: ComputeInstanceCatalogItem;
}

const ComputeCatalogItemWizard = ({ item }: ComputeCatalogItemWizardProps) => {
  const { t } = useTranslation();
  const { role } = useSession();
  const navigate = useNavigate();
  const [currentStep, setCurrentStep] = useState<ComputeCatalogWizardStepId>('general');

  const {
    mutateAsync: createAsync,
    error: createErr,
    reset: resetCreate,
  } = useCreateResource(ComputeInstanceCatalogItems);
  const {
    mutateAsync: updateAsync,
    error: updateErr,
    reset: resetUpdate,
  } = useUpdateResource(ComputeInstanceCatalogItems);

  const handleErrorReset = useCallback(() => {
    resetCreate();
    resetUpdate();
  }, [resetCreate, resetUpdate]);

  const initialValues = useMemo(
    () => getComputeCatalogItemInitialValues(item),
    // only on initial render
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [],
  );

  const validationSchema = useMemo(
    () => buildComputeCatalogStepSchema(currentStep, t, role),
    [currentStep, t, role],
  );

  const onSubmit = async (values: ComputeCatalogItemValues) => {
    try {
      if (item) {
        await updateAsync({
          object: { id: item.id, ...buildComputeCatalogItemUpdatePayload(item, values) },
        });
        navigate(`/catalog/vm/${item.id}`);
      } else {
        const resp = await createAsync({ object: buildComputeCatalogItemCreatePayload(values) });
        navigate(resp.object?.id ? `/catalog/vm/${resp.object.id}` : '/catalog');
      }
    } catch {
      // nothing to do, tanstack handles the error
    }
  };

  return (
    <Formik<ComputeCatalogItemValues>
      initialValues={initialValues}
      validationSchema={validationSchema}
      onSubmit={onSubmit}
    >
      <>
        <PageSection hasBodyWrapper={false}>
          <Breadcrumb>
            <BreadcrumbItem>
              <Button variant="link" isInline onClick={() => navigate('/catalog')}>
                {t('Catalog')}
              </Button>
            </BreadcrumbItem>
            <BreadcrumbItem isActive>
              {item ? t('Edit catalog item') : t('Create catalog item')}
            </BreadcrumbItem>
          </Breadcrumb>
          <Title headingLevel="h1" size="3xl">
            {item ? t('Edit catalog item') : t('Create catalog item')}
          </Title>
        </PageSection>
        <FieldValidationProvider>
          <LeaveFormConfirmation />
          <PageSection
            hasBodyWrapper={false}
            type={PageSectionTypes.wizard}
            aria-label={t('Compute catalog item wizard')}
          >
            <Wizard
              navAriaLabel={t('Create compute catalog item steps')}
              isVisitRequired
              footer={
                <OSACWizardFooter
                  onCancel={() => navigate('/catalog')}
                  stepHasErrors={computeCatalogStepHasErrors}
                  isEdit={!!item}
                  error={createErr || updateErr}
                  onErrorReset={handleErrorReset}
                />
              }
              onStepChange={(_, step) => {
                setCurrentStep(step.id as ComputeCatalogWizardStepId);
              }}
            >
              <WizardStep id="general" name={t('General')}>
                {currentStep === 'general' && (
                  <CatalogGeneralStep
                    isEdit={!!item}
                    templateService={ComputeInstanceTemplates}
                    templateFieldId="compute-catalog-item-template"
                  />
                )}
              </WizardStep>
              <WizardStep id="configuration" name={t('Compute Configuration')}>
                {currentStep === 'configuration' && <ComputeCatalogConfigurationStep />}
              </WizardStep>
              <WizardStep id="storage" name={t('Storage')}>
                {currentStep === 'storage' && <ComputeCatalogStorageStep />}
              </WizardStep>
              <WizardStep id="review" name={t('Review')}>
                {currentStep === 'review' && <ComputeCatalogReviewStep />}
              </WizardStep>
            </Wizard>
          </PageSection>
        </FieldValidationProvider>
      </>
    </Formik>
  );
};

export default ComputeCatalogItemWizard;
