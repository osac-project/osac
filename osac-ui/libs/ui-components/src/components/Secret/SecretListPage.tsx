import { useState } from 'react';
import { useNavigate, useSearchParams } from 'react-router-dom';
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

import { Secret, Secrets } from '@osac/types';
import { cel } from '@osac/ui-components/api/cel';
import { useListResource } from '@osac/ui-components/api/use-resource';
import ListPageToolbar from '@osac/ui-components/components/Page/ListPageToolbar';
import {
  SEARCH_PARAM,
  useArrayPageFilter,
  usePageFilter,
} from '@osac/ui-components/hooks/use-page-filter';
import { useProjectFilterQuery } from '@osac/ui-components/hooks/use-project-filter-query';
import { PROJECT_FILTER_PARAM, useSession } from '@osac/ui-components/hooks/use-session';
import { useViewType } from '@osac/ui-components/hooks/use-view-type';
import { useTranslation } from '@osac/ui-components/hooks/useTranslation';

import SecretCard from './SecretCard.tsx';
import SecretDeleteModal from './SecretDeleteModal.tsx';
import SecretTable from './SecretTable.tsx';
import TypeFilter from './TypeFilter.tsx';
import {
  TYPE_FILTER_TO_ENUM,
  TYPE_PARAM,
  type TypeFilterValue,
  isTypeFilterValue,
} from './utils.ts';
import ListPage from '../Page/ListPage';
import ListPageBody from '../Page/ListPageBody';
import ProjectFilter from '../Page/ProjectFilter';
import CreateButton from '../Primitives/CreateButton.tsx';
import ViewSwitcher from '../Primitives/ViewSwitcher.tsx';

const SECRETS_VIEW_KEY = 'secrets';

const SecretListPage = () => {
  const navigate = useNavigate();
  const { t } = useTranslation();
  const [, setSearchParams] = useSearchParams();
  const [deleteTarget, setDeleteTarget] = useState<Secret>();
  const { projects, setProjects } = useSession();
  const [search, setSearch] = usePageFilter(SEARCH_PARAM);
  const [type, setType, clearTypeFilter] = useArrayPageFilter<TypeFilterValue>(
    TYPE_PARAM,
    isTypeFilterValue,
  );
  const projectFilter = useProjectFilterQuery<Secret>();
  const [viewType] = useViewType(SECRETS_VIEW_KEY);

  const typeFilter = type.map((t) => TYPE_FILTER_TO_ENUM[t]);

  const { data, isLoading, error } = useListResource(Secrets, {
    filter: cel<Secret>((filter) =>
      filter.and(
        projectFilter,
        search ? filter.field('metadata.name').contains(search) : undefined,
        type.length
          ? filter.or(...typeFilter.map((f) => filter.field('type').equals(f)))
          : undefined,
      ),
    ),
  });

  const { data: totalsData, error: totalsError } = useListResource(Secrets, {
    filter: projectFilter,
    limit: 0,
  });

  const items = data?.items ?? [];
  const totalItems = totalsData?.total;
  const hasTotals = totalsData !== undefined && !totalsError;

  const handleEdit = (secret: Secret) => navigate(`/secrets/${secret.id}/edit`);
  const handleDelete = (secret: Secret) => setDeleteTarget(secret);

  const isFiltered = Boolean(search || projects.length || typeFilter.length);

  const showEmptyState = !isLoading && !error && items.length === 0;

  const clearAllFilters = () => {
    setProjects([]);
    requestAnimationFrame(() => {
      setSearchParams(
        (prev) => {
          const next = new URLSearchParams(prev);
          next.delete(SEARCH_PARAM);
          next.delete(PROJECT_FILTER_PARAM);
          next.delete(TYPE_PARAM);
          return next;
        },
        { replace: true },
      );
    });
  };

  const emptyState =
    (totalItems ?? 0) === 0 && !isFiltered ? (
      <EmptyState titleText={t('No secrets found')} headingLevel="h2">
        <EmptyStateBody>{t('No secrets are available yet.')}</EmptyStateBody>
      </EmptyState>
    ) : (
      <EmptyState titleText={t('No secrets match your filters')} headingLevel="h2">
        <EmptyStateBody>
          {t('Try a different project, type, or search term.')}{' '}
          <Button variant="link" isInline onClick={clearAllFilters}>
            {t('Clear all filters')}
          </Button>
        </EmptyStateBody>
      </EmptyState>
    );

  const filteredText = hasTotals
    ? items.length === totalItems
      ? t('{{count}} secret', { count: totalItems })
      : t('{{shown}} of {{count}} secret', {
          shown: items.length,
          count: totalItems,
        })
    : undefined;

  return (
    <>
      {deleteTarget && (
        <SecretDeleteModal
          secret={deleteTarget}
          onClose={() => setDeleteTarget(undefined)}
          onSuccess={() => setDeleteTarget(undefined)}
        />
      )}
      <ListPage
        title={t('Secrets')}
        description={t(
          'Store credentials for use at launch. Encrypted at rest in the platform vault.',
        )}
        actions={
          <CreateButton service={Secrets} to="/secrets/create">
            {t('Create secret')}
          </CreateButton>
        }
        error={error}
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
                    <ToolbarItem>
                      <ProjectFilter />
                    </ToolbarItem>
                    <TypeFilter type={type} onClear={clearTypeFilter} onToggle={setType} />
                    <ToolbarItem>
                      <SearchInput
                        placeholder={t('Search secrets')}
                        value={search}
                        onChange={(_e, v) => setSearch(v)}
                        onClear={() => setSearch('')}
                        aria-label={t('Search secrets')}
                      />
                    </ToolbarItem>
                  </ToolbarGroup>
                  <ToolbarGroup align={{ default: 'alignEnd' }}>
                    <ToolbarItem>
                      <ViewSwitcher pageKey={SECRETS_VIEW_KEY} />
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
                  {items.map((secret) => (
                    <GalleryItem key={secret.id}>
                      <SecretCard
                        secret={secret}
                        onEdit={() => handleEdit(secret)}
                        onDelete={() => handleDelete(secret)}
                      />
                    </GalleryItem>
                  ))}
                </Gallery>
              ) : (
                <SecretTable items={items} onEdit={handleEdit} onDelete={handleDelete} />
              )}
            </ListPageBody>
          </StackItem>
        </Stack>
      </ListPage>
    </>
  );
};

export default SecretListPage;
