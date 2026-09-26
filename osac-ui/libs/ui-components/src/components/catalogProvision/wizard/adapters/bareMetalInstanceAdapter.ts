import { useMemo } from 'react';
import type { MessageInitShape } from '@bufbuild/protobuf';

import type { BareMetalInstanceCatalogItem } from '@osac/types';
import { BareMetalInstanceSchema } from '@osac/types';

import BareMetalConfigurationStep from './bareMetalInstance/BareMetalConfigurationStep';
import BareMetalGeneralStep from './bareMetalInstance/BareMetalGeneralStep';
import { BareMetalNetworkingStep } from './bareMetalInstance/BareMetalNetworkingStep';
import { BareMetalReviewStep } from './bareMetalInstance/BareMetalReviewStep';
import {
  type BareMetalInstanceWizardValues,
  applyBmCatalogDefaults,
  createEmptyBareMetalInstanceValues,
} from './bareMetalInstance/fields';
import { buildBareMetalInstanceCreatePayload } from './bareMetalInstance/payload';
import { buildBareMetalInstanceStepSchema } from './bareMetalInstance/schemas';
import type { CatalogProvisionAdapter } from './types';
import { useBareMetalInstanceCatalogItems } from '../../../../api/v1/baremetal-instance';
import { useTranslation } from '../../../../hooks/useTranslation';

export const useBareMetalInstanceAdapter = (): CatalogProvisionAdapter<
  BareMetalInstanceCatalogItem,
  BareMetalInstanceWizardValues,
  MessageInitShape<typeof BareMetalInstanceSchema>
> => {
  const { t } = useTranslation();

  return useMemo(
    () => ({
      kind: 'bare_metal_instance',
      useCatalogItems: () => {
        const query = useBareMetalInstanceCatalogItems();
        return {
          data: query.data?.items ?? [],
          isPending: query.isPending,
          isError: query.isError,
          refetch: () => {
            void query.refetch();
          },
        };
      },
      getInitialValues: (_catalogItem) => createEmptyBareMetalInstanceValues(),
      buildCreatePayload: (values) => buildBareMetalInstanceCreatePayload(values),
      ConfigurationStep: BareMetalConfigurationStep,
      GeneralStep: BareMetalGeneralStep,
      NetworkingStep: BareMetalNetworkingStep,
      ReviewStep: BareMetalReviewStep,
      getStepValidationSchema: (catalogItem, stepId) =>
        buildBareMetalInstanceStepSchema(catalogItem, stepId, t),
      onCatalogItemSelected: (item, helpers) => {
        helpers.resetForm({
          values: {
            ...createEmptyBareMetalInstanceValues(),
            catalogItemId: item.id,
          },
        });
        applyBmCatalogDefaults(item, helpers, t);
      },
      wizardTitleKey: t('Provision bare metal'),
      wizardDescriptionKey: t('Provision a bare metal instance from a catalog item.'),
      breadcrumbCreateLabelKey: t('Provision bare metal'),
      ariaLabelKey: t('Bare metal provisioning wizard'),
    }),
    [t],
  );
};
