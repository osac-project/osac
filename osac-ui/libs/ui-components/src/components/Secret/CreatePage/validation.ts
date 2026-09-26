import type { FormikErrors } from 'formik';
import type { TFunction } from 'i18next';
import * as Yup from 'yup';

import { SecretType } from '@osac/types';
import { resourceNameSchema } from '@osac/ui-components/validation/resource-name';

import { SECRET_FILE_MAX_BYTES, type SecretValues } from './values';

const getByteLength = (value: unknown): number | undefined => {
  if (!value || typeof value !== 'object') {
    return undefined;
  }

  const byteLength = (value as { byteLength?: unknown }).byteLength;
  return typeof byteLength === 'number' ? byteLength : undefined;
};

const getValueSchema = (t: TFunction, required: boolean) =>
  Yup.mixed<Uint8Array>()
    .strict(true)
    .test(
      'value-required',
      t('Secret value is required'),
      (value) => !required || (getByteLength(value) ?? 0) > 0,
    )
    .test(
      'value-max-size',
      t('Secret files must not exceed 1 MiB'),
      (value) => (getByteLength(value) ?? 0) <= SECRET_FILE_MAX_BYTES,
    );

const getEntrySchema = (t: TFunction, required: boolean) =>
  Yup.object({
    key: required ? Yup.string().required(t('Secret key is required')) : Yup.string(),
    value: getValueSchema(t, required),
  });

const getTypedEntrySchema = (t: TFunction, type: SecretType) =>
  Yup.object().when('type', {
    is: type,
    then: () => getEntrySchema(t, true),
    otherwise: () => getEntrySchema(t, false),
  });

export const getSecretValidationSchema = (t: TFunction, _isEdit: boolean) =>
  Yup.object({
    metadata: Yup.object({
      name: resourceNameSchema(t),
    }),
    type: Yup.mixed<SecretType>().required(t('Secret type is required')),
    kubeconfig: getTypedEntrySchema(t, SecretType.KUBECONFIG),
    pullsecret: getTypedEntrySchema(t, SecretType.PULL_SECRET),
    userData: getTypedEntrySchema(t, SecretType.USER_DATA),
    value: getTypedEntrySchema(t, SecretType.VALUE),
    opaque: Yup.array()
      .of(getEntrySchema(t, false))
      .when('type', {
        is: SecretType.OPAQUE,
        then: (schema) =>
          schema.min(1, t('At least one secret entry is required')).of(getEntrySchema(t, true)),
        otherwise: (schema) => schema.notRequired(),
      })
      .test('unique-secret-keys', t('Secret keys must be unique'), (entries, context) => {
        const parent = context.parent as { type?: SecretType };
        if (!entries || parent.type !== SecretType.OPAQUE) {
          return true;
        }

        const seenKeys = new Set<string>();
        for (const [index, entry] of entries.entries()) {
          if (!entry.key) {
            continue;
          }

          if (seenKeys.has(entry.key)) {
            return context.createError({
              path: `${context.path}[${index}].key`,
              message: t('Secret keys must be unique'),
            });
          }

          seenKeys.add(entry.key);
        }

        return true;
      }),
  });

export const secretStepHasErrors = (
  stepId: string,
  errors: FormikErrors<SecretValues>,
): boolean => {
  switch (stepId) {
    case 'general':
      return Boolean(errors.metadata?.name || errors.metadata?.project);
    case 'data':
      return Boolean(
        errors.type ||
        errors.kubeconfig ||
        errors.pullsecret ||
        errors.userData ||
        errors.value ||
        errors.opaque,
      );
    default:
      return false;
  }
};
