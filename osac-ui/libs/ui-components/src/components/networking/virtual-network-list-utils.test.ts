import { describe, expect, it } from 'vitest';

import { VirtualNetworkState } from '@osac/types';

import { buildVirtualNetworkListFilter } from './virtual-network-list-utils';

describe('buildVirtualNetworkListFilter', () => {
  it('returns undefined when no filters are applied', () => {
    expect(buildVirtualNetworkListFilter([], '')).toBeUndefined();
  });

  it('builds a state filter', () => {
    expect(buildVirtualNetworkListFilter([VirtualNetworkState.READY], '')).toBe(
      'this.status.state in [2]',
    );
  });

  it('builds a name search filter', () => {
    expect(buildVirtualNetworkListFilter([], 'prod')).toBe('this.metadata.name.contains("prod")');
  });

  it('combines state and name filters', () => {
    expect(buildVirtualNetworkListFilter([VirtualNetworkState.PENDING], 'vn')).toBe(
      'this.status.state in [1] && this.metadata.name.contains("vn")',
    );
  });
});
