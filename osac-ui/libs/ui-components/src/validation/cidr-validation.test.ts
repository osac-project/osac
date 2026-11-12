import { describe, expect, it } from 'vitest';

import {
  buildCidrSchema,
  cidrsOverlap,
  hasSubnetOverlap,
  isSubnetWithinVN,
  isValidCidr,
} from './cidr-validation';
import { initTestI18n, tIdentity } from '../test-utils/i18n';

describe('isValidCidr (ipv4)', () => {
  it.each([
    ['', true],
    ['   ', true],
    ['10.128.0.0/14', true],
    ['172.30.0.0/16', true],
    ['not-a-cidr', false],
    ['10.0.0.0', false],
    ['10.0.0.0/', false],
    ['/24', false],
    ['10.0.0.0/33', false],
    ['256.0.0.0/8', false],
    ['fd01::/48', false],
    ['fd02::/112', false],
  ])('validates %j as %s', (value, expected) => {
    expect(isValidCidr(value, 'ipv4')).toBe(expected);
  });
});

describe('buildCidrSchema (ipv4)', () => {
  const schema = buildCidrSchema(tIdentity, 'ipv4');

  it('validates valid IPv4 CIDR', async () => {
    await expect(schema.validate('10.128.0.0/14')).resolves.toBe('10.128.0.0/14');
  });

  it.each(['0.0.0.0/0', '192.168.1.0/24', '192.168.1.5/32'])(
    'accepts the network address %s',
    async (cidr) => {
      await expect(schema.validate(cidr)).resolves.toBe(cidr);
    },
  );

  it.each([
    ['2.3.4.5/23', '2.3.4.0/23'],
    ['192.168.1.5/24', '192.168.1.0/24'],
  ])('suggests the network address for %s', async (cidr, expected) => {
    const i18n = await initTestI18n();
    await expect(buildCidrSchema(i18n.t, 'ipv4').validate(cidr)).rejects.toThrow(
      `Use ${expected} instead. CIDRs must start at the network address.`,
    );
  });

  it('rejects IPv6 CIDR', async () => {
    await expect(schema.validate('fd01::/48')).rejects.toThrow('Invalid IPv4 CIDR notation');
  });

  it('allows empty string for optional fields', async () => {
    await expect(schema.validate('')).resolves.toBe('');
  });

  it('allows whitespace-only input for optional fields without throwing', async () => {
    await expect(schema.validate('   ')).resolves.toBe('   ');
  });

  it('rejects invalid CIDR without prefix', async () => {
    await expect(schema.validate('192.168.1.0')).rejects.toThrow();
  });

  it('rejects invalid CIDR with invalid IP', async () => {
    await expect(schema.validate('999.999.999.999/24')).rejects.toThrow();
  });

  it('rejects invalid CIDR with invalid prefix', async () => {
    await expect(schema.validate('192.168.1.0/99')).rejects.toThrow();
  });

  it('rejects non-CIDR string', async () => {
    await expect(schema.validate('not-a-cidr')).rejects.toThrow();
  });

  it('rejects empty string when required', async () => {
    await expect(schema.required('CIDR is required').validate('')).rejects.toThrow();
  });
});

describe('isValidCidr (ipv6)', () => {
  it.each([
    ['', true],
    ['   ', true],
    ['2001:db8::/32', true],
    ['2001:db8:0000:0000::/64', true],
    ['fd01::/48', true],
    ['not-a-cidr', false],
    ['10.128.0.0/14', false],
    ['2001:db8::/199', false],
  ])('validates %j as %s', (value, expected) => {
    expect(isValidCidr(value, 'ipv6')).toBe(expected);
  });
});

describe('buildCidrSchema (ipv6)', () => {
  const schema = buildCidrSchema(tIdentity, 'ipv6');

  it('validates valid IPv6 CIDR', async () => {
    await expect(schema.validate('2001:db8::/32')).resolves.toBe('2001:db8::/32');
  });

  it('rejects IPv4 CIDR', async () => {
    await expect(schema.validate('10.128.0.0/14')).rejects.toThrow('Invalid IPv6 CIDR notation');
  });

  it('allows empty string for optional fields', async () => {
    await expect(schema.validate('')).resolves.toBe('');
  });

  it('rejects invalid IPv6 CIDR with invalid prefix', async () => {
    await expect(schema.validate('2001:db8::/199')).rejects.toThrow();
  });

  it('rejects non-CIDR string', async () => {
    await expect(schema.validate('not-a-cidr')).rejects.toThrow();
  });
});

