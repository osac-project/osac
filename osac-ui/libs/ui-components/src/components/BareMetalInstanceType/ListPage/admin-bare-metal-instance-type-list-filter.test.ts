import { describe, expect, it } from 'vitest';

import { buildAdminBareMetalInstanceTypeListFilter } from './admin-bare-metal-instance-type-list-filter';

describe('buildAdminBareMetalInstanceTypeListFilter', () => {
  it('returns undefined when no criteria are set', () => {
    expect(buildAdminBareMetalInstanceTypeListFilter({})).toBeUndefined();
  });

  it('returns undefined when search is blank', () => {
    expect(buildAdminBareMetalInstanceTypeListFilter({ search: '   ' })).toBeUndefined();
  });

  it('builds a name search filter', () => {
    const filter = buildAdminBareMetalInstanceTypeListFilter({ search: 'gpu' });
    expect(filter).toContain('this.metadata.name.contains("gpu")');
    expect(filter).toContain('this.id.contains("gpu")');
  });
});
