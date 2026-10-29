import type { FormikErrors } from 'formik';
import type { TFunction } from 'i18next';
import * as yup from 'yup';

import type { UserRole } from '../../../../shellTypes';
import { buildCidrSchema, cidrsOverlap, isValidCidr } from '../../../../validation/cidr-validation';
import { resourceNameSchema } from '../../../../validation/resource-name';
import type { CatalogItemScope } from '../CatalogGeneralStep';
import type { ClusterCatalogItemValues } from './values';

export type ClusterCatalogWizardStepId = 'general' | 'configuration' | 'networking' | 'review';

const catalogLockedStringField = (valueSchema: yup.StringSchema, requiredMessage: string) =>
  yup.object({
    locked: yup.boolean(),
    value: valueSchema.when('locked', {
      is: true,
      then: (schema) => schema.required(requiredMessage),
    }),
  });

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
  role: UserRole,
): yup.AnyObjectSchema | undefined => {
  const isAdmin = role === 'admin';

  switch (stepId) {
    case 'general':
      return yup.object({
        scope: yup.string(),
        metadata: yup
          .object({
            name: resourceNameSchema(t),
            project: yup.string(),
            tenant: yup.object({ id: yup.string(), name: yup.string() }),
          })
          .test('tenant-required', t('Tenant is required'), function tenantRequired(value) {
            if (!isAdmin) {
              return true;
            }
            const scope = (this.parent as { scope?: CatalogItemScope } | undefined)?.scope;
            if (scope !== 'tenant' || value?.tenant?.id?.trim()) {
              return true;
            }
            return this.createError({
              path: `${this.path}.tenant`,
              message: t('Tenant is required'),
            });
          }),
        template: yup
          .object({ id: yup.string(), name: yup.string() })
          .test('template-required', t('Provisioning template is required'), (value) =>
            Boolean(value?.id?.trim()),
          ),
      });
    case 'configuration':
      return yup.object({
        version: catalogLockedStringField(
          yup.string().trim(),
          t('Version is required when locked'),
        ),
        nodeSets: nodeSetField(t),
      });
    case 'networking':
      return yup
        .object({
          podCidr: cidrField(t, 'Pod CIDR'),
          serviceCidr: cidrField(t, 'Service CIDR'),
        })
        .test(
          'service-cidr-no-overlap',
          t('Service CIDR must not overlap the pod CIDR.'),
          function networkCidrsDoNotOverlap(network) {
            const podCidr = network?.podCidr?.value ?? '';
            const serviceCidr = network?.serviceCidr?.value ?? '';
            if (!podCidr.trim() || !serviceCidr.trim()) {
              return true;
            }
            if (!isValidCidr(podCidr, 'ipv4') || !isValidCidr(serviceCidr, 'ipv4')) {
              return true;
            }
            if (!cidrsOverlap(podCidr, serviceCidr, 'ipv4')) {
              return true;
            }
            return this.createError({
              path: `${this.path}.serviceCidr.value`,
              message: t('Service CIDR must not overlap the pod CIDR.'),
            });
          },
        );
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
