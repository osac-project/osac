import { describe, expect, it } from 'vitest';

import { subnetCidrExample } from './subnetCidrExample';

describe('subnetCidrExample', () => {
  it('suggests a /24 inside a large network', () => {
    expect(subnetCidrExample('10.200.0.0/16')).toBe('10.200.0.0/24');
  });

  it('never suggests a prefix longer than /26, which every derived address plan accepts', () => {
    expect(subnetCidrExample('192.168.101.0/24')).toBe('192.168.101.0/26');
    expect(subnetCidrExample('192.168.100.0/25')).toBe('192.168.100.0/26');
    // A /26 network allows no smaller subnet under a /26 limit, so the example is the network.
    expect(subnetCidrExample('192.168.102.0/26')).toBe('192.168.102.0/26');
    expect(subnetCidrExample('192.168.102.0/28')).toBe('192.168.102.0/28');
  });

  it('suggests the first block no existing subnet uses', () => {
    expect(subnetCidrExample('192.168.101.0/24', ['192.168.101.0/26'])).toBe('192.168.101.64/26');
    expect(subnetCidrExample('10.0.0.0/16', ['10.0.0.0/24', '10.0.1.128/25'])).toBe('10.0.2.0/24');
    // Another network's subnets do not matter, only overlaps inside this one.
    expect(subnetCidrExample('10.0.0.0/16', ['192.168.0.0/24'])).toBe('10.0.0.0/24');
  });

  it('gives no example when the network is full', () => {
    expect(subnetCidrExample('192.168.102.0/26', ['192.168.102.0/26'])).toBeUndefined();
    expect(subnetCidrExample('192.168.101.0/24', ['192.168.101.0/24'])).toBeUndefined();
  });

  it('starts at the network address', () => {
    expect(subnetCidrExample('172.16.5.77/20')).toBe('172.16.0.0/24');
  });

  it('gives no example when there is none', () => {
    expect(subnetCidrExample(undefined)).toBeUndefined();
    expect(subnetCidrExample('not-a-cidr')).toBeUndefined();
    expect(subnetCidrExample('10.0.0.0/29')).toBeUndefined();
  });
});
