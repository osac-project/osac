import { useCallback, useMemo, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { WizardStep } from '@patternfly/react-core';

import type { ComputeInstanceCatalogItem } from '@osac/types';
import { ComputeInstanceCatalogItems, ComputeInstanceTemplates } from '@osac/types';

import VMCatalogAccessStep from './VMCatalogAccessStep';
import VMCatalogConfigurationStep from './VMCatalogConfigurationStep';
import VMCatalogReviewStep from './VMCatalogReviewStep';
import VMCatalogStorageStep from './VMCatalogStorageStep';
import VMCatalogWizardFooter from './VMCatalogWizardFooter';
import CatalogItemWizardShell from '../CatalogItemWizardShell';
import {
  buildComputeCatalogItemCreatePayload,
  buildComputeCatalogItemUpdateRequest,
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
import CatalogGeneralStep from '../CatalogGeneralStep';

interface VMCatalogItemWizardProps {
  item?: ComputeInstanceCatalogItem;
}

const VMCatalogItemWizard = ({ item }: VMCatalogItemWizardProps) => {
  const { t } = useTranslation();
  const { role } = useSession();
  const navigate = useNavigate();
  const [currentStep, setCurrentStep] = useState<ComputeCatalogWizardStepId>('details');

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
        await updateAsync(buildComputeCatalogItemUpdateRequest(item, values));
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
    <CatalogItemWizardShell<ComputeCatalogItemValues, ComputeCatalogWizardStepId>
      initialValues={initialValues}
      validationSchema={validationSchema}
      onSubmit={onSubmit}
      title={item ? t('Edit VM catalog item') : t('Create VM Catalog Item')}
      wizardAriaLabel={t('Compute catalog item wizard')}
      navAriaLabel={t('Create compute catalog item steps')}
      onStepChange={setCurrentStep}
      footer={
        <VMCatalogWizardFooter
          currentStep={currentStep}
          onCancel={() => navigate('/catalog')}
          stepHasErrors={computeCatalogStepHasErrors}
          isEdit={!!item}
          error={createErr || updateErr}
          onErrorReset={handleErrorReset}
        />
      }
    >
      <WizardStep id="details" name={t('General')}>
        {currentStep === 'details' && (
          <CatalogGeneralStep
            isEdit={!!item}
            templateService={ComputeInstanceTemplates}
            templateFieldId="compute-catalog-item-template"
          />
        )}
      </WizardStep>
      <WizardStep id="configuration" name={t('Compute configuration')}>
        {currentStep === 'configuration' && <VMCatalogConfigurationStep />}
      </WizardStep>
      <WizardStep id="storage" name={t('Storage')}>
        {currentStep === 'storage' && <VMCatalogStorageStep />}
      </WizardStep>
      <WizardStep id="access-init" name={t('Access & Initial run')}>
        {currentStep === 'access-init' && <VMCatalogAccessStep />}
      </WizardStep>
      <WizardStep id="review" name={t('Review')}>
        {currentStep === 'review' && <VMCatalogReviewStep item={item} />}
      </WizardStep>
    </CatalogItemWizardShell>
  );
};

export default VMCatalogItemWizard;
