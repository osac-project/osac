import { useMemo, useState } from 'react';
import { useSearchParams } from 'react-router-dom';
import {
  Button,
  EmptyState,
  EmptyStateBody,
  Gallery,
  GalleryItem,
  Stack,
  StackItem,
  Toolbar,
  ToolbarContent,
  ToolbarGroup,
  ToolbarItem,
} from '@patternfly/react-core';

import { ComputeInstance, ComputeInstances } from '@osac/types';
import { cel } from '@osac/ui-components/api/cel';
import { useListResource } from '@osac/ui-components/api/use-resource';
import DiskImageFilter from '@osac/ui-components/components/DiskImage/DiskImageFilter';
import ListPage from '@osac/ui-components/components/Page/ListPage';
import ListPageBody from '@osac/ui-components/components/Page/ListPageBody';
import ListPageToolbar from '@osac/ui-components/components/Page/ListPageToolbar';
import ProjectFilter from '@osac/ui-components/components/Page/ProjectFilter';
import CreateButton from '@osac/ui-components/components/Primitives/CreateButton.tsx';
import DebounceSearchInput from '@osac/ui-components/components/Primitives/DebounceSearchInput';
import ViewSwitcher from '@osac/ui-components/components/Primitives/ViewSwitcher';
import {
  buildVmListFilter,
  isVmPowerStateFilter,
} from '@osac/ui-components/components/vm/ListPage/vm-list-filter';
import VmInstanceCard from '@osac/ui-components/components/vm/ListPage/VmInstanceCard';
import VmInstanceTypeFilter from '@osac/ui-components/components/vm/ListPage/VmInstanceTypeFilter';
import VmPowerStateFilter from '@osac/ui-components/components/vm/ListPage/VmPowerStateFilter';
import { VmTable } from '@osac/ui-components/components/vm/ListPage/VmTable';
import { SEARCH_PARAM, usePageFilter } from '@osac/ui-components/hooks/use-page-filter';
import { useProjectFilterQuery } from '@osac/ui-components/hooks/use-project-filter-query';
import { PROJECT_FILTER_PARAM, useSession } from '@osac/ui-components/hooks/use-session';
import { useViewType } from '@osac/ui-components/hooks/use-view-type';
import { useTranslation } from '@osac/ui-components/hooks/useTranslation';

const POWER_STATE_FILTER_PARAM = 'powerState';
const INSTANCE_TYPE_FILTER_PARAM = 'instanceType';
const DISK_IMAGE_FILTER_PARAM = 'diskImage';

const VM_LIST_VIEW_KEY = 'vm-list';

const EMPTY_VMS: ComputeInstance[] = [];

export const VmListPage = () => {
  const { t } = useTranslation();
  const [, setSearchParams] = useSearchParams();
  const { projects, setProjects, role } = useSession();
  const [search, setSearch] = usePageFilter(SEARCH_PARAM);
  const [searchResetKey, setSearchResetKey] = useState(0);
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

  const projectFilter = useProjectFilterQuery<ComputeInstance>();

  const userFilters = useMemo(
    () =>
      buildVmListFilter({
        search,
        powerState: powerStateFilter,
        instanceType: instanceTypeFilter,
        diskImage: diskImageFilter,
      }),
    [diskImageFilter, instanceTypeFilter, powerStateFilter, search],
  );
  const listFilter = useMemo(
    () => cel<ComputeInstance>((builder) => builder.and(projectFilter, userFilters)),
    [projectFilter, userFilters],
  );

  const {
    data: vmResponse,
    isLoading,
    error,
  } = useListResource(ComputeInstances, {
    filter: listFilter,
  });
  const vms = vmResponse?.items ?? EMPTY_VMS;

  const {
    data: totalsResponse,
    isLoading: totalsLoading,
    error: totalsError,
  } = useListResource(ComputeInstances, { limit: 0, filter: projectFilter });
  const totalItems = !totalsLoading && !totalsError ? (totalsResponse?.total ?? 0) : 0;

  const isFiltered = Boolean(
    search || projects.length || powerStateFilter || instanceTypeFilter || diskImageFilter,
  );
  const showEmptyState = !isLoading && !error && vms.length === 0;

  const clearAllFilters = () => {
    setSearchResetKey((key) => key + 1);
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
    totalsLoading || totalsError || vms.length === totalItems
      ? t('{{count}} virtual machine', { count: vms.length })
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
                  <DebounceSearchInput
                    placeholder={t('Search by name')}
                    value={search}
                    onChange={setSearch}
                    resetKey={searchResetKey}
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
                    <VmInstanceCard vm={vm} showTenant={role === 'admin'} />
                  </GalleryItem>
                ))}
              </Gallery>
            ) : (
              <VmTable vms={vms} showTenant={role === 'admin'} />
            )}
          </ListPageBody>
        </StackItem>
      </Stack>
    </ListPage>
  );
};
