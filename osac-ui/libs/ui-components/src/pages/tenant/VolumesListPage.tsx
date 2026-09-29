import { useMemo, useState } from 'react';
import {
  MenuToggle,
  SearchInput,
  Select,
  SelectList,
  SelectOption,
  Stack,
  StackItem,
  Toolbar,
  ToolbarContent,
  ToolbarGroup,
  ToolbarItem,
} from '@patternfly/react-core';
import type { TFunction } from 'i18next';

import { type Volume, VolumeState, Volumes } from '@osac/types';
import { type CelFilter, cel } from '@osac/ui-components/api/cel';
import { useListResource } from '@osac/ui-components/api/use-resource';
import ListPage from '@osac/ui-components/components/Page/ListPage';
import ListPageBody from '@osac/ui-components/components/Page/ListPageBody';
import ProjectFilter from '@osac/ui-components/components/Page/ProjectFilter';
import { VolumeTable } from '@osac/ui-components/components/Volume/VolumeTable';
import { SEARCH_PARAM, usePageFilter } from '@osac/ui-components/hooks/use-page-filter';
import { useProjectFilterQuery } from '@osac/ui-components/hooks/use-project-filter-query';
import { useTranslation } from '@osac/ui-components/hooks/useTranslation';

const REFETCH_INTERVAL_MS = 30_000;

/**
 * Map from every VolumeState to its translated label. Using Record<VolumeState, string>
 * ensures TypeScript flags any missing state when the enum is extended.
 */
const getVolumeStateLabels = (t: TFunction): Record<VolumeState, string> => ({
  [VolumeState.UNSPECIFIED]: t('Unknown'),
  [VolumeState.CREATING]: t('Creating'),
  [VolumeState.AVAILABLE]: t('Available'),
  [VolumeState.FAILED]: t('Failed'),
  [VolumeState.DELETING]: t('Deleting'),
  [VolumeState.DELETED]: t('Deleted'),
});

/** States shown in the filter dropdown — UNSPECIFIED is excluded. */
const FILTERABLE_VOLUME_STATES: readonly VolumeState[] = [
  VolumeState.CREATING,
  VolumeState.AVAILABLE,
  VolumeState.FAILED,
  VolumeState.DELETING,
  VolumeState.DELETED,
];

const parseStateFilter = (raw: string): VolumeState[] => {
  if (!raw) {
    return [];
  }
  return raw
    .split(',')
    .map((s) => Number(s.trim()))
    .filter((n): n is VolumeState => FILTERABLE_VOLUME_STATES.includes(n as VolumeState));
};

const buildVolumeCelFilter = (
  projectFilter: CelFilter<Volume> | undefined,
  stateFilters: VolumeState[],
  nameSearch: string,
): CelFilter<Volume> | undefined =>
  cel<Volume>((b) =>
    b.and(
      projectFilter,
      stateFilters.length > 0 ? b.field('status.state').isIn(stateFilters) : undefined,
      nameSearch ? b.field('metadata.name').contains(nameSearch) : undefined,
    ),
  );

export const VolumesListPage = () => {
  const { t } = useTranslation();

  const stateLabels = useMemo(() => getVolumeStateLabels(t), [t]);

  const [nameSearch, setNameSearch] = usePageFilter(SEARCH_PARAM);
  const [stateFilterRaw, setStateFilterRaw] = usePageFilter('state');
  const [isStateSelectOpen, setIsStateSelectOpen] = useState(false);

  const stateFilters = useMemo(() => parseStateFilter(stateFilterRaw), [stateFilterRaw]);
  const projectFilter = useProjectFilterQuery<Volume>();

  const filter = useMemo(
    () => buildVolumeCelFilter(projectFilter, stateFilters, nameSearch.trim()),
    [projectFilter, stateFilters, nameSearch],
  );

  const { data, isLoading, error } = useListResource(
    Volumes,
    { filter },
    { refetchInterval: REFETCH_INTERVAL_MS },
  );

  const volumes = data?.items ?? [];

  const toggleStateFilter = (value: VolumeState) => {
    const next = stateFilters.includes(value)
      ? stateFilters.filter((v) => v !== value)
      : [...stateFilters, value];
    setStateFilterRaw(next.join(','));
  };

  const stateToggleLabel =
    stateFilters.length === 0
      ? t('All states')
      : stateFilters
          .map((v) => stateLabels[v])
          .filter(Boolean)
          .join(', ');

  return (
    <ListPage
      title={t('Volumes')}
      label={t('Storage')}
      description={t('View and manage your storage volumes.')}
      error={error}
    >
      <ListPageBody isLoading={isLoading} error={error}>
        <Stack hasGutter>
          <StackItem>
            <Toolbar>
              <ToolbarContent>
                <ToolbarGroup>
                  <ToolbarItem>
                    <ProjectFilter />
                  </ToolbarItem>
                </ToolbarGroup>
                <ToolbarItem>
                  <Select
                    isOpen={isStateSelectOpen}
                    onOpenChange={() => setIsStateSelectOpen((o) => !o)}
                    onSelect={(_event, value) => {
                      if (typeof value === 'number') {
                        toggleStateFilter(value as VolumeState);
                      }
                    }}
                    toggle={(toggleRef) => (
                      <MenuToggle
                        ref={toggleRef}
                        onClick={() => setIsStateSelectOpen((o) => !o)}
                        isExpanded={isStateSelectOpen}
                      >
                        {stateToggleLabel}
                      </MenuToggle>
                    )}
                    shouldFocusToggleOnSelect
                  >
                    <SelectList>
                      {FILTERABLE_VOLUME_STATES.map((state) => (
                        <SelectOption
                          key={state}
                          value={state}
                          hasCheckbox
                          isSelected={stateFilters.includes(state)}
                        >
                          {stateLabels[state]}
                        </SelectOption>
                      ))}
                    </SelectList>
                  </Select>
                </ToolbarItem>
                <ToolbarItem>
                  <SearchInput
                    placeholder={t('Search volumes by name…')}
                    value={nameSearch}
                    onChange={(_event, value) => setNameSearch(value)}
                    onClear={() => setNameSearch('')}
                    aria-label={t('Filter volumes by name')}
                  />
                </ToolbarItem>
              </ToolbarContent>
            </Toolbar>
          </StackItem>
          <StackItem>
            <VolumeTable volumes={volumes} />
          </StackItem>
        </Stack>
      </ListPageBody>
    </ListPage>
  );
};
