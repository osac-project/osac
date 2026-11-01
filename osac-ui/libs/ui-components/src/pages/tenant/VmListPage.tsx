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

import { ComputeInstances } from '@osac/types';
import { useComputeInstances } from '@osac/ui-components/api/v1/compute-instance';
import DiskImageFilter from '@osac/ui-components/components/DiskImage/DiskImageFilter';
import ListPage from '@osac/ui-components/components/Page/ListPage';
import ListPageBody from '@osac/ui-components/components/Page/ListPageBody';
import ListPageToolbar from '@osac/ui-components/components/Page/ListPageToolbar';
import ProjectFilter from '@osac/ui-components/components/Page/ProjectFilter';
import CreateButton from '@osac/ui-components/components/Primitives/CreateButton.tsx';
import ViewSwitcher from '@osac/ui-components/components/Primitives/ViewSwitcher';
import { buildVmListFilter } from '@osac/ui-components/components/vm/ListPage/vm-list-filter';
import VmInstanceCard from '@osac/ui-components/components/vm/ListPage/VmInstanceCard';
import VmInstanceTypeFilter from '@osac/ui-components/components/vm/ListPage/VmInstanceTypeFilter';
import VmPowerStateFilter from '@osac/ui-components/components/vm/ListPage/VmPowerStateFilter';
import { VmTable } from '@osac/ui-components/components/vm/ListPage/VmTable';
import { isVmPowerStateFilter } from '@osac/ui-components/components/vm/vmInstanceDisplay';
import { SEARCH_PARAM, usePageFilter } from '@osac/ui-components/hooks/use-page-filter';
import { PROJECT_FILTER_PARAM, useSession } from '@osac/ui-components/hooks/use-session';
import { useViewType } from '@osac/ui-components/hooks/use-view-type';
import { useTranslation } from '@osac/ui-components/hooks/useTranslation';

const POWER_STATE_FILTER_PARAM = 'powerState';
const INSTANCE_TYPE_FILTER_PARAM = 'instanceType';
const DISK_IMAGE_FILTER_PARAM = 'diskImage';

const VM_LIST_VIEW_KEY = 'vm-list';

export const VmListPage = () => {
  const { t } = useTranslation();
  const [, setSearchParams] = useSearchParams();
  const { projects, setProjects } = useSession();
  const [search, setSearch] = usePageFilter(SEARCH_PARAM);
  const [powerStateFilterParam, setPowerStateFilterParam] = usePageFilter(POWER_STATE_FILTER_PARAM);
  const [instanceTypeFilterParam, setInstanceTypeFilterParam] = usePageFilter(
    INSTANCE_TYPE_FILTER_PARAM,
  );
  const powerStateFilter = isVmPowerStateFilter(powerStateFilterParam)
    ? powerStateFilterParam
    : undefined;
  const [diskImageFilterParam, setDiskImageFilterParam] = usePageFilter(DISK_IMAGE_FILTER_PARAM);
  const diskImageFilter = diskImageFilterParam.trim() || undefined;
  const instanceTypeFilter = instanceTypeFilterParam.trim() || undefined;
  const [viewType] = useViewType(VM_LIST_VIEW_KEY);

  const listFilter = useMemo(
    () =>
      buildVmListFilter({
        search,
        powerState: powerStateFilter,
        instanceType: instanceTypeFilter,
        diskImage: diskImageFilter,
      }),
    [diskImageFilter, instanceTypeFilter, powerStateFilter, search],
  );

  const { vms, totalItems, isLoading, error } = useComputeInstances(listFilter);

  const isFiltered = Boolean(
    search || projects.length || powerStateFilter || instanceTypeFilter || diskImageFilter,
  );
  const showEmptyState = !isLoading && !error && vms.length === 0;

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
      <EmptyState titleText={t('No virtual machines found')} headingLevel="h2">
        <EmptyStateBody>{t('No virtual machines are available yet.')}</EmptyStateBody>
      </EmptyState>
    ) : (
      <EmptyState titleText={t('No virtual machines match your filters')} headingLevel="h2">
        <EmptyStateBody>
          {t('Try a different project, power state, instance type, disk image, or search term.')}{' '}
          <Button variant="link" isInline onClick={clearAllFilters}>
            {t('Clear all filters')}
          </Button>
        </EmptyStateBody>
      </EmptyState>
    );

  const filteredText =
    vms.length === totalItems
      ? t('{{count}} virtual machine', { count: totalItems })
      : t('{{shown}} of {{count}} virtual machine', {
          shown: vms.length,
          count: totalItems,
        });

  return (
    <ListPage
      title={t('Virtual machines')}
      description={t('Monitor and manage virtual machine instances across all projects.')}
      error={error}
      actions={
        <CreateButton service={ComputeInstances} to="/vms/create">
          {t('Create virtual machine')}
        </CreateButton>
      }
    >
      <Stack hasGutter>
        <StackItem>
          <ListPageToolbar
            filteredText={!isLoading && !error && !showEmptyState ? filteredText : undefined}
            isFiltered={isFiltered}
            onClearAllFilters={clearAllFilters}
          >
            <Toolbar>
              <ToolbarContent>
                <ToolbarItem>
                  <ProjectFilter />
                </ToolbarItem>
                <ToolbarItem>
                  <VmPowerStateFilter
                    selected={powerStateFilter}
                    onChange={(value) => setPowerStateFilterParam(value ?? '')}
                  />
                </ToolbarItem>
                <ToolbarItem>
                  <VmInstanceTypeFilter
                    selected={instanceTypeFilter}
                    onChange={(value) => setInstanceTypeFilterParam(value ?? '')}
                  />
                </ToolbarItem>
                <ToolbarItem>
                  <DiskImageFilter
                    selected={diskImageFilterParam.trim()}
                    onChange={(value) => setDiskImageFilterParam(value ?? '')}
                  />
                </ToolbarItem>
                <ToolbarItem>
                  <SearchInput
                    placeholder={t('Search by name')}
                    value={search}
                    onChange={(_event, value) => setSearch(value)}
                    onClear={() => setSearch('')}
                    aria-label={t('Filter virtual machines by name')}
                  />
                </ToolbarItem>
                <ToolbarGroup align={{ default: 'alignEnd' }}>
                  <ToolbarItem>
                    <ViewSwitcher pageKey={VM_LIST_VIEW_KEY} />
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
              <Gallery hasGutter minWidths={{ default: '400px' }} maxWidths={{ default: '400px' }}>
                {vms.map((vm) => (
                  <GalleryItem key={vm.id}>
                    <VmInstanceCard vm={vm} />
                  </GalleryItem>
                ))}
              </Gallery>
            ) : (
              <VmTable vms={vms} />
            )}
          </ListPageBody>
        </StackItem>
      </Stack>
    </ListPage>
  );
};
