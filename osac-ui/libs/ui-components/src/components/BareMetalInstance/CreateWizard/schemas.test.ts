import { describe, expect, it } from 'vitest';
import { ValidationError } from 'yup';

import { tIdentity } from '@osac/ui-components/test-utils/i18n';

import { type BareMetalInstanceWizardValues, createEmptyBareMetalInstanceValues } from './fields';
import { buildBareMetalInstanceStepSchema } from './schemas';
import { emptyResourceSelectValue } from '../../Form/resourceSelectValue';

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

const valuesWithAuth = (sshKey: string, userData: string): BareMetalInstanceWizardValues => ({
  ...createEmptyBareMetalInstanceValues(),
  spec: {
    ...createEmptyBareMetalInstanceValues().spec,
    sshKey: { value: sshKey, locked: false },
    userData: { value: userData, locked: false },
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

  it('rejects neither authentication method', async () => {
    await expect(validateReview(valuesWithAuth('', ''))).resolves.toBe(authenticationError);
  });

  it('rejects whitespace-only authentication values', async () => {
    await expect(validateReview(valuesWithAuth(' \n ', '\t'))).resolves.toBe(authenticationError);
  });

  it('accepts a USER_DATA Secret as the authentication method', async () => {
    const values = createEmptyBareMetalInstanceValues();
    values.spec.userDataSource = { value: 'secret', locked: false };
    values.spec.userDataSecret = { value: 'user-data-secret', locked: false };
    await expect(validateReview(values)).resolves.toBeUndefined();
  });
});

const validateNetworking = async (values: BareMetalInstanceWizardValues) => {
  const schema = buildBareMetalInstanceStepSchema('networking', tIdentity);
  if (!schema) {
    throw new Error('Networking schema is required');
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

describe('Bare Metal networking validation', () => {
  it('does not require attachments when useDefaults is true', async () => {
    const values = createEmptyBareMetalInstanceValues();
    values.spec.networkAttachment.value.useDefaultNetwork = true;

    const errors = await validateNetworking(values);
    expect(errors).toEqual({});
  });

  it('requires virtual network and subnet when useDefaults is false', async () => {
    const values = createEmptyBareMetalInstanceValues();
    values.spec.networkAttachment.value.useDefaultNetwork = false;
    values.spec.networkAttachment.value.virtualNetwork = emptyResourceSelectValue();
    values.spec.networkAttachment.value.subnet = emptyResourceSelectValue();

    const errors = await validateNetworking(values);
    expect(errors).toEqual({
      spec: {
        networkAttachment: {
          value: {
            virtualNetwork: { id: 'Virtual network is required' },
            subnet: { id: 'Subnet is required' },
          },
        },
      },
    });
  });

  it('accepts valid custom networking when useDefaults is false', async () => {
    const values = createEmptyBareMetalInstanceValues();
    values.spec.networkAttachment.value.useDefaultNetwork = false;
    values.spec.networkAttachment.value.virtualNetwork = { id: 'vn-1', name: 'vn-1' };
    values.spec.networkAttachment.value.subnet = { id: 'subnet-1', name: 'subnet-1' };

    const errors = await validateNetworking(values);
    expect(errors).toEqual({});
  });
});
