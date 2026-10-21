import type { FormikErrors } from 'formik';
import type { TFunction } from 'i18next';
import * as yup from 'yup';

import type { UserRole } from '../../../../shellTypes';
import { resourceNameSchema } from '../../../../validation/resource-name';
import type { CatalogItemScope } from '../CatalogGeneralStep';
import type { BareMetalCatalogItemValues } from './values';

export type BareMetalCatalogWizardStepId = 'general' | 'configuration' | 'review';

const requiredResourceSelectValue = (message: string) =>
  yup
    .object({ id: yup.string(), name: yup.string() })
    .test('resource-select-required', message, (value) => Boolean(value?.id?.trim()));

const catalogLockedStringField = (valueSchema: yup.StringSchema, requiredMessage: string) =>
  yup.object({
    locked: yup.boolean(),
    value: valueSchema.when('locked', {
      is: true,
      then: (schema) => schema.required(requiredMessage),
    }),
  });

export const buildBareMetalCatalogStepSchema = (
  stepId: BareMetalCatalogWizardStepId,
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
            description: yup.string(),
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
        template: requiredResourceSelectValue(t('Provisioning template is required')),
      });
    case 'configuration':
      return yup.object({
        instanceType: catalogLockedStringField(
          yup.string().trim(),
          t('Instance type is required when locked'),
        ),
        diskImage: yup
          .object({
            locked: yup.boolean(),
            value: yup.object({ id: yup.string(), name: yup.string() }),
          })
          .test(
            'resource-select-required-when-locked',
            t('Disk image is required when locked'),
            function requiredWhenLocked(field) {
              const id = field?.value?.id;
              if (!field?.locked || (typeof id === 'string' && id.trim())) {
                return true;
              }
              return this.createError({
                path: `${this.path}.value.id`,
                message: t('Disk image is required when locked'),
              });
            },
          ),
        userData: catalogLockedStringField(yup.string(), t('User data is required when locked')),
      });
    case 'review':
      return undefined;
    default:
      return undefined;
  }
};

export const bareMetalCatalogStepHasErrors = (
  stepId: string,
  errors: FormikErrors<BareMetalCatalogItemValues>,
): boolean => {
  switch (stepId) {
    case 'general':
      return Boolean(errors.scope || errors.metadata || errors.template);
    case 'configuration':
      return Boolean(errors.instanceType || errors.diskImage || errors.userData);
    default:
      return false;
  }
};
