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
import {
  SEARCH_PARAM,
  useArrayPageFilter,
  usePageFilter,
} from '@osac/ui-components/hooks/use-page-filter';
import { useProjectFilterQuery } from '@osac/ui-components/hooks/use-project-filter-query';
import { useTranslation } from '@osac/ui-components/hooks/useTranslation';

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

type VolumeStateFilterValue = 'creating' | 'available' | 'failed' | 'deleting' | 'deleted';

const STATE_FILTER_PARAM = 'state';

const isVolumeStateFilterValue = (value: string): value is VolumeStateFilterValue =>
  value === 'creating' ||
  value === 'available' ||
  value === 'failed' ||
  value === 'deleting' ||
  value === 'deleted';

const VOLUME_STATE_FILTER_TO_ENUM: Record<VolumeStateFilterValue, VolumeState> = {
  creating: VolumeState.CREATING,
  available: VolumeState.AVAILABLE,
  failed: VolumeState.FAILED,
  deleting: VolumeState.DELETING,
  deleted: VolumeState.DELETED,
};

/** States shown in the filter dropdown — UNSPECIFIED is excluded. */
const FILTERABLE_STATE_OPTIONS: readonly {
  value: VolumeStateFilterValue;
  enumValue: VolumeState;
}[] = [
  { value: 'creating', enumValue: VolumeState.CREATING },
  { value: 'available', enumValue: VolumeState.AVAILABLE },
  { value: 'failed', enumValue: VolumeState.FAILED },
  { value: 'deleting', enumValue: VolumeState.DELETING },
  { value: 'deleted', enumValue: VolumeState.DELETED },
];

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
  const [stateFilters, toggleStateFilter] = useArrayPageFilter(
    STATE_FILTER_PARAM,
    isVolumeStateFilterValue,
  );
  const [isStateSelectOpen, setIsStateSelectOpen] = useState(false);

  const stateEnumValues = useMemo(
    () => stateFilters.map((v) => VOLUME_STATE_FILTER_TO_ENUM[v]),
    [stateFilters],
  );
  const projectFilter = useProjectFilterQuery<Volume>();

  const filter = useMemo(
    () => buildVolumeCelFilter(projectFilter, stateEnumValues, nameSearch.trim()),
    [projectFilter, stateEnumValues, nameSearch],
  );

  const { data, isLoading, error } = useListResource(Volumes, { filter });

  const volumes = data?.items ?? [];

  const stateToggleLabel =
    stateFilters.length === 0
      ? t('All states')
      : stateFilters
          .map((v) => stateLabels[VOLUME_STATE_FILTER_TO_ENUM[v]])
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
                      if (typeof value === 'string' && isVolumeStateFilterValue(value)) {
                        toggleStateFilter(value);
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
                      {FILTERABLE_STATE_OPTIONS.map(({ value, enumValue }) => (
                        <SelectOption
                          key={value}
                          value={value}
                          hasCheckbox
                          isSelected={stateFilters.includes(value)}
                        >
                          {stateLabels[enumValue]}
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
