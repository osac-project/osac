import { describe, expect, it } from 'vitest';

import { getExternalIpPoolSchema } from './validation';

const t = ((key: string) => key) as never;

const validMetadata = { name: 'pool', tenant: { id: 'tenant-1', name: 'tenant' } };

describe('getExternalIpPoolSchema', () => {
  it('accepts one canonical IPv4 CIDR', async () => {
    await expect(
      getExternalIpPoolSchema(t).validate({
        metadata: validMetadata,
        cidrs: ['192.168.1.0/24'],
      }),
    ).resolves.toBeDefined();
  });

  it('rejects IPv6 CIDRs', async () => {
    const schema = getExternalIpPoolSchema(t);

    await expect(
      schema.validate({
        metadata: validMetadata,
        cidrs: ['2001:db8::/64'],
      }),
    ).rejects.toThrow('Invalid IPv4 CIDR notation');
  });
});
