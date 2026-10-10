import { ViewableListSection } from '@osac/ui-components/components/Page/ViewableListSection';

import CatalogItemCard from './CatalogItemCard';
import { type CatalogItem } from './catalogItemDisplay';
import { useCatalogItemResourceLookups } from './catalogItemResourceLookups';
import CatalogItemTable from './CatalogItemTable';

interface CatalogItemListSectionProps {
  title?: string;
  items: CatalogItem[];
  isLoading?: boolean;
  error?: unknown;
}

export const CATALOG_ITEMS_VIEW_KEY = 'catalog-items';

export const CatalogItemListSection = ({
  title,
  items,
  isLoading = false,
  error = null,
}: CatalogItemListSectionProps) => {
  const resourceLookups = useCatalogItemResourceLookups({
    enabled: items.length > 0 && !isLoading && !error,
  });

  if (!isLoading && !error && items.length === 0) {
    return null;
  }

  return (
    <ViewableListSection
      title={title}
      items={items}
      pageKey={CATALOG_ITEMS_VIEW_KEY}
      isLoading={isLoading}
      error={error}
      getItemKey={(item) => item.id}
      renderCard={(item) => <CatalogItemCard item={item} resourceLookups={resourceLookups} />}
      renderList={(catalogItems) => (
        <CatalogItemTable items={catalogItems} resourceLookups={resourceLookups} />
      )}
    />
  );
};
