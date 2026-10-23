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

import { type BareMetalInstance, BareMetalInstances } from '@osac/types';
import { type CelFilter } from '@osac/ui-components/api/cel';
import { useBareMetalInstances } from '@osac/ui-components/api/v1/baremetal-instance';
import { isBareMetalPowerStateFilter } from '@osac/ui-components/components/BareMetalInstance/bareMetalInstanceDisplay';
import { buildBareMetalListFilter } from '@osac/ui-components/components/BareMetalInstance/ListPage/baremetal-instance-list-filter';
import BareMetalInstanceCard from '@osac/ui-components/components/BareMetalInstance/ListPage/BareMetalInstanceCard';
import { BareMetalTable } from '@osac/ui-components/components/BareMetalInstance/ListPage/BareMetalTable';
import BareMetalDiskImageFilter from '@osac/ui-components/components/BareMetalInstance/ListPage/Filters/BareMetalDiskImageFilter';
import BareMetalInstanceTypeFilter from '@osac/ui-components/components/BareMetalInstance/ListPage/Filters/BareMetalInstanceTypeFilter';
import BareMetalPowerStateFilter from '@osac/ui-components/components/BareMetalInstance/ListPage/Filters/BareMetalPowerStateFilter';
import ListPage from '@osac/ui-components/components/Page/ListPage';
import ListPageBody from '@osac/ui-components/components/Page/ListPageBody';
import ListPageToolbar from '@osac/ui-components/components/Page/ListPageToolbar';
import ProjectFilter from '@osac/ui-components/components/Page/ProjectFilter';
import CreateButton from '@osac/ui-components/components/Primitives/CreateButton.tsx';
import ViewSwitcher from '@osac/ui-components/components/Primitives/ViewSwitcher';
import { SEARCH_PARAM, usePageFilter } from '@osac/ui-components/hooks/use-page-filter';
import { PROJECT_FILTER_PARAM, useSession } from '@osac/ui-components/hooks/use-session';
import { useViewType } from '@osac/ui-components/hooks/use-view-type';
import { useTranslation } from '@osac/ui-components/hooks/useTranslation';

const POWER_STATE_FILTER_PARAM = 'powerState';
const INSTANCE_TYPE_FILTER_PARAM = 'instanceType';
const DISK_IMAGE_FILTER_PARAM = 'diskImage';

const BARE_METAL_LIST_VIEW_KEY = 'bare-metal-list';

