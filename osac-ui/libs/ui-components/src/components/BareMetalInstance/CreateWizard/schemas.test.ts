import { describe, expect, it } from 'vitest';
import { ValidationError } from 'yup';

import { tIdentity } from '@osac/ui-components/test-utils/i18n';

import { type BareMetalInstanceWizardValues, createEmptyBareMetalInstanceValues } from './fields';
import { buildBareMetalInstanceStepSchema } from './schemas';
import { emptyResourceSelectValue } from '../../Form/resourceSelectValue';

const authenticationError = 'Provide either an SSH public key or user data.';
const authenticationErrors = [
  { path: 'spec.sshKey.value', message: authenticationError },
  { path: 'spec.userData.value.source', message: authenticationError },
];

const validateAccess = async (values: BareMetalInstanceWizardValues) => {
  const schema = buildBareMetalInstanceStepSchema('access', tIdentity);
  if (!schema) {
    throw new Error('Access schema is required for Bare Metal');
  }

  try {
    await schema.validate(values, { abortEarly: false });
    return undefined;
  } catch (error) {
    if (!(error instanceof ValidationError)) {
      throw error;
    }
    return (error.inner.length ? error.inner : [error])
      .map(({ path, message }) => ({ path, message }))
      .sort((left, right) => (left.path ?? '').localeCompare(right.path ?? ''));
  }
};

const valuesWithAuth = (sshKey: string, userData: string): BareMetalInstanceWizardValues => {
  const values = createEmptyBareMetalInstanceValues();
  values.spec.sshKey.value = sshKey;
  values.spec.userData.value.inline = userData;
  values.spec.diskImage.value.id = 'disk-image-id';
  return values;
};

describe('Bare Metal access authentication validation', () => {
  it.each([
    ['SSH key only', 'ssh-public-key-secret', ''],
    ['user data only', '', '#cloud-config\nusers: []'],
    ['both authentication methods', 'ssh-public-key-secret', '#cloud-config'],
  ])('accepts %s', async (_label, sshKey, userData) => {
    await expect(validateAccess(valuesWithAuth(sshKey, userData))).resolves.toBeUndefined();
  });

  it('rejects neither authentication method', async () => {
    await expect(validateAccess(valuesWithAuth('', ''))).resolves.toEqual(authenticationErrors);
  });

  it.each(['spec.sshKey.value', 'spec.userData.value.source'])(
    'validates %s independently',
    async (path) => {
      const schema = buildBareMetalInstanceStepSchema('access', tIdentity);
      if (!schema) {
        throw new Error('Access schema is required for Bare Metal');
      }

      await expect(schema.validateAt(path, valuesWithAuth('', ''))).rejects.toMatchObject({
        path,
        message: authenticationError,
      });
    },
  );

  it('ignores user data in the inactive source', async () => {
    const values = valuesWithAuth('', 'stale-inline-data');
    values.spec.userData.value.source = 'secret';

    await expect(validateAccess(values)).resolves.toEqual(authenticationErrors);
  });
});

describe('Bare Metal configuration validation', () => {
  it('allows Next without authentication when a disk image is selected', async () => {
    const schema = buildBareMetalInstanceStepSchema('configuration', tIdentity);
    if (!schema) {
      throw new Error('Configuration schema is required for Bare Metal');
    }

    await expect(schema.validate(valuesWithAuth('', ''))).resolves.toBeDefined();
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
