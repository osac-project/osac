import { ReactNode } from 'react';
import {
  Bullseye,
  Gallery,
  GalleryItem,
  GalleryProps,
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

import { getErrorMessage } from '../../utils/error';
import QueryErrorState from '../Resource/QueryErrorState';

export interface ViewableListSectionProps<T> {
  title?: string;
  items: T[];
  pageKey: string;
  isLoading?: boolean;
  error?: unknown;
  getItemKey: (item: T) => string;
  renderCard: (item: T) => ReactNode;
  renderList: (items: T[]) => ReactNode;
  cardGalleryMinWidths?: GalleryProps['minWidths'];
  cardGalleryMaxWidths?: GalleryProps['maxWidths'];
}

export const ViewableListSection = <T,>({
  title,
  items,
  pageKey,
  isLoading = false,
  error = null,
  getItemKey,
  renderCard,
  renderList,
  cardGalleryMinWidths = { default: '400px' },
  cardGalleryMaxWidths = { default: '400px' },
}: ViewableListSectionProps<T>) => {
  const [viewTypePref] = useUserPreferences(getViewTypePrefKey(pageKey));
  const viewType: ViewType = isViewType(viewTypePref) ? viewTypePref : DEFAULT_VIEW_TYPE;

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
            <Gallery hasGutter minWidths={cardGalleryMinWidths} maxWidths={cardGalleryMaxWidths}>
              {items.map((item) => (
                <GalleryItem key={getItemKey(item)}>{renderCard(item)}</GalleryItem>
              ))}
            </Gallery>
          ) : (
            renderList(items)
          )}
        </StackItem>
      ) : null}
    </Stack>
  );
};
