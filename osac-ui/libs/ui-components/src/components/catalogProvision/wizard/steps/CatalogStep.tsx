import { useMemo, useState } from 'react';
import {
  Alert,
  Bullseye,
  Button,
  Content,
  Flex,
  FlexItem,
  Gallery,
  GalleryItem,
  SearchInput,
  Spinner,
  Stack,
  StackItem,
} from '@patternfly/react-core';
import { useFormikContext } from 'formik';

import { useTranslation } from '../../../../hooks/useTranslation';
import CatalogItemCard from '../../../catalog/CatalogItemCard';
import { CatalogItem, filterCatalogItemsBySearch } from '../../../catalog/catalogItemDisplay';
import { useCatalogItemResourceLookups } from '../../../catalog/catalogItemResourceLookups';
import { getVisibleFieldError } from '../../../Form/fieldError';
import { useShowFieldValidationErrors } from '../../../Form/FieldValidationContext';
import { FormFieldHelper } from '../../../Form/FormFieldHelper';
import type { CatalogProvisionAdapter } from '../adapters/types';

interface Props<TValues extends { catalogItemId: string }, TPayload> {
  adapter: CatalogProvisionAdapter<CatalogItem, TValues, TPayload>;
}

export const CatalogStep = <TValues extends { catalogItemId: string }, TPayload>({
  adapter,
}: Props<TValues, TPayload>) => {
  const { t } = useTranslation();
  const [search, setSearch] = useState('');
  const formik = useFormikContext<TValues>();
  const { values } = formik;

  const {
    data: catalogItems = [],
    isPending: catalogLoading,
    isError: catalogError,
    refetch: refetchCatalogItems,
  } = adapter.useCatalogItems();

  const filtered = useMemo(
    () => filterCatalogItemsBySearch(catalogItems, search),
    [catalogItems, search],
  );

  const resourceLookups = useCatalogItemResourceLookups({
    enabled: !catalogLoading && !catalogError && filtered.length > 0,
  });

  const count = filtered.length;
  const countPhrase = t('catalogProvision.catalog.count', { count });
  const showValidationErrors = useShowFieldValidationErrors();
  const catalogItemError = getVisibleFieldError(
    formik.getFieldMeta('catalogItemId'),
    showValidationErrors,
  );

  const handleSelect = async (item: CatalogItem) => {
    await adapter.onCatalogItemSelected?.(item, formik);
  };

  return (
    <Stack hasGutter>
      <StackItem>
        <Flex
          direction={{ default: 'column', md: 'row' }}
          flexWrap={{ default: 'wrap' }}
          alignItems={{ default: 'alignItemsFlexEnd' }}
          gap={{ default: 'gapMd' }}
        >
          <FlexItem flex={{ default: 'flex_1' }}>
            <SearchInput
              placeholder={t('catalogProvision.catalog.searchPlaceholder')}
              value={search}
              onChange={(_event, value) => setSearch(value)}
              onClear={() => setSearch('')}
              aria-label={t('catalogProvision.catalog.searchAria')}
            />
          </FlexItem>
        </Flex>
      </StackItem>
      <StackItem>
        <Content component="p">
          {catalogLoading ? t('catalogProvision.catalog.loading') : countPhrase}
        </Content>
      </StackItem>
      {catalogItemError ? (
        <StackItem>
          <FormFieldHelper error={catalogItemError} fieldId="catalog-item-selection" />
        </StackItem>
      ) : null}
      {catalogError ? (
        <StackItem>
          <Stack hasGutter>
            <StackItem>
              <Alert variant="danger" title={t('catalogProvision.catalog.loadError')}>
                {t('catalogProvision.catalog.loadErrorDetail')}
              </Alert>
            </StackItem>
            <StackItem>
              <Button variant="primary" onClick={() => void refetchCatalogItems()}>
                {t('catalogProvision.actions.retry')}
              </Button>
            </StackItem>
          </Stack>
        </StackItem>
      ) : null}
      <StackItem>
        <Gallery
          hasGutter
          minWidths={{ default: '300px' }}
          role="radiogroup"
          aria-label={t('catalogProvision.steps.catalog.title')}
        >
          {catalogLoading ? (
            <GalleryItem>
              <Bullseye>
                <Spinner aria-label={t('catalogProvision.catalog.loading')} />
              </Bullseye>
            </GalleryItem>
          ) : null}
          {!catalogLoading && !catalogError && count === 0 ? (
            <GalleryItem>
              <Content component="p">{t('catalogProvision.catalog.empty')}</Content>
            </GalleryItem>
          ) : null}
          {!catalogLoading &&
            !catalogError &&
            filtered.map((item) => {
              const selected = values.catalogItemId === item.id;
              return (
                <GalleryItem key={item.id}>
                  <CatalogItemCard
                    item={item}
                    resourceLookups={resourceLookups}
                    selection={{
                      selected,
                      onSelect: () => {
                        void handleSelect(item);
                      },
                    }}
                  />
                </GalleryItem>
              );
            })}
        </Gallery>
      </StackItem>
    </Stack>
  );
};
