import { useCallback, useMemo } from 'react';
import { useSearchParams } from 'react-router-dom';
import {
  Button,
  EmptyState,
  EmptyStateBody,
  Flex,
  FlexItem,
  SearchInput,
  Toolbar,
  ToolbarContent,
  ToolbarGroup,
  ToolbarItem,
} from '@patternfly/react-core';

import {
  type BareMetalInstance,
  BareMetalInstanceCatalogItems,
  BareMetalInstanceTypes,
} from '@osac/types';
import { type CelFilter } from '@osac/ui-components/api/cel';
import { useListResource } from '@osac/ui-components/api/use-resource';
import { useBareMetalInstances } from '@osac/ui-components/api/v1/baremetal-instance';
import { useDiskImages } from '@osac/ui-components/api/v1/disk-image';
import PageView from '@osac/ui-components/components/Page/PageView';
import ProjectFilter from '@osac/ui-components/components/Page/ProjectFilter';
import { ViewableListSection } from '@osac/ui-components/components/Page/ViewableListSection';
import CreateButton from '@osac/ui-components/components/Primitives/CreateButton.tsx';
import SpecsFilter, {
  SpecFilterCategory,
} from '@osac/ui-components/components/Primitives/SpecsFilter';
import ViewSwitcher from '@osac/ui-components/components/Primitives/ViewSwitcher';
import {
  SEARCH_PARAM,
  useArrayPageFilter,
  usePageFilter,
} from '@osac/ui-components/hooks/use-page-filter';
import { PROJECT_FILTER_PARAM, useSession } from '@osac/ui-components/hooks/use-session';
import { useTranslation } from '@osac/ui-components/hooks/useTranslation';

import {
  type BareMetalListFilterCriteria,
  buildBareMetalListFilter,
} from './baremetal-instance-list-filter';
import BareMetalInstanceCard from './BareMetalInstanceCard';
import BareMetalPowerStateFilter from './BareMetalPowerStateFilter';
import { BareMetalTable } from './BareMetalTable';
import {
  type BareMetalSpecFilterCategory,
  bareMetalSpecFiltersFromQuery,
  getBareMetalSpecFilterGroupsFromInstanceTypes,
  hasBareMetalHardwareSpecFilters,
  hasBareMetalSpecFilters,
  isBareMetalPowerStateFilter,
  isBareMetalSpecFilterValue,
} from '../bareMetalInstanceDisplay';
import { useBareMetalSshHosts } from '../useBareMetalSshHosts';

const POWER_STATE_FILTER_PARAM = 'powerState';
const DISK_IMAGE_FILTER_PARAM = 'diskImage';
const GPU_FILTER_PARAM = 'gpu';
const RAM_FILTER_PARAM = 'ram';
const CPU_FILTER_PARAM = 'cpu';

const BARE_METAL_LIST_VIEW_KEY = 'bare-metal-list';

