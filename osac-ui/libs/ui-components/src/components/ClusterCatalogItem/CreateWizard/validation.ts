import type { FormikErrors } from 'formik';
import type { TFunction } from 'i18next';
import * as yup from 'yup';

import type { ClusterCatalogItemValues } from './values';
import { buildCidrSchema } from '../../../validation/cidr-validation';
import { resourceNameSchema } from '../../../validation/resource-name';
import {
  catalogGeneralStepSchema,
  catalogLockedResourceField,
  catalogLockedStringField,
} from '../../CatalogItem/catalogValidation';
import { clusterNetworkDoNotOverlapTest, uniqueNodeSetNamesTest } from '../../Cluster/validation';

export type ClusterCatalogWizardStepId = 'general' | 'configuration' | 'networking' | 'review';

const nodeSetRowSchema = (t: TFunction) =>
  yup.object({
    rowId: yup.string().required(),
    name: resourceNameSchema(t),
    hostType: yup.string().required(t('Host type is required')),
    size: yup
      .string()
      .required(t('Pool size is required'))
      .test('pool-size-positive', t('Pool size must be greater than zero'), (value) => {
        const parsed = Number(value?.trim());
        return Number.isFinite(parsed) && parsed > 0;
      }),
  });

const nodeSetField = (t: TFunction) =>
  yup.object({
    locked: yup.boolean(),
    value: yup
      .array()
      .of(nodeSetRowSchema(t))
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