export const BareMetalListPage = () => {
  const { t } = useTranslation();
  const [, setSearchParams] = useSearchParams();
  const { projects, setProjects } = useSession();
  const [search, setSearch] = usePageFilter(SEARCH_PARAM);
  const [powerStateFilterParam, setPowerStateFilterParam] = usePageFilter(POWER_STATE_FILTER_PARAM);
  const [instanceTypeFilterParam, setInstanceTypeFilterParam] = usePageFilter(
    INSTANCE_TYPE_FILTER_PARAM,
  );
  const [diskImageFilterParam, setDiskImageFilterParam] = usePageFilter(DISK_IMAGE_FILTER_PARAM);
  const powerStateFilter = isBareMetalPowerStateFilter(powerStateFilterParam)
    ? powerStateFilterParam
    : undefined;
  const [viewType] = useViewType(BARE_METAL_LIST_VIEW_KEY);

  const listFilter = useMemo(
    (): CelFilter<BareMetalInstance> | undefined =>
      buildBareMetalListFilter({
        search,
        powerState: powerStateFilter,
        instanceType: instanceTypeFilterParam.trim(),
        diskImage: diskImageFilterParam.trim(),
      }),
    [diskImageFilterParam, instanceTypeFilterParam, powerStateFilter, search],
  );

  const {
    instances,
    isLoading,
    error: instancesError,
    totalItems,
  } = useBareMetalInstances(listFilter);

  const isFiltered = Boolean(
    search ||
    projects.length ||
    powerStateFilter ||
    instanceTypeFilterParam.trim() ||
    diskImageFilterParam.trim(),
  );
  const showEmptyState = !isLoading && !instancesError && instances.length === 0;

  const clearAllFilters = () => {
    setProjects([]);
    requestAnimationFrame(() => {
      setSearchParams(
        (prev) => {
          const next = new URLSearchParams(prev);
          next.delete(SEARCH_PARAM);
          next.delete(PROJECT_FILTER_PARAM);
          next.delete(POWER_STATE_FILTER_PARAM);
          next.delete(INSTANCE_TYPE_FILTER_PARAM);
          next.delete(DISK_IMAGE_FILTER_PARAM);
          return next;
        },
        { replace: true },
      );
    });
  };

  const emptyState =
    totalItems === 0 && !isFiltered ? (
      <EmptyState titleText={t('No bare metal instances found')} headingLevel="h2">
        <EmptyStateBody>{t('No bare metal instances are available yet.')}</EmptyStateBody>
      </EmptyState>
    ) : (
      <EmptyState titleText={t('No bare metal instances match your filters')} headingLevel="h2">
        <EmptyStateBody>
          {t('Try a different project, power state, instance type, disk image, or search term.')}{' '}
          <Button variant="link" isInline onClick={clearAllFilters}>
            {t('Clear all filters')}
          </Button>
        </EmptyStateBody>
      </EmptyState>
    );

  const filteredText =
    instances.length === totalItems
      ? t('{{count}} bare metal instance', { count: totalItems })
      : t('{{shown}} of {{count}} bare metal instance', {
          shown: instances.length,
          count: totalItems,
        });

  return (
    <ListPage
      title={t('Bare Metal')}
      description={t('Monitor and manage bare metal instances across all projects.')}
      error={instancesError}
      actions={
        <CreateButton service={BareMetalInstances} to="/bare-metal/create">
          {t('Provision bare metal')}
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
                <ToolbarItem>
                  <ProjectFilter />
                </ToolbarItem>
                <ToolbarItem>
                  <BareMetalPowerStateFilter
                    selected={powerStateFilter}
                    onChange={(value) => setPowerStateFilterParam(value ?? '')}
                  />
                </ToolbarItem>
                <ToolbarItem>
                  <BareMetalInstanceTypeFilter
                    selected={instanceTypeFilterParam.trim()}
                    onChange={(value) => setInstanceTypeFilterParam(value ?? '')}
                  />
                </ToolbarItem>
                <ToolbarItem>
                  <BareMetalDiskImageFilter
                    selected={diskImageFilterParam.trim()}
                    onChange={(value) => setDiskImageFilterParam(value ?? '')}
                  />
                </ToolbarItem>
                <ToolbarItem>
                  <SearchInput
                    placeholder={t('Search by name')}
                    value={search}
                    onChange={(_e, v) => setSearch(v)}
                    onClear={() => setSearch('')}
                    aria-label={t('Filter bare metal instances by name')}
                  />
                </ToolbarItem>
                <ToolbarGroup align={{ default: 'alignEnd' }}>
                  <ToolbarItem>
                    <ViewSwitcher pageKey={BARE_METAL_LIST_VIEW_KEY} />
                  </ToolbarItem>
                </ToolbarGroup>
              </ToolbarContent>
            </Toolbar>
          </ListPageToolbar>
        </StackItem>
        <StackItem>
          <ListPageBody isLoading={isLoading} error={instancesError}>
            {showEmptyState ? (
              emptyState
            ) : viewType === 'cards' ? (
              <Gallery hasGutter minWidths={{ default: '400px' }} maxWidths={{ default: '400px' }}>
                {instances.map((instance) => (
                  <GalleryItem key={instance.id}>
                    <BareMetalInstanceCard instance={instance} />
                  </GalleryItem>
                ))}
              </Gallery>
            ) : (
              <BareMetalTable instances={instances} />
            )}
          </ListPageBody>
        </StackItem>
      </Stack>
    </ListPage>
  );
};
