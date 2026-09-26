import { describe, expect, it } from 'vitest';

import {
  type CatalogListFilterableItem,
  buildCatalogListFilter,
  catalogItemMatchesListFilter,
} from './catalog-item-list-filter';

describe('buildCatalogListFilter', () => {
  it.each([
    ['no criteria', {}, undefined],
    [
      'search only',
      { search: 'fedora' },
      '(this.metadata.name.contains("fedora") || this.title.contains("fedora") || this.description.contains("fedora"))',
    ],
    [
      'search escapes quotes and backslashes',
      { search: 'a"b\\c' },
      '(this.metadata.name.contains("a\\"b\\\\c") || this.title.contains("a\\"b\\\\c") || this.description.contains("a\\"b\\\\c"))',
    ],
    ['published only', { published: 'published' as const }, 'this.published == true'],
    ['unpublished only', { published: 'unpublished' as const }, 'this.published == false'],
    [
      'tenant only',
      { tenant: 'acme' },
      '(this.metadata.tenant == "shared" || this.metadata.tenant == "acme")',
    ],
    [
      'multiple dimensions combine with &&',
      { search: 'rhel', published: 'published' as const, tenant: 'acme' },
      '(this.metadata.name.contains("rhel") || this.title.contains("rhel") || this.description.contains("rhel")) && this.published == true && (this.metadata.tenant == "shared" || this.metadata.tenant == "acme")',
    ],
  ])('%s', (_name, criteria, expected) => {
    expect(buildCatalogListFilter(criteria)).toBe(expected);
  });
});

describe('catalogItemMatchesListFilter', () => {
  const item: CatalogListFilterableItem = {
    metadata: { name: 'catalog-rhel-9', tenant: 'shared' },
    title: 'RHEL 9 catalog',
    description: 'RHEL 9 base image',
    published: true,
  };

  it('matches when filter is undefined', () => {
    expect(catalogItemMatchesListFilter(item, undefined)).toBe(true);
  });

  it('matches search terms against name, title, and description', () => {
    const filter = buildCatalogListFilter({ search: 'base image' });
    expect(catalogItemMatchesListFilter(item, filter)).toBe(true);
    expect(catalogItemMatchesListFilter(item, buildCatalogListFilter({ search: 'missing' }))).toBe(
      false,
    );
  });

  it('matches published and tenant clauses', () => {
    expect(
      catalogItemMatchesListFilter(item, buildCatalogListFilter({ published: 'published' })),
    ).toBe(true);
    expect(
      catalogItemMatchesListFilter(item, buildCatalogListFilter({ published: 'unpublished' })),
    ).toBe(false);
    expect(catalogItemMatchesListFilter(item, buildCatalogListFilter({ tenant: 'acme' }))).toBe(
      true,
    );
    expect(
      catalogItemMatchesListFilter(
        { ...item, metadata: { ...item.metadata, tenant: 'other' } },
        buildCatalogListFilter({ tenant: 'acme' }),
      ),
    ).toBe(false);
  });
});
