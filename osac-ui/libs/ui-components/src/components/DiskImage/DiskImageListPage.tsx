import { useState } from 'react';
import {
  Checkbox,
  MenuToggle,
  SearchInput,
  Select,
  SelectList,
  SelectOption,
  Toolbar,
  ToolbarContent,
  ToolbarGroup,
  ToolbarItem,
} from '@patternfly/react-core';

import { Architecture, DiskImageLifecycle, GuestOSFamily } from '@osac/types';
import { Tenants } from '@osac/types/private';
import CreateButton from '@osac/ui-components/components/Primitives/CreateButton.tsx';

import DiskImageTable from './DiskImageTable';
import { useListResource } from '../../api/use-resource';
import { buildDiskImageListFilter, useDiskImages } from '../../api/v1/disk-image';
import { SEARCH_PARAM, useArrayPageFilter, usePageFilter } from '../../hooks/use-page-filter';
import { useTranslation } from '../../hooks/useTranslation';
import ListPage from '../Page/ListPage';
import ListPageBody from '../Page/ListPageBody';

const ALL_OPTION_VALUE = '__all__';

interface FilterOption<T extends string> {
  value: T;
  label: string;
}

interface SingleSelectFilterProps<T extends string> {
  label: string;
  allLabel: string;
  options: FilterOption<T>[];
  selected: T | undefined;
  onChange: (value: T | undefined) => void;
}

const SingleSelectFilter = <T extends string>({
  label,
  allLabel,
  options,
  selected,
  onChange,
}: SingleSelectFilterProps<T>) => {
  const [isOpen, setIsOpen] = useState(false);
  const selectedLabel = options.find((option) => option.value === selected)?.label ?? allLabel;

  return (
    <Select
      isOpen={isOpen}
      onOpenChange={setIsOpen}
      onSelect={(_event, value) => {
        onChange(value === ALL_OPTION_VALUE ? undefined : (value as T));
        setIsOpen(false);
      }}
      toggle={(toggleRef) => (
        <MenuToggle ref={toggleRef} onClick={() => setIsOpen((open) => !open)} isExpanded={isOpen}>
          {`${label}: ${selectedLabel}`}
        </MenuToggle>
      )}
    >
      <SelectList>
        <SelectOption value={ALL_OPTION_VALUE} isSelected={selected === undefined}>
          {allLabel}
        </SelectOption>
        {options.map((option) => (
          <SelectOption
            key={option.value}
            value={option.value}
            isSelected={selected === option.value}
          >
            {option.label}
          </SelectOption>
        ))}
      </SelectList>
    </Select>
  );
};

interface MultiSelectFilterProps<T extends string> {
  label: string;
  options: FilterOption<T>[];
  selected: T[];
  onToggle: (value: T) => void;
}

const MultiSelectFilter = <T extends string>({
  label,
  options,
  selected,
  onToggle,
}: MultiSelectFilterProps<T>) => {
  const [isOpen, setIsOpen] = useState(false);
  const toggleLabel = selected.length ? `${label} (${selected.length})` : label;

  return (
    <Select
      role="menu"
      isOpen={isOpen}
      onOpenChange={setIsOpen}
      onSelect={(_event, value) => {
        if (value === undefined) {
          return;
        }
        onToggle(value as T);
      }}
      toggle={(toggleRef) => (
        <MenuToggle ref={toggleRef} onClick={() => setIsOpen((open) => !open)} isExpanded={isOpen}>
          {toggleLabel}
        </MenuToggle>
      )}
    >
      <SelectList>
        {options.map((option) => (
          <SelectOption
            key={option.value}
            value={option.value}
            hasCheckbox
            isSelected={selected.includes(option.value)}
          >
            {option.label}
          </SelectOption>
        ))}
      </SelectList>
    </Select>
  );
};

type GuestOsFamilyFilterValue = 'linux' | 'windows';
const GUEST_OS_FAMILY_PARAM = 'guestOsFamily';
const isGuestOsFamilyFilterValue = (value: string): value is GuestOsFamilyFilterValue =>
  value === 'linux' || value === 'windows';
const GUEST_OS_FAMILY_FILTER_TO_ENUM: Record<GuestOsFamilyFilterValue, GuestOSFamily> = {
  linux: GuestOSFamily.GUEST_OS_FAMILY_LINUX,
  windows: GuestOSFamily.GUEST_OS_FAMILY_WINDOWS,
};

type ArchitectureFilterValue = 'amd64' | 'arm64' | 's390x';
const ARCHITECTURE_PARAM = 'architecture';
const isArchitectureFilterValue = (value: string): value is ArchitectureFilterValue =>
  value === 'amd64' || value === 'arm64' || value === 's390x';
const ARCHITECTURE_FILTER_TO_ENUM: Record<ArchitectureFilterValue, Architecture> = {
  amd64: Architecture.AMD64,
  arm64: Architecture.ARM64,
  s390x: Architecture.S390X,
};

type LifecycleFilterValue = 'available' | 'deprecated';
const LIFECYCLE_PARAM = 'lifecycle';
const isLifecycleFilterValue = (value: string): value is LifecycleFilterValue =>
  value === 'available' || value === 'deprecated';
const LIFECYCLE_FILTER_TO_ENUM: Record<LifecycleFilterValue, DiskImageLifecycle> = {
  available: DiskImageLifecycle.AVAILABLE,
  deprecated: DiskImageLifecycle.DEPRECATED,
};

