export type CatalogListFilterableItem = {
  metadata?: { name?: string; tenant?: string };
  title?: string;
  published?: boolean;
};

interface CatalogListFilterTerms {
  search?: string;
  published?: boolean;
  tenants?: string[];
}

const unescapeCelStringLiteral = (value: string): string =>
  value.replaceAll('\\"', '"').replaceAll('\\\\', '\\');

const filterTermsFromCel = (filter: string | undefined): CatalogListFilterTerms => {
  if (!filter) {
    return {};
  }

  const publishedMatch = filter.match(/this\.published == (true|false)/);
  const tenants = [...filter.matchAll(/this\.metadata\.tenant == "((?:\\.|[^"\\])*)"/g)].map(
    (match) => unescapeCelStringLiteral(match[1]),
  );
  const searchMatch = filter.match(/this\.metadata\.name\.contains\("((?:\\.|[^"\\])*)"\)/);

  return {
    ...(publishedMatch ? { published: publishedMatch[1] === 'true' } : {}),
    ...(tenants.length ? { tenants } : {}),
    ...(searchMatch ? { search: unescapeCelStringLiteral(searchMatch[1]) } : {}),
  };
};

const matchesFilterTerms = (
  item: CatalogListFilterableItem,
  terms: CatalogListFilterTerms,
): boolean => {
  if (terms.published !== undefined && item.published !== terms.published) {
    return false;
  }

  if (terms.tenants && !terms.tenants.includes(item.metadata?.tenant ?? '')) {
    return false;
  }

  const search = terms.search;
  if (search && ![item.metadata?.name, item.title].some((value) => value?.includes(search))) {
    return false;
  }

  return true;
};

export const catalogItemMatchesListFilter = (
  item: CatalogListFilterableItem,
  filter: string | undefined,
): boolean => matchesFilterTerms(item, filterTermsFromCel(filter));
