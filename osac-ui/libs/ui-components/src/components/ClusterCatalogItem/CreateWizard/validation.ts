import type { FormikErrors } from 'formik';
import type { TFunction } from 'i18next';
import * as yup from 'yup';

import type { ClusterCatalogItemValues } from './values';
import { buildCidrSchema } from '../../../validation/cidr-validation';
import {
  catalogGeneralStepSchema,
  catalogLockedResourceField,
  catalogLockedStringField,
} from '../../CatalogItem/catalogValidation';
import {
  clusterNetworkDoNotOverlapTest,
  clusterNodeSetRowSchema,
  uniqueNodeSetNamesTest,
} from '../../Cluster/validation';

export type ClusterCatalogWizardStepId = 'general' | 'configuration' | 'networking' | 'review';

const nodeSetField = (t: TFunction) =>
  yup.object({
    locked: yup.boolean(),
    value: yup
      .array()
      .of(clusterNodeSetRowSchema(t))
      .test('unique-node-set-names', uniqueNodeSetNamesTest(t))
      .when('locked', {
        is: true,
        then: (schema) =>
          schema.min(1, t('At least one node set is required when this field is locked')),
      }),
  });

const cidrField = (t: TFunction, label: string) =>
  catalogLockedStringField(buildCidrSchema(t, 'ipv4'), t(`${label} is required when locked`));

export const buildClusterCatalogStepSchema = (
  stepId: ClusterCatalogWizardStepId,
  t: TFunction,
  role: Parameters<typeof catalogGeneralStepSchema>[1],
): yup.AnyObjectSchema | undefined => {
  switch (stepId) {
    case 'general':
      return catalogGeneralStepSchema(t, role);
    case 'configuration':
      return yup.object({
        version: catalogLockedResourceField(t('Version is required when locked')),
        nodeSets: nodeSetField(t),
      });
    case 'networking':
      return yup
        .object({
          podCidr: cidrField(t, 'Pod CIDR'),
          serviceCidr: cidrField(t, 'Service CIDR'),
        })
        .test('service-cidr-no-overlap', clusterNetworkDoNotOverlapTest(t));
    case 'review':
      return undefined;
    default:
      return undefined;
  }
};

export const clusterCatalogStepHasErrors = (
  stepId: string,
  errors: FormikErrors<ClusterCatalogItemValues>,
): boolean => {
  switch (stepId) {
    case 'general':
      return Boolean(errors.scope || errors.metadata || errors.template);
    case 'configuration':
      return Boolean(errors.version || errors.nodeSets);
    case 'networking':
      return Boolean(errors.podCidr || errors.serviceCidr);
    default:
      return false;
  }
};
