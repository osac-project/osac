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

import type { BareMetalInstanceCatalogItem } from '@osac/types';
import { BareMetalInstanceCatalogItems, BareMetalInstanceTemplates } from '@osac/types';

import BareMetalCatalogConfigurationStep from './BareMetalCatalogConfigurationStep';
import BareMetalCatalogReviewStep from './BareMetalCatalogReviewStep';
import {
  buildBareMetalCatalogItemCreatePayload,
  buildBareMetalCatalogItemUpdatePayload,
} from './payload';
import {
  type BareMetalCatalogWizardStepId,
  bareMetalCatalogStepHasErrors,
  buildBareMetalCatalogStepSchema,
} from './validation';
import { type BareMetalCatalogItemValues, getBareMetalCatalogItemInitialValues } from './values';
import { useCreateResource, useUpdateResource } from '../../../../api/use-resource';
import { useSession } from '../../../../hooks/use-session';
import { useTranslation } from '../../../../hooks/useTranslation';
import { FieldValidationProvider } from '../../../Form/FieldValidationContext';
import LeaveFormConfirmation from '../../../Form/LeaveFormConfirmation';
import { OSACWizardFooter } from '../../../Wizard/OSACWizardFooter';
import CatalogGeneralStep from '../CatalogGeneralStep';

interface BareMetalCatalogItemWizardProps {
  item?: BareMetalInstanceCatalogItem;
}

const BareMetalCatalogItemWizard = ({ item }: BareMetalCatalogItemWizardProps) => {
  const { t } = useTranslation();
  const { role } = useSession();
  const navigate = useNavigate();
  const [currentStep, setCurrentStep] = useState<BareMetalCatalogWizardStepId>('general');

  const {
    mutateAsync: createAsync,
    error: createErr,
    reset: resetCreate,
  } = useCreateResource(BareMetalInstanceCatalogItems);
  const {
    mutateAsync: updateAsync,
    error: updateErr,
    reset: resetUpdate,
  } = useUpdateResource(BareMetalInstanceCatalogItems);

  const handleErrorReset = useCallback(() => {
    resetCreate();
    resetUpdate();
  }, [resetCreate, resetUpdate]);

  const initialValues = useMemo(
    () => getBareMetalCatalogItemInitialValues(item),
    // only on initial render
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [],
  );

  const validationSchema = useMemo(
    () => buildBareMetalCatalogStepSchema(currentStep, t, role),
    [currentStep, role, t],
  );

  const onSubmit = async (values: BareMetalCatalogItemValues) => {
    try {
      if (item) {
        await updateAsync({
          object: { id: item.id, ...buildBareMetalCatalogItemUpdatePayload(item, values) },
        });
        navigate(`/catalog/bm/${item.id}`);
      } else {
        const resp = await createAsync({
          object: buildBareMetalCatalogItemCreatePayload(values),
        });
        navigate(resp.object?.id ? `/catalog/bm/${resp.object.id}` : '/catalog');
      }
    } catch {
      // nothing to do, tanstack handles the error
    }
  };

  return (
    <Formik<BareMetalCatalogItemValues>
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
              {item ? t('Edit bare metal catalog item') : t('Create bare metal catalog item')}
            </BreadcrumbItem>
          </Breadcrumb>
          <Title headingLevel="h1" size="3xl">
            {item ? t('Edit bare metal catalog item') : t('Create bare metal catalog item')}
          </Title>
        </PageSection>
        <FieldValidationProvider>
          <LeaveFormConfirmation />
          <PageSection
            hasBodyWrapper={false}
            type={PageSectionTypes.wizard}
            aria-label={t('Bare metal catalog item wizard')}
          >
            <Wizard
              navAriaLabel={t('Create bare metal catalog item steps')}
              isVisitRequired
              footer={
                <OSACWizardFooter
                  onCancel={() => navigate('/catalog')}
                  stepHasErrors={bareMetalCatalogStepHasErrors}
                  isEdit={!!item}
                  error={createErr || updateErr}
                  onErrorReset={handleErrorReset}
                />
              }
              onStepChange={(_, step) => {
                setCurrentStep(step.id as BareMetalCatalogWizardStepId);
              }}
            >
              <WizardStep id="general" name={t('General')}>
                {currentStep === 'general' && (
                  <CatalogGeneralStep
                    isEdit={!!item}
                    templateService={BareMetalInstanceTemplates}
                    templateFieldId="bare-metal-catalog-item-template"
                  />
                )}
              </WizardStep>
              <WizardStep id="configuration" name={t('Configuration')}>
                {currentStep === 'configuration' && <BareMetalCatalogConfigurationStep />}
              </WizardStep>
              <WizardStep id="review" name={t('Review')}>
                {currentStep === 'review' && <BareMetalCatalogReviewStep />}
              </WizardStep>
            </Wizard>
          </PageSection>
        </FieldValidationProvider>
      </>
    </Formik>
  );
};

export default BareMetalCatalogItemWizard;