export const BareMetalListPage = () => {
  const { t } = useTranslation();
  const [, setSearchParams] = useSearchParams();
  const { projects, setProjects } = useSession();
  const [search, setSearch] = usePageFilter(SEARCH_PARAM);
  const [powerStateFilterParam, setPowerStateFilterParam] = usePageFilter(POWER_STATE_FILTER_PARAM);
  const [diskImageFilterParams, toggleDiskImageFilter] = useArrayPageFilter(
    DISK_IMAGE_FILTER_PARAM,
    isBareMetalSpecFilterValue,
  );
  const [gpuFilterParams, toggleGpuFilter] = useArrayPageFilter(
    GPU_FILTER_PARAM,
    isBareMetalSpecFilterValue,
  );
  const [ramFilterParams, toggleRamFilter] = useArrayPageFilter(
    RAM_FILTER_PARAM,
    isBareMetalSpecFilterValue,
  );
  const [cpuFilterParams, toggleCpuFilter] = useArrayPageFilter(
    CPU_FILTER_PARAM,
    isBareMetalSpecFilterValue,
  );
  const powerStateFilter = isBareMetalPowerStateFilter(powerStateFilterParam)
    ? powerStateFilterParam
    : undefined;

  const {
    data: instanceTypes,
    isLoading: instanceTypesLoading,
    error: instanceTypesError,
  } = useListResource(BareMetalInstanceTypes);
  const {
    data: diskImages = [],
    isLoading: diskImagesLoading,
    error: diskImagesError,
  } = useDiskImages();
  const specOptionsLoading = instanceTypesLoading || diskImagesLoading;
  const specOptionsError = instanceTypesError || diskImagesError;
  const specOptionsReady = !specOptionsLoading && !specOptionsError;

  const { data: catalogItems } = useListResource(BareMetalInstanceCatalogItems);

  const diskImageFilterOptions = useMemo(
    () =>
      diskImages
        .map((diskImage) => diskImage.metadata?.name?.trim())
        .filter((name): name is string => Boolean(name)),
    [diskImages],
  );

  const specFilterGroups = useMemo(
    () =>
      getBareMetalSpecFilterGroupsFromInstanceTypes(instanceTypes?.items, diskImageFilterOptions),
    [diskImageFilterOptions, instanceTypes?.items],
  );

  const specFilters = useMemo(
    () =>
      bareMetalSpecFiltersFromQuery(
        {
          diskImage: diskImageFilterParams,
          gpu: gpuFilterParams,
          ram: ramFilterParams,
          cpu: cpuFilterParams,
        },
        specFilterGroups,
      ),
    [diskImageFilterParams, gpuFilterParams, ramFilterParams, cpuFilterParams, specFilterGroups],
  );

  const filterCriteria = useMemo<BareMetalListFilterCriteria>(
    () => ({
      search,
      powerState: powerStateFilter,
      specs: specFilters,
    }),
    [powerStateFilter, search, specFilters],
  );

  const listFilter = useMemo(
    (): CelFilter<BareMetalInstance> | undefined =>
      buildBareMetalListFilter(filterCriteria, instanceTypes?.items, catalogItems?.items),
    [catalogItems?.items, filterCriteria, instanceTypes?.items],
  );

  const hardwareSpecFiltersActive = hasBareMetalHardwareSpecFilters(specFilters);
  const instancesQueryEnabled = hardwareSpecFiltersActive ? specOptionsReady : true;

  const {
    instances,
    isLoading: instancesLoading,
    error: instancesError,
    totalItems,
  } = useBareMetalInstances(listFilter, instancesQueryEnabled);

  const sshHostsByInstanceId = useBareMetalSshHosts(instances);

  const isLoading =
    instancesLoading || (hardwareSpecFiltersActive && specOptionsLoading && !specOptionsReady);
  const error = instancesError;

  const isFiltered = Boolean(
    search || projects.length || powerStateFilter || hasBareMetalSpecFilters(specFilters),
  );
  const showEmptyState = !isLoading && !error && instances.length === 0;

  const toggleSpecFilter = useCallback(
    (category: BareMetalSpecFilterCategory, value: string) => {
      switch (category) {
        case 'diskImage':
          toggleDiskImageFilter(value);
          break;
        case 'gpu':
          toggleGpuFilter(value);
          break;
        case 'ram':
          toggleRamFilter(value);
          break;
        case 'cpu':
          toggleCpuFilter(value);
          break;
      }
    },
    [toggleCpuFilter, toggleDiskImageFilter, toggleGpuFilter, toggleRamFilter],
  );

  const categories = useMemo(
    (): readonly SpecFilterCategory<BareMetalSpecFilterCategory>[] => [
      { id: 'diskImage', label: t('OS image'), options: specFilterGroups.diskImage },
      { id: 'gpu', label: t('GPU'), options: specFilterGroups.gpu },
      { id: 'ram', label: t('RAM'), options: specFilterGroups.ram },
      { id: 'cpu', label: t('CPU'), options: specFilterGroups.cpu },
    ],
    [specFilterGroups, t],
  );

  const clearAllFilters = () => {
    setProjects([]);
    setSearchParams(
      (prev) => {
        const next = new URLSearchParams(prev);
        next.delete(SEARCH_PARAM);
        next.delete(PROJECT_FILTER_PARAM);
        next.delete(POWER_STATE_FILTER_PARAM);
        next.delete(DISK_IMAGE_FILTER_PARAM);
        next.delete(GPU_FILTER_PARAM);
        next.delete(RAM_FILTER_PARAM);
        next.delete(CPU_FILTER_PARAM);
        return next;
      },
      { replace: true },
    );
  };

  const emptyState =
    totalItems === 0 && !isFiltered ? (
      <EmptyState titleText={t('No bare metal instances found')} headingLevel="h2">
        <EmptyStateBody>{t('No bare metal instances are available yet.')}</EmptyStateBody>
      </EmptyState>
    ) : (
      <EmptyState titleText={t('No bare metal instances match your filters')} headingLevel="h2">
        <EmptyStateBody>
          {t('Try a different project, power state, spec, or search term.')}{' '}
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

  const toolbar = (
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
          <Flex direction={{ default: 'column' }} spaceItems={{ default: 'spaceItemsSm' }}>
            <FlexItem>
              <SpecsFilter<BareMetalSpecFilterCategory>
                categories={categories}
                selected={specFilters}
                onToggle={toggleSpecFilter}
                ariaLabel={t('Filter bare metal by specs')}
                loadError={specOptionsError}
              />
            </FlexItem>
          </Flex>
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
  );

  return (
    <PageView
      title={t('Bare Metal')}
      label={t('Services')}
      description={t('View and manage your bare metal instances.')}
      error={error}
      actions={<CreateButton to="/bare-metal/create">{t('Provision bare metal')}</CreateButton>}
      toolbar={toolbar}
      showEmptyState={showEmptyState}
      emptyState={emptyState}
      filteredText={filteredText}
      isFiltered={isFiltered}
      onClearAllFilters={clearAllFilters}
      pageBody={{ isLoading, error }}
      listSection={
        <ViewableListSection
          items={instances}
          pageKey={BARE_METAL_LIST_VIEW_KEY}
          getItemKey={(instance) => instance.id}
          renderCard={(instance) => (
            <BareMetalInstanceCard
              instance={instance}
              instanceTypes={instanceTypes?.items}
              catalogItems={catalogItems?.items}
              sshHost={sshHostsByInstanceId?.get(instance.id)}
            />
          )}
          renderList={(bareMetalInstances) => (
            <BareMetalTable
              instances={bareMetalInstances}
              instanceTypes={instanceTypes?.items}
              catalogItems={catalogItems?.items}
              sshHostsByInstanceId={sshHostsByInstanceId}
            />
          )}
        />
      }
    />
  );
};
