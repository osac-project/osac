import { useMemo } from 'react';
import { type MessageInitShape } from '@bufbuild/protobuf';

import { type ComputeInstanceCatalogItem, ComputeInstanceSchema } from '@osac/types';

import { applyVmCatalogConfigurationDefaults } from './computeInstance/applyCatalogDefaults';
import { applyVmCatalogGeneralDefaults } from './computeInstance/applyCatalogGeneralDefaults';
import type { ComputeInstanceWizardValues } from './computeInstance/fields';
import {
  buildComputeInstanceCreatePayload,
  createEmptyComputeInstanceValues,
} from './computeInstance/payload';
import { buildComputeInstanceStepSchema } from './computeInstance/schemas';
import { VmConfigurationStep } from './computeInstance/VmConfigurationStep';
import VmGeneralStep from './computeInstance/VmGeneralStep';
import { VmNetworkingStep } from './computeInstance/VmNetworkingStep';
import { VmReviewStep } from './computeInstance/VmReviewStep';
import { VmStorageStep } from './computeInstance/VmStorageStep';
import type { CatalogProvisionAdapter } from './types';
import { useComputeInstanceCatalogItems } from '../../../../api/v1/compute-instance-catalog-item';
import { useTranslation } from '../../../../hooks/useTranslation';

export {
  buildComputeInstanceCreatePayload,
  createEmptyComputeInstanceValues,
} from './computeInstance/payload';

export const useComputeInstanceAdapter = (): CatalogProvisionAdapter<
  ComputeInstanceCatalogItem,
  ComputeInstanceWizardValues,
  MessageInitShape<typeof ComputeInstanceSchema>
> => {
  const { t } = useTranslation();

  return useMemo(
    () => ({
      kind: 'compute_instance' as const,
      useCatalogItems: () => {
        const query = useComputeInstanceCatalogItems();
        return {
          data: query.data ?? [],
          isPending: query.isPending,
          isError: query.isError,
          refetch: () => {
            void query.refetch();
          },
        };
      },
      getInitialValues: (_catalogItem) => createEmptyComputeInstanceValues(),
      buildCreatePayload: buildComputeInstanceCreatePayload,
      ConfigurationStep: VmConfigurationStep,
      StorageStep: VmStorageStep,
      NetworkingStep: VmNetworkingStep,
      GeneralStep: VmGeneralStep,
      getStepValidationSchema: (catalogItem, stepId) =>
        buildComputeInstanceStepSchema(catalogItem, stepId, t),
      ReviewStep: VmReviewStep,
      onCatalogItemSelected: (item, helpers) => {
        helpers.resetForm({
          values: {
            ...createEmptyComputeInstanceValues(),
            catalogItemId: item.id,
          },
        });
        applyVmCatalogConfigurationDefaults(item, helpers, t);
        applyVmCatalogGeneralDefaults(item, helpers);
      },
      wizardTitleKey: 'catalogProvision.vm.wizardTitle',
      wizardDescriptionKey: 'catalogProvision.vm.wizardDescription',
      breadcrumbCreateLabelKey: 'catalogProvision.vm.breadcrumbCreate',
      ariaLabelKey: 'catalogProvision.vm.ariaLabel',
    }),
    [t],
  );
};
