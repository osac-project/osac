import { describe, expect, it } from 'vitest';

import {
  SUBNETS_LIST_PATH,
  buildSubnetListFilter,
  subnetReturnPath,
  subnetsCreatePath,
  virtualNetworkDetailsPath,
} from './subnet-list-utils';
import { cel } from '../../api/cel';

describe('subnetsCreatePath', () => {
  it('opens the create page with no network', () => {
    expect(subnetsCreatePath()).toBe('/networking/subnets/create');
    expect(subnetsCreatePath('')).toBe('/networking/subnets/create');
  });

  it('pre-fills the network through the shared URL parameter', () => {
    expect(subnetsCreatePath('vn-1')).toBe('/networking/subnets/create?virtualNetwork=vn-1');
    expect(subnetsCreatePath('vn/1')).toBe('/networking/subnets/create?virtualNetwork=vn%2F1');
  });
});

describe('subnetReturnPath', () => {
  it('returns to the network the create started from, or to the list', () => {
    expect(subnetReturnPath()).toBe(SUBNETS_LIST_PATH);
    expect(subnetReturnPath('vn-1')).toBe(virtualNetworkDetailsPath('vn-1'));
  });
});

describe('buildSubnetListFilter', () => {
  it('emits no filter without any criteria', () => {
    expect(buildSubnetListFilter(undefined, '')).toBeUndefined();
  });

  it('filters by name', () => {
    expect(buildSubnetListFilter(undefined, 'web')).toBe('this.metadata.name.contains("web")');
  });

  it('filters by virtual network id', () => {
    expect(buildSubnetListFilter(undefined, '', 'vn-1')).toBe(
      'this.spec.virtual_network.id == "vn-1"',
    );
  });

  it('combines the project, name and network criteria', () => {
    const projectFilter = cel<{ metadata?: { project: string } }>((builder) =>
      builder.field('metadata.project').isIn(['web-team']),
    ) as Parameters<typeof buildSubnetListFilter>[0];

    expect(buildSubnetListFilter(projectFilter, 'web', 'vn-1')).toBe(
      'this.metadata.project in ["web-team"] && this.metadata.name.contains("web") && this.spec.virtual_network.id == "vn-1"',
    );
  });

  it('escapes quotes in the network id', () => {
    expect(buildSubnetListFilter(undefined, '', 'vn"1')).toBe(
      'this.spec.virtual_network.id == "vn\\"1"',
    );
  });
});
