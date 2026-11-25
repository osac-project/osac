import { describe, expect, it } from 'vitest';

import { subnetCidrExample, subnetExamplePrefix } from './subnet-cidr-example';

describe('subnetExamplePrefix', () => {
  it.each([
    [8, 24],
    [16, 24],
    [21, 24],
    [22, 24],
    [23, 25],
    [24, 26],
    [25, 26],
    [26, 26],
    [27, 27],
    [30, 30],
  ])('derives a /%i network prefix as /%i', (networkPrefix, expected) => {
    expect(subnetExamplePrefix(networkPrefix)).toBe(expected);
  });
});

describe('subnetCidrExample', () => {
  it('returns the first block two bits longer than the network', () => {
    expect(subnetCidrExample('10.20.0.0/16')).toBe('10.20.0.0/24');
  });

  it('skips blocks that a subnet already uses', () => {
    expect(subnetCidrExample('10.20.0.0/16', ['10.20.0.0/24', '10.20.1.0/24'])).toBe(
      '10.20.2.0/24',
    );
  });

  it('continues after an existing subnet larger than the example block', () => {
    expect(subnetCidrExample('10.20.0.0/16', ['10.20.0.0/20'])).toBe('10.20.16.0/24');
  });

  it('uses a /25 inside a /23 network', () => {
    expect(subnetCidrExample('10.0.0.0/23')).toBe('10.0.0.0/25');
  });

  it('starts at the first free block when an earlier block is taken', () => {
    expect(subnetCidrExample('10.0.0.0/24', ['10.0.0.0/26'])).toBe('10.0.0.64/26');
  });

  it('uses the network CIDR when it is /26 or longer', () => {
    expect(subnetCidrExample('10.0.0.0/26')).toBe('10.0.0.0/26');
    expect(subnetCidrExample('10.0.0.16/28')).toBe('10.0.0.16/28');
  });

  it('returns undefined when every block of the example size is taken', () => {
    expect(
      subnetCidrExample('10.0.0.0/24', [
        '10.0.0.0/26',
        '10.0.0.64/26',
        '10.0.0.128/26',
        '10.0.0.192/26',
      ]),
    ).toBeUndefined();
  });

  it('ignores existing CIDRs that are invalid or from another family', () => {
    expect(subnetCidrExample('10.0.0.0/24', ['not-a-cidr', 'fd00::/64', '10.0.0.0/26'])).toBe(
      '10.0.0.64/26',
    );
  });

  it('returns undefined without an IPv4 network CIDR', () => {
    expect(subnetCidrExample('')).toBeUndefined();
    expect(subnetCidrExample('nope')).toBeUndefined();
    expect(subnetCidrExample('fd00::/64')).toBeUndefined();
  });
});
