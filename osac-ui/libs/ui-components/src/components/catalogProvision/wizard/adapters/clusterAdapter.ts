import { useMemo } from 'react';
import { type MessageInitShape } from '@bufbuild/protobuf';

import { type ClusterCatalogItem, ClusterSchema } from '@osac/types';

import { applyClusterCatalogConfigurationDefaults } from './cluster/applyCatalogDefaults';
import { applyClusterCatalogGeneralDefaults } from './cluster/applyCatalogGeneralDefaults';
import { applyClusterCatalogNetworkingDefaults } from './cluster/applyCatalogNetworkingDefaults';
import ClusterConfigurationStep from './cluster/ClusterConfigurationStep';
import ClusterGeneralStep from './cluster/ClusterGeneralStep';
import { ClusterNetworkingStep } from './cluster/ClusterNetworkingStep';
import { ClusterReviewStep } from './cluster/ClusterReviewStep';
import type { ClusterWizardValues } from './cluster/fields';
import { buildClusterCreatePayload, createEmptyClusterValues } from './cluster/payload';
import { buildClusterStepSchema } from './cluster/schemas';
import type { CatalogProvisionAdapter } from './types';
import { useClusterCatalogItems } from '../../../../api/v1/cluster-catalog-item';
import { useTranslation } from '../../../../hooks/useTranslation';

export { buildClusterCreatePayload, createEmptyClusterValues } from './cluster/payload';

export const useClusterAdapter = (): CatalogProvisionAdapter<
  ClusterCatalogItem,
  ClusterWizardValues,
  MessageInitShape<typeof ClusterSchema>
> => {
  const { t } = useTranslation();

  return useMemo(
    () => ({
      kind: 'cluster' as const,
      useCatalogItems: () => {
        const query = useClusterCatalogItems();
        return {
          data: query.data?.items ?? [],
          isPending: query.isPending,
          isError: query.isError,
          refetch: () => {
            void query.refetch();
          },
        };
      },
      getInitialValues: () => createEmptyClusterValues(),
      buildCreatePayload: buildClusterCreatePayload,
      ConfigurationStep: ClusterConfigurationStep,
      NetworkingStep: ClusterNetworkingStep,
      GeneralStep: ClusterGeneralStep,
      getStepValidationSchema: (catalogItem, stepId) =>
        buildClusterStepSchema(catalogItem, stepId, t),
      ReviewStep: ClusterReviewStep,
      onCatalogItemSelected: (item, helpers) => {
        helpers.resetForm({
          values: {
            ...createEmptyClusterValues(),
            catalogItemId: item.id,
          },
        });
        applyClusterCatalogConfigurationDefaults(item, helpers, t);
        applyClusterCatalogGeneralDefaults(item, helpers, t);
        applyClusterCatalogNetworkingDefaults(item, helpers, t);
      },
      wizardTitleKey: t('Create cluster'),
      wizardDescriptionKey: t(
        'Select a catalog item, configure, and provision an OpenShift cluster.',
      ),
      breadcrumbCreateLabelKey: t('Create'),
      ariaLabelKey: t('Create cluster wizard'),
    }),
    [t],
  );
};
