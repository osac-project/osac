import { useMemo } from 'react';
import { useSearchParams } from 'react-router-dom';
import {
  Button,
  EmptyState,
  EmptyStateBody,
  Gallery,
  GalleryItem,
  SearchInput,
  Stack,
  StackItem,
  Toolbar,
  ToolbarContent,
  ToolbarGroup,
  ToolbarItem,
} from '@patternfly/react-core';

import { IdentityProviders } from '@osac/types';
import { useListResource } from '@osac/ui-components/api/use-resource';
import IdentityProviderEnabledFilter from '@osac/ui-components/components/IdentityProvider/IdentityProviderEnabledFilter';
import ListPage from '@osac/ui-components/components/Page/ListPage';
import ListPageBody from '@osac/ui-components/components/Page/ListPageBody';
import ListPageToolbar from '@osac/ui-components/components/Page/ListPageToolbar';
import CreateButton from '@osac/ui-components/components/Primitives/CreateButton.tsx';
import ViewSwitcher from '@osac/ui-components/components/Primitives/ViewSwitcher';
import {
  SEARCH_PARAM,
  useArrayPageFilter,
  usePageFilter,
} from '@osac/ui-components/hooks/use-page-filter.ts';
import { useViewType } from '@osac/ui-components/hooks/use-view-type';
import { useTranslation } from '@osac/ui-components/hooks/useTranslation';

import IdentityProviderCard from './IdentityProviderCard';
import IdentityProviderPhaseFilter from './IdentityProviderPhaseFilter';
import IdentityProviderTable from './IdentityProviderTable';
import {
  ENABLED_FILTER_PARAM,
  IDENTITY_PROVIDER_PHASE_FILTER_TO_ENUM,
  type IdentityProviderPhaseFilterValue,
  PHASE_FILTER_PARAM,
  buildIdentityProviderListFilter,
  isIdentityProviderPhaseFilterValue,
} from './utils';

const IDENTITY_PROVIDERS_VIEW_KEY = 'identity-providers';

const IdentityProviderListPage = () => {
  const { t } = useTranslation();
  const [, setSearchParams] = useSearchParams();
  const [search, setSearch] = usePageFilter(SEARCH_PARAM);
  const [enabledFilterParam, setEnabledFilter] = usePageFilter(ENABLED_FILTER_PARAM);
  const [phaseFilters, togglePhaseFilter] = useArrayPageFilter<IdentityProviderPhaseFilterValue>(
    PHASE_FILTER_PARAM,
    isIdentityProviderPhaseFilterValue,
  );
  const [viewType] = useViewType(IDENTITY_PROVIDERS_VIEW_KEY);
  const enabledFilter =
    enabledFilterParam === 'true' ? true : enabledFilterParam === 'false' ? false : undefined;

  const phaseEnumValues = useMemo(
    () => phaseFilters.map((value) => IDENTITY_PROVIDER_PHASE_FILTER_TO_ENUM[value]),
    [phaseFilters],
  );

  const filter = useMemo(
    () => buildIdentityProviderListFilter(phaseEnumValues, enabledFilter, search.trim()),
    [phaseEnumValues, enabledFilter, search],
  );

  const { data, isLoading, error } = useListResource(IdentityProviders, { filter });

  const { data: totalsData, error: totalsError } = useListResource(IdentityProviders, {
    limit: 0,
  });

  const items = data?.items ?? [];
  const totalItems = totalsData?.total ?? 0;
  const hasTotals = totalsData !== undefined && !totalsError;

  const isFiltered = Boolean(search || enabledFilter !== undefined || phaseEnumValues.length);

  const showEmptyState = !isLoading && !error && items.length === 0;

  const clearAllFilters = () => {
    requestAnimationFrame(() => {
      setSearchParams(
        (prev) => {
          const next = new URLSearchParams(prev);
          next.delete(SEARCH_PARAM);
          next.delete(PHASE_FILTER_PARAM);
          next.delete(ENABLED_FILTER_PARAM);
          return next;
        },
        { replace: true },
      );
    });
  };

  const emptyState =
    totalItems === 0 && !isFiltered ? (
      <EmptyState titleText={t('No identity providers found')} headingLevel="h2">
        <EmptyStateBody>{t('No identity providers are available yet.')}</EmptyStateBody>
      </EmptyState>
    ) : (
      <EmptyState titleText={t('No identity providers match your filters')} headingLevel="h2">
        <EmptyStateBody>
          {t('Try a different state, enablement, or search term.')}{' '}
          <Button variant="link" isInline onClick={clearAllFilters}>
            {t('Clear all filters')}
          </Button>
        </EmptyStateBody>
      </EmptyState>
    );

  const filteredText = hasTotals
    ? items.length === totalItems
      ? t('{{count}} identity provider', { count: totalItems })
      : t('{{shown}} of {{count}} identity provider', {
          shown: items.length,
          count: totalItems,
        })
    : undefined;

  return (
    <ListPage
      title={t('Identity providers')}
      description={t('Manage identity providers for your tenant.')}
      error={error}
      actions={
        <CreateButton service={IdentityProviders} to="/tenant/identity-provider/create">
          {t('Create identity provider')}
        </CreateButton>
      }
    >
      <Stack hasGutter>
        <StackItem>
          <ListPageToolbar
            filteredText={showEmptyState ? undefined : filteredText}
            isFiltered={isFiltered}
            onClearAllFilters={clearAllFilters}
          >
            <Toolbar>
              <ToolbarContent>
                <ToolbarGroup>
                  <IdentityProviderPhaseFilter
                    phaseFilters={phaseFilters}
                    onToggle={togglePhaseFilter}
                  />
                  <IdentityProviderEnabledFilter
                    enabled={enabledFilter}
                    onChange={(value) => setEnabledFilter(value !== null ? String(value) : '')}
                  />
                  <ToolbarItem>
                    <SearchInput
                      placeholder={t('Search identity providers')}
                      value={search}
                      onChange={(_e, v) => setSearch(v)}
                      onClear={() => setSearch('')}
                      aria-label={t('Search identity providers')}
                    />
                  </ToolbarItem>
                </ToolbarGroup>
                <ToolbarGroup align={{ default: 'alignEnd' }}>
                  <ToolbarItem>
                    <ViewSwitcher pageKey={IDENTITY_PROVIDERS_VIEW_KEY} />
                  </ToolbarItem>
                </ToolbarGroup>
              </ToolbarContent>
            </Toolbar>
          </ListPageToolbar>
        </StackItem>
        <StackItem>
          <ListPageBody isLoading={isLoading} error={error}>
            {showEmptyState ? (
              emptyState
            ) : viewType === 'cards' ? (
              <Gallery hasGutter minWidths={{ default: '300px' }} maxWidths={{ default: '1fr' }}>
                {items.map((idp) => (
                  <GalleryItem key={idp.id}>
                    <IdentityProviderCard identityProvider={idp} />
                  </GalleryItem>
                ))}
              </Gallery>
            ) : (
              <IdentityProviderTable identityProviders={items} />
            )}
          </ListPageBody>
        </StackItem>
      </Stack>
    </ListPage>
  );
};

export default IdentityProviderListPage;
