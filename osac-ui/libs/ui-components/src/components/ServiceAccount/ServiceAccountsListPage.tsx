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

import { ServiceAccount, ServiceAccounts } from '@osac/types';
import { cel } from '@osac/ui-components/api/cel';
import { useListResource } from '@osac/ui-components/api/use-resource';
import ListPage from '@osac/ui-components/components/Page/ListPage';
import ListPageBody from '@osac/ui-components/components/Page/ListPageBody';
import { SEARCH_PARAM, usePageFilter } from '@osac/ui-components/hooks/use-page-filter';
import { usePermissions } from '@osac/ui-components/hooks/use-permissions';
import { useViewType } from '@osac/ui-components/hooks/use-view-type';
import { useTranslation } from '@osac/ui-components/hooks/useTranslation';

import ServiceAccountCard from './ServiceAccountCard';
import ServiceAccountsTable from './ServiceAccountsTable';
import ListPageToolbar from '../Page/ListPageToolbar';
import ViewSwitcher from '../Primitives/ViewSwitcher';

const SA_VIEW_KEY = 'service-accounts';

const ServiceAccountsListPage = () => {
  const { t } = useTranslation();
  const permissions = usePermissions(ServiceAccounts, 'list');

  const [search, setSearch] = usePageFilter(SEARCH_PARAM);
  const [viewType] = useViewType(SA_VIEW_KEY);
  const isFiltered = !!search;
  const clearAllFilters = () => setSearch('');

  const { data, isLoading, error } = useListResource(
    ServiceAccounts,
    {
      filter: cel<ServiceAccount>((filter) =>
        search ? filter.field('metadata.name').contains(search) : undefined,
      ),
    },
    { enabled: permissions.allowed },
  );
  const serviceAccounts = data?.items ?? [];

  const emptyState = !isFiltered ? (
    <EmptyState titleText={t('No service accounts found')} headingLevel="h2">
      <EmptyStateBody>{t('No service accounts are available yet.')}</EmptyStateBody>
    </EmptyState>
  ) : (
    <EmptyState titleText={t('No service accounts match your filters')} headingLevel="h2">
      <EmptyStateBody>
        {t('Try a different search term.')}{' '}
        <Button variant="link" isInline onClick={clearAllFilters}>
          {t('Clear all filters')}
        </Button>
      </EmptyStateBody>
    </EmptyState>
  );

  return (
    <ListPage
      title={t('Service accounts')}
      description={t('Create non-human accounts for CI systems and other automation.')}
      error={error}
      permissions={permissions}
    >
      <Stack hasGutter>
        <StackItem>
          <ListPageToolbar isFiltered={isFiltered} onClearAllFilters={clearAllFilters}>
            <Toolbar>
              <ToolbarContent>
                <ToolbarGroup>
                  <ToolbarItem>
                    <SearchInput
                      placeholder={t('Search service accounts')}
                      value={search}
                      onChange={(_e, v) => setSearch(v)}
                      onClear={() => setSearch('')}
                      aria-label={t('Search service accounts')}
                    />
                  </ToolbarItem>
                </ToolbarGroup>
                <ToolbarGroup align={{ default: 'alignEnd' }}>
                  <ToolbarItem>
                    <ViewSwitcher pageKey={SA_VIEW_KEY} />
                  </ToolbarItem>
                </ToolbarGroup>
              </ToolbarContent>
            </Toolbar>
          </ListPageToolbar>
        </StackItem>
        <StackItem>
          <ListPageBody isLoading={isLoading} error={error}>
            {data?.total === 0 ? (
              emptyState
            ) : viewType === 'cards' ? (
              <Gallery hasGutter minWidths={{ default: '300px' }} maxWidths={{ default: '1fr' }}>
                {serviceAccounts.map((sa) => (
                  <GalleryItem key={sa.id}>
                    <ServiceAccountCard serviceAccount={sa} />
                  </GalleryItem>
                ))}
              </Gallery>
            ) : (
              <ServiceAccountsTable serviceAccounts={serviceAccounts} />
            )}
          </ListPageBody>
        </StackItem>
      </Stack>
    </ListPage>
  );
};

export default ServiceAccountsListPage;
