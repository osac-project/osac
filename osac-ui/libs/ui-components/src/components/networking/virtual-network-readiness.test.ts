import { describe, expect, it } from 'vitest';

import { VirtualNetworkState } from '@osac/types';

import { virtualNetworkOptionLabel } from './virtual-network-option-label';
import { isReadyVirtualNetwork, isTenantDefaultVirtualNetwork } from './virtual-network-readiness';

describe('isReadyVirtualNetwork', () => {
  it('accepts only the READY state', () => {
    expect(isReadyVirtualNetwork({ status: { state: VirtualNetworkState.READY } })).toBe(true);
    expect(isReadyVirtualNetwork({ status: { state: VirtualNetworkState.PENDING } })).toBe(false);
    expect(isReadyVirtualNetwork({ status: { state: VirtualNetworkState.FAILED } })).toBe(false);
    expect(isReadyVirtualNetwork({})).toBe(false);
    expect(isReadyVirtualNetwork(undefined)).toBe(false);
  });
});

describe('isTenantDefaultVirtualNetwork', () => {
  it('requires the default label in the tenant default project', () => {
    expect(
      isTenantDefaultVirtualNetwork({
        metadata: { project: '', labels: { 'osac.openshift.io/default': 'true' } },
      }),
    ).toBe(true);
    expect(
      isTenantDefaultVirtualNetwork({
        metadata: { project: 'web-team', labels: { 'osac.openshift.io/default': 'true' } },
      }),
    ).toBe(false);
    expect(isTenantDefaultVirtualNetwork({ metadata: { project: '', labels: {} } })).toBe(false);
    expect(isTenantDefaultVirtualNetwork(undefined)).toBe(false);
  });
});

describe('virtualNetworkOptionLabel', () => {
  it('shows the name and the IPv4 CIDR', () => {
    expect(
      virtualNetworkOptionLabel({
        id: 'vn-1',
        metadata: { name: 'app-net' },
        spec: { ipv4Cidr: '10.20.0.0/16' },
      }),
    ).toBe('app-net (10.20.0.0/16)');
  });

  it('falls back to the id and omits a missing CIDR', () => {
    expect(virtualNetworkOptionLabel({ id: 'vn-1', metadata: {}, spec: {} })).toBe('vn-1');
    expect(virtualNetworkOptionLabel({ id: 'vn-1', metadata: { name: 'app-net' }, spec: {} })).toBe(
      'app-net',
    );
  });
});
