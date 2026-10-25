import type { FormikErrors } from 'formik';
import type { TFunction } from 'i18next';
import * as Yup from 'yup';

import { SecretType } from '@osac/types';
import { resourceNameSchema } from '@osac/ui-components/validation/resource-name';

import {
  SECRET_FILE_MAX_BYTES,
  type SecretDataEntry,
  type SecretValues,
  decodeSecretValue,
} from './values';
import { isValidSshPublicKey } from '../../ProvisioningWizard/credentialValidation';

const getByteLength = (value: unknown): number | undefined => {
  if (!value || typeof value !== 'object') {
    return undefined;
  }

  const byteLength = (value as { byteLength?: unknown }).byteLength;
  return typeof byteLength === 'number' ? byteLength : undefined;
};

const isOpaqueType = (type: SecretType | undefined): boolean =>
  type === SecretType.OPAQUE || type === SecretType.UNSPECIFIED;

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
    uid: Yup.string(),
    key: required ? Yup.string().required(t('Secret key is required')) : Yup.string(),
    value: getValueSchema(t, required),
  });

export const getSecretValidationSchema = (t: TFunction, _isEdit: boolean) =>
  Yup.object({
    metadata: Yup.object({
      name: resourceNameSchema(t),
    }),
    type: Yup.mixed<SecretType>().required(t('Secret type is required')),
    data: Yup.array()
      .when('type', {
        is: (type: SecretType) => isOpaqueType(type),
        then: (schema) =>
          schema.min(1, t('At least one secret entry is required')).of(getEntrySchema(t, true)),
        otherwise: (schema) =>
          schema.min(1).of(
            Yup.object({
              uid: Yup.string(),
              key: Yup.string(),
              value: getValueSchema(t, false),
            }),
          ),
      })
      .test(
        'unique-secret-keys',
        t('Secret keys must be unique'),
        (entries: SecretDataEntry[] | undefined, context) => {
          if (!entries) {
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
        },
      )
      .test(
        'secret-data-entries',
        function validateDataEntries(entries: SecretDataEntry[] | undefined) {
          const type = (this.parent as { type?: SecretType }).type;
          if (!entries || isOpaqueType(type)) {
            return true;
          }

          for (const [index, entry] of entries.entries()) {
            const requireKey = index > 0;
            const requireValue = true;

            if (requireKey && !entry.key) {
              return this.createError({
                path: `${this.path}[${index}].key`,
                message: t('Secret key is required'),
              });
            }

            const byteLength = getByteLength(entry.value) ?? 0;
            if (requireValue && byteLength === 0) {
              return this.createError({
                path: `${this.path}[${index}].value`,
                message: t('Secret value is required'),
              });
            }
            if (byteLength > SECRET_FILE_MAX_BYTES) {
              return this.createError({
                path: `${this.path}[${index}].value`,
                message: t('Secret files must not exceed 1 MiB'),
              });
            }

            if (type === SecretType.SSH_PUBLIC_KEY && index === 0 && entry.value) {
              const decoded = decodeSecretValue(entry.value);
              if (decoded === undefined || decoded.trim() === '') {
                return this.createError({
                  path: `${this.path}[${index}].value`,
                  message: t(
                    'Enter a valid OpenSSH public key (ssh-rsa, ssh-ed25519, or ecdsa-sha2-nistp*).',
                  ),
                });
              }
              if (!isValidSshPublicKey(decoded)) {
                return this.createError({
                  path: `${this.path}[${index}].value`,
                  message: t(
                    'Enter a valid OpenSSH public key (ssh-rsa, ssh-ed25519, or ecdsa-sha2-nistp*).',
                  ),
                });
              }
            }
          }

          return true;
        },
      ),
  });

export const secretStepHasErrors = (
  stepId: string,
  errors: FormikErrors<SecretValues>,
): boolean => {
  switch (stepId) {
    case 'general':
      return Boolean(errors.metadata?.name || errors.metadata?.project);
    case 'data':
      return Boolean(errors.type || errors.data);
    default:
      return false;
  }
};
