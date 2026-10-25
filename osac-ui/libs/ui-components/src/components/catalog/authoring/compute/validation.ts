import type { FormikErrors } from 'formik';
import type { TFunction } from 'i18next';
import * as yup from 'yup';

import type { UserRole } from '../../../../shellTypes';
import { resourceNameSchema } from '../../../../validation/resource-name';
import type { CatalogItemScope } from '../CatalogGeneralStep';
import type { ComputeCatalogItemValues } from './values';

export type ComputeCatalogWizardStepId =
  | 'details'
  | 'configuration'
  | 'storage'
  | 'access-init'
  | 'review';

const requiredResourceSelectValue = (message: string) =>
  yup
    .object({ id: yup.string(), name: yup.string() })
    .test('resource-select-required', message, (value) => Boolean(value?.id?.trim()));

/** `locked: true` requires a value; `locked: false` allows empty (omitted at submit). */
const catalogLockedStringField = (valueSchema: yup.StringSchema, requiredMessage: string) =>
  yup.object({
    locked: yup.boolean(),
    value: valueSchema.when('locked', {
      is: true,
      then: (schema) => schema.required(requiredMessage),
    }),
  });

const catalogLockedResourceField = (requiredMessage: string) =>
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

const diskSizeNumberTest = (t: TFunction) =>
  ({
    name: 'disk-size-number',
    message: t('Disk size must be a number'),
    test: (value?: string) => {
      if (!value?.trim()) {
        return true;
      }
      const size = Number(value.trim());
      return Number.isInteger(size) && size >= 1 && size <= 16384;
    },
  }) as const;

const diskRowSchema = (t: TFunction) =>
  yup.object({
    sizeGib: yup.string().test(diskSizeNumberTest(t)),
    storageTier: requiredResourceSelectValue(t('Storage tier is required')),
  });

export const buildComputeCatalogStepSchema = (
  stepId: ComputeCatalogWizardStepId,
  t: TFunction,
  role: UserRole,
): yup.AnyObjectSchema | undefined => {
  const isAdmin = role === 'admin';

  switch (stepId) {
    case 'details':
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
        diskImage: catalogLockedResourceField(t('Disk image is required when locked')),
        instanceType: catalogLockedResourceField(t('Instance type is required when locked')),
      });
    case 'storage':
      return yup.object({
        bootDiskSizeGib: catalogLockedStringField(
          yup.string().test(diskSizeNumberTest(t)),
          t('Boot disk size is required when locked'),
        ),
        bootDiskStorageTier: catalogLockedResourceField(t('Storage tier is required when locked')),
        additionalDisks: yup.object({
          locked: yup.boolean(),
          value: yup.array(diskRowSchema(t)).when('locked', {
            is: true,
            then: (schema) =>
              schema.min(1, t('At least one disk is required when this field is locked')),
          }),
        }),
      });
    case 'access-init':
      return yup
        .object({
          userData: yup.object({ locked: yup.boolean(), value: yup.string() }),
          userDataSource: yup.object({ value: yup.string() }),
          userDataSecret: yup.object({ value: yup.string() }),
        })
        .test(
          'user-data-required-when-locked',
          t('User data is required when locked'),
          function userDataRequiredWhenLocked(values) {
            if (!values?.userData?.locked) {
              return true;
            }
            const source = values.userDataSource?.value === 'secret' ? 'secret' : 'inline';
            const value =
              source === 'secret' ? values.userDataSecret?.value : values.userData?.value;
            if (typeof value === 'string' && value.trim()) {
              return true;
            }
            return this.createError({
              path: source === 'secret' ? 'userDataSecret.value' : 'userData.value',
              message: t('User data is required when locked'),
            });
          },
        );
    case 'review':
      return undefined;
    default:
      return undefined;
  }
};

export const computeCatalogStepHasErrors = (
  stepId: string,
  errors: FormikErrors<ComputeCatalogItemValues>,
): boolean => {
  switch (stepId) {
    case 'details':
      return Boolean(errors.scope || errors.metadata || errors.template);
    case 'configuration':
      return Boolean(errors.diskImage || errors.instanceType);
    case 'storage':
      return Boolean(
        errors.bootDiskSizeGib || errors.bootDiskStorageTier || errors.additionalDisks,
      );
    case 'access-init':
      return Boolean(errors.userData || errors.userDataSecret);
    default:
      return false;
  }
};
