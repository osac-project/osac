import { useMemo, useState } from 'react';
import type { DescMessage } from '@bufbuild/protobuf';
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

import { type ResourceService, useListResource } from '../../api/use-resource';
import { useTranslation } from '../../hooks/useTranslation';
import CatalogItemCard from '../catalog/CatalogItemCard';
import { CatalogItem, filterCatalogItemsBySearch } from '../catalog/catalogItemDisplay';
import { useCatalogItemResourceLookups } from '../catalog/catalogItemResourceLookups';
import { getVisibleFieldError } from '../Form/fieldError';
import { useShowFieldValidationErrors } from '../Form/FieldValidationContext';
import { FormFieldHelper } from '../Form/FormFieldHelper';

interface CatalogFormValues {
  catalogItemReference: { id?: string; name?: string };
}

interface Props<T extends CatalogItem, V extends CatalogFormValues> {
  catalogService: ResourceService<'list', DescMessage, DescMessage>;
  isInitializing?: boolean;
  getInitialFormValues: (item: T) => V;
  onCatalogItemSelected?: (item: T) => void;
}

export const CatalogStep = <T extends CatalogItem, V extends CatalogFormValues>({
  catalogService,
  isInitializing = false,
  getInitialFormValues,
  onCatalogItemSelected,
}: Props<T, V>) => {
  const { t } = useTranslation();
  const [search, setSearch] = useState('');
  const formik = useFormikContext<V>();
  const { values } = formik;
  const catalogQuery = useListResource<DescMessage, DescMessage, T[]>(
    catalogService,
    {},
    {},
    (data) => data.items,
  );
  const loading = isInitializing || catalogQuery.isPending;
  const error = catalogQuery.isError;
  const retry = () => void catalogQuery.refetch();

  const filtered = useMemo(
    () => filterCatalogItemsBySearch(catalogQuery.data ?? [], search),
    [catalogQuery.data, search],
  );

  const resourceLookups = useCatalogItemResourceLookups({
    enabled: !loading && !error && filtered.length > 0,
  });

  const count = filtered.length;
  const countPhrase = t('catalogProvision.catalog.count', { count });
  const showValidationErrors = useShowFieldValidationErrors();
  const catalogItemError = getVisibleFieldError(
    formik.getFieldMeta('catalogItemReference'),
    showValidationErrors,
  );

  const handleSelect = (item: T) => {
    formik.resetForm({ values: getInitialFormValues(item) });
    onCatalogItemSelected?.(item);
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
          {loading ? t('catalogProvision.catalog.loading') : countPhrase}
        </Content>
      </StackItem>
      {catalogItemError ? (
        <StackItem>
          <FormFieldHelper error={catalogItemError} fieldId="catalog-item-selection" />
        </StackItem>
      ) : null}
      {error ? (
        <StackItem>
          <Stack hasGutter>
            <StackItem>
              <Alert variant="danger" title={t('catalogProvision.catalog.loadError')}>
                {t('catalogProvision.catalog.loadErrorDetail')}
              </Alert>
            </StackItem>
            <StackItem>
              <Button variant="primary" onClick={() => void retry()}>
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
          {loading ? (
            <GalleryItem>
              <Bullseye>
                <Spinner aria-label={t('catalogProvision.catalog.loading')} />
              </Bullseye>
            </GalleryItem>
          ) : null}
          {!loading && !error && count === 0 ? (
            <GalleryItem>
              <Content component="p">{t('catalogProvision.catalog.empty')}</Content>
            </GalleryItem>
          ) : null}
          {!loading &&
            !error &&
            filtered.map((item) => {
              const selected =
                (values.catalogItemReference as { id?: string } | undefined)?.id === item.id;
              return (
                <GalleryItem key={item.id}>
                  <CatalogItemCard
                    item={item}
                    resourceLookups={resourceLookups}
                    selection={{
                      selected,
                      onSelect: () => {
                        handleSelect(item as T);
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
