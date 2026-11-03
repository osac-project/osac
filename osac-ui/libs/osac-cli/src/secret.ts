import { generateKeyPairSync } from 'node:crypto';
import { mkdtemp, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import path from 'node:path';

import { expectOsacCommandSucceeded, runOsacCommand } from './cli';

const sshKeySecretSuffix = '-ssh-key';

export const tenantSshKeyName = (tenantName: string): string =>
  `${tenantName}${sshKeySecretSuffix}`;

const encodeSshField = (value: Buffer): Buffer => {
  const length = Buffer.alloc(4);
  length.writeUInt32BE(value.length);
  return Buffer.concat([length, value]);
};

const generateSshPublicKey = (): string => {
  const { publicKey } = generateKeyPairSync('ed25519');
  const encodedPublicKey = Buffer.from(publicKey.export({ format: 'der', type: 'spki' }));
  const rawPublicKey = encodedPublicKey.subarray(encodedPublicKey.length - 32);
  const keyType = Buffer.from('ssh-ed25519');
  const publicKeyBlob = Buffer.concat([encodeSshField(keyType), encodeSshField(rawPublicKey)]);

  return `ssh-ed25519 ${publicKeyBlob.toString('base64')} osac-vm-flow-e2e`;
};

export const createTenantSshKey = async (tenantName: string): Promise<string> => {
  const secretName = tenantSshKeyName(tenantName);
  const temporaryDirectory = await mkdtemp(path.join(tmpdir(), 'osac-tenant-ssh-'));
  const publicKeyFile = path.join(temporaryDirectory, 'id_ed25519.pub');

  try {
    await writeFile(publicKeyFile, `${generateSshPublicKey()}\n`, {
      encoding: 'utf8',
      mode: 0o600,
    });
    const result = await runOsacCommand([
      '--tenant',
      tenantName,
      'create',
      'secret',
      '--name',
      secretName,
      '--type',
      'ssh-public-key',
      '--from-file',
      `public_key=${publicKeyFile}`,
    ]);
    expectOsacCommandSucceeded(result, `Create SSH public-key Secret '${secretName}'`);
    return secretName;
  } finally {
    await rm(temporaryDirectory, { force: true, recursive: true });
  }
};

export const deleteTenantSshKey = async (tenantName: string): Promise<void> => {
  const secretName = tenantSshKeyName(tenantName);
  const existingResult = await runOsacCommand([
    '--tenant',
    tenantName,
    'get',
    'secret',
    secretName,
    '--output',
    'json',
  ]);
  expectOsacCommandSucceeded(existingResult, `Get SSH public-key Secret '${secretName}'`);

  const existingSecrets: unknown = JSON.parse(existingResult.stdout);
  if (Array.isArray(existingSecrets) && existingSecrets.length === 0) {
    return;
  }

  const result = await runOsacCommand(['--tenant', tenantName, 'delete', 'secret', secretName]);
  expectOsacCommandSucceeded(result, `Delete SSH public-key Secret '${secretName}'`);
};
