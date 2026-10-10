import {
  Bullseye,
  Gallery,
  GalleryItem,
  Spinner,
  Stack,
  StackItem,
  Title,
} from '@patternfly/react-core';

import {
  DEFAULT_VIEW_TYPE,
  ViewType,
  getViewTypePrefKey,
  isViewType,
} from '@osac/ui-components/components/Primitives/ViewSwitcher';
import { useUserPreferences } from '@osac/ui-components/hooks/use-user-preferences';

import CatalogItemCard from './CatalogItemCard';
import { type CatalogItem } from './catalogItemDisplay';
import { useCatalogItemResourceLookups } from './catalogItemResourceLookups';
import CatalogItemTable from './CatalogItemTable';
import { getErrorMessage } from '../../utils/error';
import QueryErrorState from '../Resource/QueryErrorState';

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
  const [viewTypePref] = useUserPreferences(getViewTypePrefKey(CATALOG_ITEMS_VIEW_KEY));
  const viewType: ViewType = isViewType(viewTypePref) ? viewTypePref : DEFAULT_VIEW_TYPE;

  const resourceLookups = useCatalogItemResourceLookups({
    enabled: items.length > 0 && !isLoading && !error,
  });

  if (!isLoading && !error && items.length === 0) {
    return null;
  }

  return (
    <Stack hasGutter>
      {title ? (
        <StackItem>
          <Title headingLevel="h2" size="lg">
            {title}
          </Title>
        </StackItem>
      ) : null}
      {isLoading ? (
        <StackItem>
          <Bullseye>
            <Spinner aria-label={`Loading ${title ?? ''}`} />
          </Bullseye>
        </StackItem>
      ) : null}
      {error ? (
        <StackItem>
          <QueryErrorState error={error} title={title} body={getErrorMessage(error)} />
        </StackItem>
      ) : null}
      {items.length > 0 ? (
        <StackItem>
          {viewType === 'cards' ? (
            <Gallery hasGutter minWidths={{ default: '400px' }} maxWidths={{ default: '400px' }}>
              {items.map((item) => (
                <GalleryItem key={item.id}>
                  <CatalogItemCard item={item} resourceLookups={resourceLookups} />
                </GalleryItem>
              ))}
            </Gallery>
          ) : (
            <CatalogItemTable items={items} resourceLookups={resourceLookups} />
          )}
        </StackItem>
      ) : null}
    </Stack>
  );
};
