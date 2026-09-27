import { useEffect, useMemo, useRef, useState } from 'react';
import {
  MenuToggle,
  Pagination,
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

import { type Volume, VolumeState, Volumes } from '@osac/types';
import { type CelFilter, cel } from '@osac/ui-components/api/cel';
import { useListResource } from '@osac/ui-components/api/use-resource';
import ListPage from '@osac/ui-components/components/Page/ListPage';
import ListPageBody from '@osac/ui-components/components/Page/ListPageBody';
import ProjectFilter from '@osac/ui-components/components/Page/ProjectFilter';
import { VolumeTable } from '@osac/ui-components/components/Volume/VolumeTable';
import { SEARCH_PARAM, usePageFilter } from '@osac/ui-components/hooks/use-page-filter';
import { useProjectFilterQuery } from '@osac/ui-components/hooks/use-project-filter-query';
import { useSession } from '@osac/ui-components/hooks/use-session';
import { useTranslation } from '@osac/ui-components/hooks/useTranslation';

const DEFAULT_PAGE_SIZE = 20;
const REFETCH_INTERVAL_MS = 30_000;

const VALID_VOLUME_STATES: readonly VolumeState[] = [
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
    .filter((n): n is VolumeState => VALID_VOLUME_STATES.includes(n as VolumeState));
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

  const volumeStateOptions = [
    { value: VolumeState.CREATING, label: t('Creating') },
    { value: VolumeState.AVAILABLE, label: t('Available') },
    { value: VolumeState.FAILED, label: t('Failed') },
    { value: VolumeState.DELETING, label: t('Deleting') },
    { value: VolumeState.DELETED, label: t('Deleted') },
  ];

  const [nameSearch, setNameSearch] = usePageFilter(SEARCH_PARAM);
  const [stateFilterRaw, setStateFilterRaw] = usePageFilter('state');
  const [isStateSelectOpen, setIsStateSelectOpen] = useState(false);
  const [page, setPage] = useState(1);
  const [perPage, setPerPage] = useState(DEFAULT_PAGE_SIZE);

  const { projects } = useSession();
  const stateFilters = useMemo(() => parseStateFilter(stateFilterRaw), [stateFilterRaw]);
  const projectFilter = useProjectFilterQuery<Volume>();

  // Reset page to 1 when the project filter changes. Name and state filters
  // already call setPage(1) in their event handlers; the project filter is
  // changed externally via useSession, so we track it here.
  const prevProjectsRef = useRef(projects);
  useEffect(() => {
    if (prevProjectsRef.current !== projects) {
      prevProjectsRef.current = projects;
      setPage(1);
    }
  }, [projects]);

  const filter = useMemo(
    () => buildVolumeCelFilter(projectFilter, stateFilters, nameSearch.trim()),
    [projectFilter, stateFilters, nameSearch],
  );

  const offset = (page - 1) * perPage;

  const { data, isLoading, error } = useListResource(
    Volumes,
    { filter, limit: perPage, offset },
    { refetchInterval: REFETCH_INTERVAL_MS },
  );

  const volumes = data?.items ?? [];
  const totalItems = data?.total ?? 0;

  // Clamp page so it never exceeds the last page after data loads (e.g. when a
  // filter change reduces the result set while the user was on a later page).
  const lastPage = Math.max(1, Math.ceil(totalItems / perPage));
  useEffect(() => {
    if (page > lastPage) {
      setPage(lastPage);
    }
  }, [page, lastPage]);

  const toggleStateFilter = (value: VolumeState) => {
    const current = stateFilters;
    const next = current.includes(value) ? current.filter((v) => v !== value) : [...current, value];
    setStateFilterRaw(next.join(','));
    setPage(1);
  };

  const stateToggleLabel =
    stateFilters.length === 0
      ? t('All states')
      : stateFilters
          .map((v) => {
            const opt = volumeStateOptions.find((o) => o.value === v);
            return opt?.label ?? '';
          })
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
                      {volumeStateOptions.map((opt) => (
                        <SelectOption
                          key={opt.value}
                          value={opt.value}
                          hasCheckbox
                          isSelected={stateFilters.includes(opt.value)}
                        >
                          {opt.label}
                        </SelectOption>
                      ))}
                    </SelectList>
                  </Select>
                </ToolbarItem>
                <ToolbarItem>
                  <SearchInput
                    placeholder={t('Search volumes by name…')}
                    value={nameSearch}
                    onChange={(_event, value) => {
                      setNameSearch(value);
                      setPage(1);
                    }}
                    onClear={() => {
                      setNameSearch('');
                      setPage(1);
                    }}
                    aria-label={t('Filter volumes by name')}
                  />
                </ToolbarItem>
              </ToolbarContent>
            </Toolbar>
          </StackItem>
          <StackItem>
            <VolumeTable volumes={volumes} />
          </StackItem>
          {totalItems > perPage && (
            <StackItem>
              <Pagination
                itemCount={totalItems}
                perPage={perPage}
                page={page}
                onSetPage={(_event, newPage) => setPage(newPage)}
                onPerPageSelect={(_event, newPerPage) => {
                  setPerPage(newPerPage);
                  setPage(1);
                }}
              />
            </StackItem>
          )}
        </Stack>
      </ListPageBody>
    </ListPage>
  );
};