describe('cidrsOverlap', () => {
  it('detects identical overlapping CIDRs', () => {
    expect(cidrsOverlap('10.128.0.0/14', '10.128.0.0/14', 'ipv4')).toBe(true);
  });

  it('allows non-overlapping CIDRs', () => {
    expect(cidrsOverlap('10.128.0.0/14', '172.30.0.0/16', 'ipv4')).toBe(false);
  });

  it('ignores empty values', () => {
    expect(cidrsOverlap('', '172.30.0.0/16', 'ipv4')).toBe(false);
  });

  it('detects containment overlap when one CIDR is within another', () => {
    expect(cidrsOverlap('10.0.0.0/8', '10.128.0.0/14', 'ipv4')).toBe(true);
  });
});

describe('isSubnetWithinVN', () => {
  it('returns true when subnet is within parent VN', () => {
    expect(isSubnetWithinVN('192.168.1.0/26', '192.168.1.0/24')).toBe(true);
  });

  it('returns true when subnet equals parent VN', () => {
    expect(isSubnetWithinVN('192.168.1.0/24', '192.168.1.0/24')).toBe(true);
  });

  it('returns false when subnet is outside parent VN', () => {
    expect(isSubnetWithinVN('192.168.2.0/24', '192.168.1.0/24')).toBe(false);
  });

  it('returns false when subnet is larger than parent VN', () => {
    expect(isSubnetWithinVN('192.168.0.0/16', '192.168.1.0/24')).toBe(false);
  });

  it('returns true for subnet at end of parent range', () => {
    expect(isSubnetWithinVN('10.0.255.0/24', '10.0.0.0/16')).toBe(true);
  });

  it('returns false when subnet starts in range but extends beyond', () => {
    expect(isSubnetWithinVN('192.168.1.128/24', '192.168.1.0/25')).toBe(false);
  });

  it('accepts non-canonical IPv6 notation within the parent range', () => {
    expect(isSubnetWithinVN('2001:db8:0000:0000::/64', '2001:db8::/32')).toBe(true);
  });
});

describe('hasSubnetOverlap', () => {
  it('returns false when no existing subnets', () => {
    expect(hasSubnetOverlap('192.168.1.0/26', [])).toBe(false);
  });

  it('returns false when subnet does not overlap', () => {
    expect(hasSubnetOverlap('192.168.1.64/26', ['192.168.1.0/26'])).toBe(false);
  });

  it('returns true when subnet exactly matches existing', () => {
    expect(hasSubnetOverlap('192.168.1.0/26', ['192.168.1.0/26'])).toBe(true);
  });

  it('returns true when subnet contains existing subnet', () => {
    expect(hasSubnetOverlap('192.168.1.0/24', ['192.168.1.0/26'])).toBe(true);
  });

  it('returns true when existing subnet contains new subnet', () => {
    expect(hasSubnetOverlap('192.168.1.0/26', ['192.168.1.0/24'])).toBe(true);
  });

  it('returns true when subnet partially overlaps', () => {
    expect(hasSubnetOverlap('192.168.1.32/26', ['192.168.1.0/25'])).toBe(true);
  });

  it('returns false when none of multiple subnets overlap', () => {
    expect(hasSubnetOverlap('192.168.1.128/26', ['192.168.1.0/26', '192.168.1.64/26'])).toBe(false);
  });

  it('returns true when at least one of multiple subnets overlaps', () => {
    expect(hasSubnetOverlap('192.168.1.64/26', ['192.168.1.0/26', '192.168.1.64/26'])).toBe(true);
  });

  it('accepts non-canonical IPv6 notation when checking overlap', () => {
    expect(hasSubnetOverlap('2001:db8:0000:0000::/64', ['2001:db8::/64'])).toBe(true);
  });
});
