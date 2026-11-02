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

import { type Volume, Volumes } from '@osac/types';
import { useListResource } from '@osac/ui-components/api/use-resource';
import ListPage from '@osac/ui-components/components/Page/ListPage';
import ListPageBody from '@osac/ui-components/components/Page/ListPageBody';
import ListPageToolbar from '@osac/ui-components/components/Page/ListPageToolbar';
import ProjectFilter from '@osac/ui-components/components/Page/ProjectFilter';
import ViewSwitcher from '@osac/ui-components/components/Primitives/ViewSwitcher';
import {
  STATE_FILTER_PARAM,
  VOLUME_STATE_FILTER_TO_ENUM,
  type VolumeStateFilterValue,
  buildVolumeListFilter,
  isVolumeStateFilterValue,
} from '@osac/ui-components/components/Volume/utils';
import VolumeCard from '@osac/ui-components/components/Volume/VolumeCard';
import VolumeStateFilter from '@osac/ui-components/components/Volume/VolumeStateFilter';
import { VolumeTable } from '@osac/ui-components/components/Volume/VolumeTable';
import {
  SEARCH_PARAM,
  useArrayPageFilter,
  usePageFilter,
} from '@osac/ui-components/hooks/use-page-filter';
import { useProjectFilterQuery } from '@osac/ui-components/hooks/use-project-filter-query';
import { PROJECT_FILTER_PARAM, useSession } from '@osac/ui-components/hooks/use-session';
import { useViewType } from '@osac/ui-components/hooks/use-view-type';
import { useTranslation } from '@osac/ui-components/hooks/useTranslation';

const VOLUMES_VIEW_KEY = 'volumes';

export const VolumesListPage = () => {
  const { t } = useTranslation();
  const [, setSearchParams] = useSearchParams();
  const { projects, setProjects } = useSession();
  const [search, setSearch] = usePageFilter(SEARCH_PARAM);
  const [stateFilters, toggleStateFilter] = useArrayPageFilter<VolumeStateFilterValue>(
    STATE_FILTER_PARAM,
    isVolumeStateFilterValue,
  );
  const projectFilter = useProjectFilterQuery<Volume>();
  const [viewType] = useViewType(VOLUMES_VIEW_KEY);

  const stateEnumValues = useMemo(
    () => stateFilters.map((value) => VOLUME_STATE_FILTER_TO_ENUM[value]),
    [stateFilters],
  );

  const filter = useMemo(
    () => buildVolumeListFilter(projectFilter, stateEnumValues, search.trim()),
    [projectFilter, stateEnumValues, search],
  );

  const { data, isLoading, error } = useListResource(Volumes, { filter });

  const { data: totalsData, error: totalsError } = useListResource(Volumes, {
    filter: projectFilter,
    limit: 0,
  });

  const items = data?.items ?? [];
  const totalItems = totalsData?.total ?? 0;
  const hasTotals = totalsData !== undefined && !totalsError;

  const isFiltered = Boolean(search || projects.length || stateEnumValues.length);

  const showEmptyState = !isLoading && !error && items.length === 0;

  const clearAllFilters = () => {
    setProjects([]);
    requestAnimationFrame(() => {
      setSearchParams(
        (prev) => {
          const next = new URLSearchParams(prev);
          next.delete(SEARCH_PARAM);
          next.delete(PROJECT_FILTER_PARAM);
          next.delete(STATE_FILTER_PARAM);
          return next;
        },
        { replace: true },
      );
    });
  };

  const emptyState =
    totalItems === 0 && !isFiltered ? (
      <EmptyState titleText={t('No volumes found')} headingLevel="h2">
        <EmptyStateBody>{t('No volumes are available yet.')}</EmptyStateBody>
      </EmptyState>
    ) : (
      <EmptyState titleText={t('No volumes match your filters')} headingLevel="h2">
        <EmptyStateBody>
          {t('Try a different project, state, or search term.')}{' '}
          <Button variant="link" isInline onClick={clearAllFilters}>
            {t('Clear all filters')}
          </Button>
        </EmptyStateBody>
      </EmptyState>
    );

  const filteredText = hasTotals
    ? items.length === totalItems
      ? t('{{count}} volume', { count: totalItems })
      : t('{{shown}} of {{count}} volume', {
          shown: items.length,
          count: totalItems,
        })
    : undefined;

  return (
    <ListPage
      title={t('Volumes')}
      description={t('View and manage your storage volumes.')}
      error={error}
      // TODO: re-enable when volume creation is supported after developer preview
      // actions={<CreateButton to="/storage/volumes/create">{t('Create volume')}</CreateButton>}
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
                  <VolumeStateFilter stateFilters={stateFilters} onToggle={toggleStateFilter} />
                  <ToolbarItem>
                    <SearchInput
                      placeholder={t('Search volumes')}
                      value={search}
                      onChange={(_e, v) => setSearch(v)}
                      onClear={() => setSearch('')}
                      aria-label={t('Search volumes')}
                    />
                  </ToolbarItem>
                </ToolbarGroup>
                <ToolbarGroup align={{ default: 'alignEnd' }}>
                  <ToolbarItem>
                    <ViewSwitcher pageKey={VOLUMES_VIEW_KEY} />
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
                {items.map((volume) => (
                  <GalleryItem key={volume.id}>
                    <VolumeCard volume={volume} />
                  </GalleryItem>
                ))}
              </Gallery>
            ) : (
              <VolumeTable volumes={items} />
            )}
          </ListPageBody>
        </StackItem>
      </Stack>
    </ListPage>
  );
};