type ScopeFilterValue = 'global' | 'tenant';
const SCOPE_PARAM = 'scope';
const isScopeFilterValue = (value: string): value is ScopeFilterValue =>
  value === 'global' || value === 'tenant';

const SHOW_OBSOLETE_PARAM = 'showObsolete';

const DiskImageListPage = () => {
  const { t } = useTranslation();

  const [search, setSearch] = usePageFilter(SEARCH_PARAM);
  const [guestOsFamilyParam, setGuestOsFamilyParam] = usePageFilter(GUEST_OS_FAMILY_PARAM);
  const [architectureFilter, toggleArchitectureFilter] = useArrayPageFilter(
    ARCHITECTURE_PARAM,
    isArchitectureFilterValue,
  );
  const [lifecycleFilter, toggleLifecycleFilter] = useArrayPageFilter(
    LIFECYCLE_PARAM,
    isLifecycleFilterValue,
  );
  const [scopeParam, setScopeParam] = usePageFilter(SCOPE_PARAM);
  const [showObsoleteParam, setShowObsoleteParam] = usePageFilter(SHOW_OBSOLETE_PARAM);

  const guestOsFamily = isGuestOsFamilyFilterValue(guestOsFamilyParam)
    ? GUEST_OS_FAMILY_FILTER_TO_ENUM[guestOsFamilyParam]
    : undefined;
  const architecture = architectureFilter.map((value) => ARCHITECTURE_FILTER_TO_ENUM[value]);
  const lifecycle = lifecycleFilter.map((value) => LIFECYCLE_FILTER_TO_ENUM[value]);
  const scope = isScopeFilterValue(scopeParam) ? scopeParam : undefined;
  const showObsolete = showObsoleteParam === 'true';

  const filter = buildDiskImageListFilter({
    search,
    guestOsFamily,
    architecture,
    lifecycle,
    showObsolete,
    scope,
  });

  const { data: diskImages = [], isLoading, error } = useDiskImages({ filter });
  const { data: tenantsResponse } = useListResource(Tenants);
  const tenants = tenantsResponse?.items ?? [];

  const guestOsFamilyOptions: FilterOption<GuestOsFamilyFilterValue>[] = [
    { value: 'linux', label: t('Linux') },
    { value: 'windows', label: t('Windows') },
  ];
  const architectureOptions: FilterOption<ArchitectureFilterValue>[] = [
    { value: 'amd64', label: 'amd64' },
    { value: 'arm64', label: 'arm64' },
    { value: 's390x', label: 's390x' },
  ];
  const lifecycleOptions: FilterOption<LifecycleFilterValue>[] = [
    { value: 'available', label: t('Available') },
    { value: 'deprecated', label: t('Deprecated') },
  ];
  const scopeOptions: FilterOption<ScopeFilterValue>[] = [
    { value: 'global', label: t('Global') },
    { value: 'tenant', label: t('Tenant') },
  ];

  return (
    <ListPage
      title={t('Disk images')}
      description={t('Manage disk images available for provisioning virtual machines.')}
      error={error}
      actions={
        <CreateButton to="/admin/infrastructure/disk-images/create">
          {t('Create disk image')}
        </CreateButton>
      }
    >
      <ListPageBody isLoading={isLoading} error={error}>
        <Toolbar>
          <ToolbarContent>
            <ToolbarGroup>
              <ToolbarItem>
                <SearchInput
                  placeholder={t('Search disk images by name…')}
                  value={search}
                  onChange={(_event, value) => setSearch(value)}
                  onClear={() => setSearch('')}
                  aria-label={t('Search disk images')}
                />
              </ToolbarItem>
              <ToolbarItem>
                <SingleSelectFilter
                  label={t('Guest OS family')}
                  allLabel={t('All guest OS families')}
                  options={guestOsFamilyOptions}
                  selected={
                    isGuestOsFamilyFilterValue(guestOsFamilyParam) ? guestOsFamilyParam : undefined
                  }
                  onChange={(value) => setGuestOsFamilyParam(value ?? '')}
                />
              </ToolbarItem>
              <ToolbarItem>
                <MultiSelectFilter
                  label={t('Architecture')}
                  options={architectureOptions}
                  selected={architectureFilter}
                  onToggle={toggleArchitectureFilter}
                />
              </ToolbarItem>
              <ToolbarItem>
                <MultiSelectFilter
                  label={t('Lifecycle')}
                  options={lifecycleOptions}
                  selected={lifecycleFilter}
                  onToggle={toggleLifecycleFilter}
                />
              </ToolbarItem>
              <ToolbarItem>
                <SingleSelectFilter
                  label={t('Scope')}
                  allLabel={t('All scopes')}
                  options={scopeOptions}
                  selected={scope}
                  onChange={(value) => setScopeParam(value ?? '')}
                />
              </ToolbarItem>
              <ToolbarItem>
                <Checkbox
                  id="disk-image-show-obsolete"
                  label={t('Show obsolete')}
                  isChecked={showObsolete}
                  onChange={(_event, checked) => setShowObsoleteParam(checked ? 'true' : '')}
                />
              </ToolbarItem>
            </ToolbarGroup>
          </ToolbarContent>
        </Toolbar>
        <DiskImageTable diskImages={diskImages} tenants={tenants} />
      </ListPageBody>
    </ListPage>
  );
};

export default DiskImageListPage;
