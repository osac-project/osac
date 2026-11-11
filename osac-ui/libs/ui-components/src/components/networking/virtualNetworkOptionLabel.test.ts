import { describe, expect, it } from 'vitest';

import { virtualNetworkOptionLabel } from './virtualNetworkReadiness';

describe('virtualNetworkOptionLabel', () => {
  const vn = {
    id: 'vn-1',
    metadata: { name: 'default', tenant: 'tenant-a' },
    spec: { ipv4Cidr: '10.100.0.0/16' },
  };

  it('shows the name and CIDR for tenant users', () => {
    expect(virtualNetworkOptionLabel(vn)).toBe('default (10.100.0.0/16)');
  });

  it('adds the tenant for a provider admin', () => {
    expect(virtualNetworkOptionLabel(vn, (id) => `name-of-${id}`)).toBe(
      'default (10.100.0.0/16, name-of-tenant-a)',
    );
  });

  it('falls back to the ID and a dash', () => {
    expect(virtualNetworkOptionLabel({ id: 'vn-2' })).toBe('vn-2 (—)');
  });
});
