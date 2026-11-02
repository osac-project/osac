import { useCallback, useMemo, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { Spinner, WizardStep } from '@patternfly/react-core';

import type { ComputeInstanceCatalogItem } from '@osac/types';
import { ComputeInstanceCatalogItems, ComputeInstanceTemplates } from '@osac/types';

import {
  buildComputeCatalogItemCreatePayload,
  buildComputeCatalogItemUpdateRequest,
} from './payload';
import {
  type ComputeCatalogWizardStepId,
  buildComputeCatalogStepSchema,
  computeCatalogStepHasErrors,
} from './validation';
import {
  type ComputeCatalogItemValues,
  getComputeCatalogItemDiskImageId,
  getComputeCatalogItemInitialValues,
} from './values';
import VMCatalogAccessStep from './VMCatalogAccessStep';
import VMCatalogConfigurationStep from './VMCatalogConfigurationStep';
import VMCatalogReviewStep from './VMCatalogReviewStep';
import VMCatalogStorageStep from './VMCatalogStorageStep';
import { useCreateResource, useUpdateResource } from '../../../api/use-resource';
import { useDiskImage } from '../../../api/v1/disk-image';
import { useSession } from '../../../hooks/use-session';
import { useTranslation } from '../../../hooks/useTranslation';
import CatalogGeneralStep from '../../CatalogItem/CatalogGeneralStep';
import ResourceWizardShell from '../../Resource/ResourceWizardShell';
import { OSACWizardFooter } from '../../Wizard/OSACWizardFooter';

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

  const diskImageId = getComputeCatalogItemDiskImageId(item);
  const { data: diskImage, isLoading: isDiskImageLoading } = useDiskImage(diskImageId);

  const initialValues = useMemo(
    () => getComputeCatalogItemInitialValues(item, diskImage),
    [diskImage, item],
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

  if (diskImageId && isDiskImageLoading) {
    return <Spinner aria-label={t('Loading disk image')} />;
  }

  return (
    <ResourceWizardShell<ComputeCatalogItemValues, ComputeCatalogWizardStepId>
      initialValues={initialValues}
      validationSchema={validationSchema}
      onSubmit={onSubmit}
      parentTo="/catalog"
      parentLabel={t('Catalog')}
      title={item ? t('Edit VM catalog item') : t('Create VM Catalog Item')}
      wizardAriaLabel={t('Compute catalog item wizard')}
      navAriaLabel={t('Create compute catalog item steps')}
      onStepChange={setCurrentStep}
      footer={
        <OSACWizardFooter
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
    </ResourceWizardShell>
  );
};

export default VMCatalogItemWizard;
