import type { FormikHelpers } from 'formik';
import { describe, expect, it } from 'vitest';
import { ValidationError } from 'yup';

import type { BareMetalInstanceCatalogItem } from '@osac/types';
import { tIdentity } from '@osac/ui-components/test-utils/i18n';

import {
  type BareMetalInstanceWizardValues,
  applyBmCatalogDefaults,
  createEmptyBareMetalInstanceValues,
} from './fields';
import { buildBareMetalInstanceStepSchema } from './schemas';

const authenticationError =
  'Provide either an SSH public key or user data containing access credentials.';

const validateReview = async (values: BareMetalInstanceWizardValues) => {
  const schema = buildBareMetalInstanceStepSchema('review', tIdentity);
  if (!schema) {
    throw new Error('Review schema is required for Bare Metal');
  }

  try {
    await schema.validate(values);
    return undefined;
  } catch (error) {
    if (!(error instanceof ValidationError)) {
      throw error;
    }
    return error.message;
  }
};

const validateStep = async (
  stepId: Parameters<typeof buildBareMetalInstanceStepSchema>[0],
  values: BareMetalInstanceWizardValues,
) => {
  const schema = buildBareMetalInstanceStepSchema(stepId, tIdentity);
  if (!schema) {
    return {};
  }

  try {
    await schema.validate(values, { abortEarly: false });
    return {};
  } catch (error) {
    if (!(error instanceof ValidationError)) {
      throw error;
    }
    const errors: Record<string, unknown> = {};
    for (const inner of error.inner.length > 0 ? error.inner : [error]) {
      if (!inner.path) {
        continue;
      }
      const parts = inner.path.split('.');
      let current: Record<string, unknown> = errors;
      for (let index = 0; index < parts.length - 1; index += 1) {
        const key = parts[index];
        if (!current[key] || typeof current[key] !== 'object') {
          current[key] = {};
        }
        current = current[key] as Record<string, unknown>;
      }
      current[parts[parts.length - 1]] = inner.message;
    }
    return errors;
  }
};

const valuesWithAuth = (sshKey: string, userData: string): BareMetalInstanceWizardValues => ({
  ...createEmptyBareMetalInstanceValues(),
  spec: {
    ...createEmptyBareMetalInstanceValues().spec,
    sshKey,
    userData,
  },
});

describe('Bare Metal review validation', () => {
  it.each([
    ['SSH key only', 'ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIexample', ''],
    ['user data only', '', '#cloud-config\nusers: []'],
    [
      'both authentication methods',
      'ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIexample',
      '#cloud-config',
    ],
  ])('accepts %s', async (_label, sshKey, userData) => {
    await expect(validateReview(valuesWithAuth(sshKey, userData))).resolves.toBeUndefined();
  });

  it('accepts a Secret reference as the user-data authentication source', async () => {
    const values = createEmptyBareMetalInstanceValues();
    values.spec.userDataSource = 'secret';
    values.spec.userDataSecret = { name: 'user-data-secret' };

    await expect(validateReview(values)).resolves.toBeUndefined();
  });

  it('validates inline user data with the shared 64 KB limit', async () => {
    const values = createEmptyBareMetalInstanceValues();
    values.spec.diskImage = { id: 'disk-image', name: '' };
    values.spec.userData = 'a'.repeat(65537);

    await expect(validateStep('configuration', values)).resolves.toEqual({
      spec: { userData: 'User data must not exceed 64 KB.' },
    });

    values.spec.userData = 'a'.repeat(65536);
    await expect(validateStep('configuration', values)).resolves.toEqual({});

    values.spec.userDataSource = 'secret';
    values.spec.userDataSecret = { name: 'user-data-secret' };
    values.spec.userData = 'a'.repeat(65537);
    await expect(validateStep('configuration', values)).resolves.toEqual({});
  });

  it('does not use inactive inline user data for Secret authentication', async () => {
    const values = createEmptyBareMetalInstanceValues();
    values.spec.userDataSource = 'secret';
    values.spec.userData = 'stale inline data';
    values.spec.userDataSecret = { name: '' };

    await expect(validateReview(values)).resolves.toBe(authenticationError);
  });

  it('rejects neither authentication method', async () => {
    await expect(validateReview(valuesWithAuth('', ''))).resolves.toBe(authenticationError);
  });

  it('rejects whitespace-only authentication values', async () => {
    await expect(validateReview(valuesWithAuth(' \n ', '\t'))).resolves.toBe(authenticationError);
  });

  it.each([
    ['SSH key', 'ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIcatalog', undefined],
    ['user data', undefined, '#cloud-config\nusers: []'],
  ])('accepts a catalog-provided %s default', async (_label, sshKey, userData) => {
    const values = createEmptyBareMetalInstanceValues();
    const catalogItem = {
      fieldDefinitions: [
        ...(sshKey !== undefined
          ? [{ path: 'ssh_public_key', default: sshKey, editable: true }]
          : []),
        ...(userData !== undefined
          ? [{ path: 'user_data', default: userData, editable: true }]
          : []),
      ],
    } as unknown as BareMetalInstanceCatalogItem;
    const appliedValues = { ...values, spec: { ...values.spec } };
    const helpers = {
      setFieldValue: (path: string, value: unknown) => {
        if (path === 'spec.sshKey') {
          appliedValues.spec.sshKey = value as string;
        }
        if (path === 'spec.userData') {
          appliedValues.spec.userData = value as string;
        }
      },
    };

    applyBmCatalogDefaults(
      catalogItem,
      helpers as unknown as FormikHelpers<BareMetalInstanceWizardValues>,
      tIdentity,
    );

    await expect(validateReview(appliedValues)).resolves.toBeUndefined();
  });
});
