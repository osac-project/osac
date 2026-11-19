import { describe, expect, it } from 'vitest';

import { InstanceTypeState } from '@osac/types/private';

import { buildAdminInstanceTypeListFilter } from './admin-instance-type-list-filter';

describe('buildAdminInstanceTypeListFilter', () => {
  it('returns undefined when no criteria are set', () => {
    expect(buildAdminInstanceTypeListFilter({})).toBeUndefined();
  });

  it('builds a name search filter', () => {
    const filter = buildAdminInstanceTypeListFilter({ search: 'standard' });
    expect(filter).toContain('this.metadata.name.contains("standard")');
    expect(filter).toContain('this.id.contains("standard")');
  });

  it('builds a lifecycle state filter', () => {
    const filter = buildAdminInstanceTypeListFilter({ lifecycles: ['deprecated'] });
    expect(filter).toBe(`this.spec.state in [${InstanceTypeState.DEPRECATED}]`);
  });

  it('builds a multi-select lifecycle filter', () => {
    const filter = buildAdminInstanceTypeListFilter({ lifecycles: ['active', 'obsolete'] });
    expect(filter).toBe(
      `this.spec.state in [${InstanceTypeState.ACTIVE}, ${InstanceTypeState.OBSOLETE}]`,
    );
  });

  it('combines search and lifecycle filters', () => {
    const filter = buildAdminInstanceTypeListFilter({ search: 'gpu', lifecycles: ['active'] });
    expect(filter).toContain(`this.spec.state in [${InstanceTypeState.ACTIVE}]`);
    expect(filter).toContain('this.metadata.name.contains("gpu")');
    expect(filter).toContain(' && ');
  });
});
