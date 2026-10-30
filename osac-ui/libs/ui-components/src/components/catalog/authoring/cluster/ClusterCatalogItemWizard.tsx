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

import type { ClusterCatalogItem } from '@osac/types';
import { ClusterCatalogItems, ClusterTemplates } from '@osac/types';

import ClusterCatalogConfigurationStep from './ClusterCatalogConfigurationStep';
import ClusterCatalogNetworkingStep from './ClusterCatalogNetworkingStep';
import ClusterCatalogReviewStep from './ClusterCatalogReviewStep';
import {
  buildClusterCatalogItemCreatePayload,
  buildClusterCatalogItemUpdatePayload,
} from './payload';
import {
  type ClusterCatalogWizardStepId,
  buildClusterCatalogStepSchema,
  clusterCatalogStepHasErrors,
} from './validation';
import { type ClusterCatalogItemValues, getClusterCatalogItemInitialValues } from './values';
import { useCreateResource, useUpdateResource } from '../../../../api/use-resource';
import { useSession } from '../../../../hooks/use-session';
import { useTranslation } from '../../../../hooks/useTranslation';
import { FieldValidationProvider } from '../../../Form/FieldValidationContext';
import LeaveFormConfirmation from '../../../Form/LeaveFormConfirmation';
import { OSACWizardFooter } from '../../../Wizard/OSACWizardFooter';
import CatalogGeneralStep from '../CatalogGeneralStep';

interface ClusterCatalogItemWizardProps {
  item?: ClusterCatalogItem;
}

const ClusterCatalogItemWizard = ({ item }: ClusterCatalogItemWizardProps) => {
  const { t } = useTranslation();
  const { role } = useSession();
  const navigate = useNavigate();
  const [currentStep, setCurrentStep] = useState<ClusterCatalogWizardStepId>('general');

  const {
    mutateAsync: createAsync,
    error: createErr,
    reset: resetCreate,
  } = useCreateResource(ClusterCatalogItems);
  const {
    mutateAsync: updateAsync,
    error: updateErr,
    reset: resetUpdate,
  } = useUpdateResource(ClusterCatalogItems);

  const handleErrorReset = useCallback(() => {
    resetCreate();
    resetUpdate();
  }, [resetCreate, resetUpdate]);

  const initialValues = useMemo(
    () => getClusterCatalogItemInitialValues(item),
    // only on initial render
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [],
  );

  const validationSchema = useMemo(
    () => buildClusterCatalogStepSchema(currentStep, t, role),
    [currentStep, role, t],
  );

  const onSubmit = async (values: ClusterCatalogItemValues) => {
    try {
      if (item) {
        await updateAsync({
          object: { id: item.id, ...buildClusterCatalogItemUpdatePayload(item, values) },
        });
        navigate(`/catalog/cluster/${item.id}`);
      } else {
        const resp = await createAsync({ object: buildClusterCatalogItemCreatePayload(values) });
        navigate(resp.object?.id ? `/catalog/cluster/${resp.object.id}` : '/catalog');
      }
    } catch {
      // nothing to do, tanstack handles the error
    }
  };

  return (
    <Formik<ClusterCatalogItemValues>
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
              {item ? t('Edit cluster catalog item') : t('Create cluster catalog item')}
            </BreadcrumbItem>
          </Breadcrumb>
          <Title headingLevel="h1" size="3xl">
            {item ? t('Edit cluster catalog item') : t('Create cluster catalog item')}
          </Title>
        </PageSection>
        <FieldValidationProvider>
          <LeaveFormConfirmation />
          <PageSection
            hasBodyWrapper={false}
            type={PageSectionTypes.wizard}
            aria-label={t('Cluster catalog item wizard')}
          >
            <Wizard
              navAriaLabel={t('Create cluster catalog item steps')}
              isVisitRequired
              footer={
                <OSACWizardFooter
                  onCancel={() => navigate('/catalog')}
                  stepHasErrors={clusterCatalogStepHasErrors}
                  isEdit={!!item}
                  error={createErr || updateErr}
                  onErrorReset={handleErrorReset}
                />
              }
              onStepChange={(_, step) => {
                setCurrentStep(step.id as ClusterCatalogWizardStepId);
              }}
            >
              <WizardStep id="general" name={t('General')}>
                {currentStep === 'general' && (
                  <CatalogGeneralStep
                    isEdit={!!item}
                    templateService={ClusterTemplates}
                    templateFieldId="cluster-catalog-item-template"
                  />
                )}
              </WizardStep>
              <WizardStep id="configuration" name={t('Configuration')}>
                {currentStep === 'configuration' && <ClusterCatalogConfigurationStep />}
              </WizardStep>
              <WizardStep id="networking" name={t('Networking')}>
                {currentStep === 'networking' && <ClusterCatalogNetworkingStep />}
              </WizardStep>
              <WizardStep id="review" name={t('Review')}>
                {currentStep === 'review' && <ClusterCatalogReviewStep />}
              </WizardStep>
            </Wizard>
          </PageSection>
        </FieldValidationProvider>
      </>
    </Formik>
  );
};

export default ClusterCatalogItemWizard;
