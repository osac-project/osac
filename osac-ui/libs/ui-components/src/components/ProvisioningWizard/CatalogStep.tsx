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
import { buildCatalogListFilter } from '../../api/v1/catalog-item-list-filter';
import { useSession } from '../../hooks/use-session';
import { useTranslation } from '../../hooks/useTranslation';
import CatalogItemCard from '../catalog/CatalogItemCard';
import { CatalogItem, filterCatalogItemsBySearch } from '../catalog/catalogItemDisplay';
import { useCatalogItemResourceLookups } from '../catalog/catalogItemResourceLookups';
import { getVisibleFieldError } from '../Form/fieldError';
import { useShowFieldValidationErrors } from '../Form/FieldValidationContext';
import { FormFieldHelper } from '../Form/FormFieldHelper';
import type { ResourceSelectValue } from '../Form/resourceSelectValue';
import TenantSelectField from '../Form/TenantSelectField';

interface CatalogFormValues {
  catalogItemReference: { id?: string; name?: string };
  metadata: { tenant?: ResourceSelectValue };
}

const isCatalogItemList = <T extends CatalogItem>(value: unknown): value is { items: T[] } =>
  value !== null && typeof value === 'object' && 'items' in value && Array.isArray(value.items);

interface Props<T extends CatalogItem, V extends CatalogFormValues> {
  catalogService: ResourceService<'list', DescMessage, DescMessage>;
  isInitializing?: boolean;
  getInitialFormValues: (item: T) => V;
  /** Form values with no catalog item, used when a cloud provider admin changes the tenant. */
  getEmptyFormValues: () => V;
  onCatalogItemSelected?: (item: T | undefined) => void;
}

export const CatalogStep = <T extends CatalogItem, V extends CatalogFormValues>({
  catalogService,
  isInitializing = false,
  getInitialFormValues,
  getEmptyFormValues,
  onCatalogItemSelected,
}: Props<T, V>) => {
  const { t } = useTranslation();
  const { role } = useSession();
  const [search, setSearch] = useState('');
  const formik = useFormikContext<V>();
  const { values } = formik;
  const isAdmin = role === 'admin';
  // A cloud provider admin provisions on behalf of a tenant and sees every tenant's items, so the
  // list is limited to the chosen tenant's items and the shared ones, as that tenant sees them.
  const tenant = values.metadata?.tenant;
  const tenantId = tenant?.id ?? '';
  const catalogFilter = isAdmin
    ? buildCatalogListFilter({ tenant: tenantId })
    : role === 'tenant-user'
      ? buildCatalogListFilter({ published: 'published' })
      : undefined;
  const catalogQuery = useListResource<DescMessage, DescMessage, T[]>(
    catalogService,
    catalogFilter ? { filter: catalogFilter } : {},
    { enabled: !isAdmin || tenantId !== '' },
    (data) => (isCatalogItemList<T>(data) ? data.items : []),
  );
  const awaitingTenant = isAdmin && tenantId === '';
  const loading = !awaitingTenant && (isInitializing || catalogQuery.isPending);
  const error = catalogQuery.isError;
  const retry = () => void catalogQuery.refetch();

  const filtered = useMemo(
    () => filterCatalogItemsBySearch(catalogQuery.data ?? [], search),
    [catalogQuery.data, search],
  );

  const resourceLookups = useCatalogItemResourceLookups({
    enabled: !awaitingTenant && !loading && !error && filtered.length > 0,
  });

  const count = filtered.length;
  const countPhrase = t('catalogProvision.catalog.count', { count });
  const showValidationErrors = useShowFieldValidationErrors();
  const catalogItemError = getVisibleFieldError(
    formik.getFieldMeta('catalogItemReference'),
    showValidationErrors,
  );

  const withTenant = (formValues: V, selectedTenant: ResourceSelectValue | undefined): V =>
    isAdmin && selectedTenant
      ? { ...formValues, metadata: { ...formValues.metadata, tenant: selectedTenant } }
      : formValues;

  const handleSelect = (item: T) => {
    formik.resetForm({ values: withTenant(getInitialFormValues(item), tenant) });
    onCatalogItemSelected?.(item);
  };

  const handleTenantSelect = (selectedTenant: ResourceSelectValue) => {
    if (selectedTenant.id === tenantId) {
      return;
    }
    // Every other choice depends on the tenant, so start over in the new one.
    setSearch('');
    formik.resetForm({ values: withTenant(getEmptyFormValues(), selectedTenant) });
    onCatalogItemSelected?.(undefined);
  };

  return (
    <Stack hasGutter>
      {isAdmin ? (
        <StackItem>
          <TenantSelectField onSelect={handleTenantSelect} />
        </StackItem>
      ) : null}
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
              isDisabled={awaitingTenant}
            />
          </FlexItem>
        </Flex>
      </StackItem>
      <StackItem>
        <Content component="p">
          {awaitingTenant
            ? t('Select a tenant to see the catalog items available to it.')
            : loading
              ? t('catalogProvision.catalog.loading')
              : countPhrase}
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
          {!awaitingTenant && !loading && !error && count === 0 ? (
            <GalleryItem>
              <Content component="p">{t('catalogProvision.catalog.empty')}</Content>
            </GalleryItem>
          ) : null}
          {!awaitingTenant &&
            !loading &&
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
