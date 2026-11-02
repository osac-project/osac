import type { TFunction } from 'i18next';
import * as yup from 'yup';

import type { UserRole } from '../../../shellTypes';
import { resourceNameSchema } from '../../../validation/resource-name';

export const catalogRequiredResourceSelectValue = (message: string) =>
  yup
    .object({ id: yup.string(), name: yup.string() })
    .test('resource-select-required', message, (value) => Boolean(value?.id?.trim()));

export const catalogLockedStringField = (valueSchema: yup.StringSchema, requiredMessage: string) =>
  yup.object({
    locked: yup.boolean(),
    value: valueSchema.when('locked', {
      is: true,
      then: (schema) => schema.trim().required(requiredMessage),
    }),
  });

export const catalogLockedResourceField = (requiredMessage: string) =>
  yup
    .object({
      locked: yup.boolean(),
      value: yup.object({ id: yup.string(), name: yup.string() }),
    })
    .test(
      'resource-select-required-when-locked',
      requiredMessage,
      function requiredWhenLocked(field) {
        const id = field?.value?.id;
        if (!field?.locked || (typeof id === 'string' && id.trim())) {
          return true;
        }
        return this.createError({ path: `${this.path}.value.id`, message: requiredMessage });
      },
    );

export const catalogGeneralStepSchema = (t: TFunction, role: UserRole) => {
  const isAdmin = role === 'admin';

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
        const scope = (this.parent as { scope?: string } | undefined)?.scope;
        if (scope !== 'tenant' || value?.tenant?.id?.trim()) {
          return true;
        }
        return this.createError({
          path: `${this.path}.tenant`,
          message: t('Tenant is required'),
        });
      }),
    template: catalogRequiredResourceSelectValue(t('Provisioning template is required')),
  });
};
