import { useMemo } from 'react';
import {
  Alert,
  Flex,
  FlexItem,
  Label,
  SearchInput,
  Stack,
  StackItem,
  ToggleGroup,
  ToggleGroupItem,
  Toolbar,
  ToolbarContent,
  ToolbarGroup,
  ToolbarItem,
} from '@patternfly/react-core';

import { ComputeInstance, ComputeInstanceState } from '@osac/types';
import { useComputeInstances } from '@osac/ui-components/api/v1/compute-instance';
import { useInstanceTypes } from '@osac/ui-components/api/v1/instance-types';
import ListPage from '@osac/ui-components/components/Page/ListPage';
import ListPageBody from '@osac/ui-components/components/Page/ListPageBody';
import ProjectFilter from '@osac/ui-components/components/Page/ProjectFilter';
import CreateButton from '@osac/ui-components/components/Primitives/CreateButton.tsx';
import { SubtleContent } from '@osac/ui-components/components/SubtleContent/SubtleContent';
import { VmTable } from '@osac/ui-components/components/vm/VmTable';
import {
  SEARCH_PARAM,
  useArrayPageFilter,
  usePageFilter,
} from '@osac/ui-components/hooks/use-page-filter';
import { useTranslation } from '@osac/ui-components/hooks/useTranslation';
import { getErrorMessage } from '@osac/ui-components/utils/error';

type VmStatusFilter = 'running' | 'stopped';

const STATUS_FILTER_PARAM = 'status';

const isVmStatusFilter = (value: string): value is VmStatusFilter =>
  value === 'running' || value === 'stopped';

const vmMatchesStatusFilter = (vm: ComputeInstance, filter: VmStatusFilter): boolean => {
  const state = vm.status?.state;
  if (filter === 'running') {
    return state === ComputeInstanceState.RUNNING;
  }
  return state === ComputeInstanceState.STOPPED;
};

export const VmListPage = () => {
  const { t } = useTranslation();
  const [search, setSearch] = usePageFilter(SEARCH_PARAM);
  const [statusFilters, setStatusFilters] = useArrayPageFilter(
    STATUS_FILTER_PARAM,
    isVmStatusFilter,
  );

  const { data: vms = [], isLoading, error } = useComputeInstances();
  const {
    data: instanceTypes = [],
    isLoading: isInstanceTypesLoading,
    error: instanceTypesError,
  } = useInstanceTypes();

  const statusFilterOptions = useMemo<ReadonlyArray<{ value: VmStatusFilter; label: string }>>(
    () => [
      { value: 'running', label: t('Running') },
      { value: 'stopped', label: t('Stopped') },
    ],
    [t],
  );

  const statusCounts = useMemo(
    () => ({
      running: vms.filter((vm) => vmMatchesStatusFilter(vm, 'running')).length,
      stopped: vms.filter((vm) => vmMatchesStatusFilter(vm, 'stopped')).length,
    }),
    [vms],
  );

  const filteredVms = useMemo(() => {
    const searchTerm = search.trim().toLowerCase();
    if (statusFilters.length === 0 && !searchTerm) {
      return vms;
    }
    return vms.filter((vm) => {
      const name = vm.metadata?.name ?? '';
      const matchesSearch = !searchTerm || name.toLowerCase().includes(searchTerm);
      const matchesStatus =
        !statusFilters.length || statusFilters.some((filter) => vmMatchesStatusFilter(vm, filter));
      return matchesSearch && matchesStatus;
    });
  }, [search, statusFilters, vms]);

  const showEmptyState = !isLoading && !error && filteredVms.length === 0;

  return (
    <ListPage
      title={t('Virtual machines')}
      description={t('View and filter your virtual machines.')}
      error={error}
      actions={<CreateButton to="/vms/create">{t('Create virtual machine')}</CreateButton>}
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
                  <ToggleGroup aria-label={t('Filter virtual machines by status')}>
                    {statusFilterOptions.map((option) => (
                      <ToggleGroupItem
                        key={option.value}
                        text={
                          <Flex
                            spaceItems={{ default: 'spaceItemsSm' }}
                            flexWrap={{ default: 'nowrap' }}
                          >
                            <FlexItem>{option.label}</FlexItem>
                            <FlexItem>
                              <Label isCompact>{statusCounts[option.value]}</Label>
                            </FlexItem>
                          </Flex>
                        }
                        buttonId={`vm-filter-status-${option.value}`}
                        isSelected={statusFilters.includes(option.value)}
                        onChange={() => setStatusFilters(option.value)}
                      />
                    ))}
                  </ToggleGroup>
                </ToolbarItem>
                <ToolbarItem>
                  <SearchInput
                    placeholder={t('Search VMs by name…')}
                    value={search}
                    onChange={(_event, value) => setSearch(value)}
                    onClear={() => setSearch('')}
                    aria-label={t('Filter virtual machines by name')}
                    isDisabled={isLoading || !!error}
                  />
                </ToolbarItem>
              </ToolbarContent>
            </Toolbar>
          </StackItem>
          {instanceTypesError ? (
            <StackItem>
              <Alert variant="danger" title={t('Could not load instance types')} isInline>
                {getErrorMessage(instanceTypesError)}
              </Alert>
            </StackItem>
          ) : null}
          <StackItem>
            {showEmptyState ? (
              <SubtleContent component="p">
                {statusFilters.length === 0
                  ? t('Choose one or more statuses above to filter virtual machines.')
                  : vms?.length
                    ? t('No virtual machines match your filters.')
                    : t('No virtual machines yet. Create one to get started.')}
              </SubtleContent>
            ) : (
              <VmTable
                vms={filteredVms}
                instanceTypes={instanceTypes}
                isInstanceTypesLoading={isInstanceTypesLoading}
              />
            )}
          </StackItem>
        </Stack>
      </ListPageBody>
    </ListPage>
  );
};
