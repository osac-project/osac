import type { BareMetalInstance, BareMetalInstanceType } from '@osac/types';
import { BareMetalInstanceState } from '@osac/types';
import {
  type BareMetalPowerStateFilter,
  type BareMetalSpecFilters,
  formatBareMetalCpu,
  formatBareMetalGpu,
  formatBareMetalMemory,
  getBareMetalDiskImageName,
  hasBareMetalSpecFilters,
} from '@osac/ui-components/components/BareMetalInstance/bareMetalInstanceDisplay';

import { type CelFilter, cel } from '../cel';

const POWER_STATE_TO_INSTANCE_STATE: Record<BareMetalPowerStateFilter, BareMetalInstanceState> = {
  running: BareMetalInstanceState.RUNNING,
  stopped: BareMetalInstanceState.STOPPED,
  provisioning: BareMetalInstanceState.PROVISIONING,
  restarting: BareMetalInstanceState.STARTING,
  failing: BareMetalInstanceState.FAILED,
};

const EMPTY_SPEC_FILTERS: BareMetalSpecFilters = {
  diskImage: [],
  gpu: [],
  ram: [],
  cpu: [],
};

export type BareMetalListFilterCriteria = {
  search?: string;
  powerState?: BareMetalPowerStateFilter;
  specs?: BareMetalSpecFilters;
};

const unescapeCelString = (value: string): string =>
  value.replaceAll('\\"', '"').replaceAll('\\\\', '\\');

const instanceTypeMatchesSpecFilters = (
  instanceType: BareMetalInstanceType,
  filters: BareMetalSpecFilters,
): boolean => {
  const hardware = instanceType.spec?.hardware;
  const cpuLabel = formatBareMetalCpu(hardware);
  const ramLabel = formatBareMetalMemory(hardware);
  const gpuLabel = formatBareMetalGpu(hardware);

  const matchesCpu =
    filters.cpu.length === 0 || (cpuLabel !== '—' && filters.cpu.includes(cpuLabel));
  const matchesRam =
    filters.ram.length === 0 || (ramLabel !== '—' && filters.ram.includes(ramLabel));
  const matchesGpu =
    filters.gpu.length === 0 || (gpuLabel !== '—' && filters.gpu.includes(gpuLabel));

  return matchesCpu && matchesRam && matchesGpu;
};

const matchingInstanceTypes = (
  instanceTypes: readonly BareMetalInstanceType[],
  filters: BareMetalSpecFilters,
): BareMetalInstanceType[] => {
  const hasHardwareFilters =
    filters.cpu.length > 0 || filters.ram.length > 0 || filters.gpu.length > 0;
  if (!hasHardwareFilters) {
    return [];
  }

  return instanceTypes.filter((instanceType) =>
    instanceTypeMatchesSpecFilters(instanceType, filters),
  );
};

/** Mirrors {@link buildBareMetalListFilter} for unit tests and mock list handlers. */
export const bareMetalInstanceMatchesListFilter = (
  instance: BareMetalInstance,
  filter: string | undefined,
): boolean => {
  if (!filter) {
    return true;
  }

  const searchClauses = [
    ...filter.matchAll(/this\.metadata\.name\.contains\("((?:\\.|[^"\\])*)"\)/g),
    ...filter.matchAll(/this\.id\.contains\("((?:\\.|[^"\\])*)"\)/g),
  ];
  if (searchClauses.length > 0) {
    const haystack = `${instance.metadata?.name ?? ''} ${instance.id}`.toLowerCase();
    const matchesSearch = searchClauses.some((match) =>
      haystack.includes(unescapeCelString(match[1]).toLowerCase()),
    );
    if (!matchesSearch) {
      return false;
    }
  }

  const stateMatch = filter.match(/this\.status\.state == (\d+)/);
  if (stateMatch && instance.status?.state !== Number(stateMatch[1])) {
    return false;
  }

  const diskImageInMatch = filter.match(/this\.spec\.disk_image\.name in \[(.*?)\]/);
  if (diskImageInMatch) {
    const diskImageName = getBareMetalDiskImageName(instance);
    const allowedNames = diskImageInMatch[1]
      .split(',')
      .map((part) => part.trim())
      .filter(Boolean)
      .map((part) => unescapeCelString(part.replace(/^"|"$/g, '')));
    if (!diskImageName || !allowedNames.includes(diskImageName)) {
      return false;
    }
  }

  const instanceTypeIdMatch = filter.match(/this\.spec\.instance_type\.id in \[(.*?)\]/);
  if (instanceTypeIdMatch) {
    const allowedIds = instanceTypeIdMatch[1]
      .split(',')
      .map((part) => part.trim())
      .filter(Boolean)
      .map((part) => unescapeCelString(part.replace(/^"|"$/g, '')));
    const instanceTypeId = instance.spec?.instanceType?.id;
    if (!instanceTypeId || !allowedIds.includes(instanceTypeId)) {
      return false;
    }
  }

  const instanceTypeNameMatch = filter.match(/this\.spec\.instance_type\.name in \[(.*?)\]/);
  if (instanceTypeNameMatch) {
    const allowedNames = instanceTypeNameMatch[1]
      .split(',')
      .map((part) => part.trim())
      .filter(Boolean)
      .map((part) => unescapeCelString(part.replace(/^"|"$/g, '')));
    const instanceTypeName = instance.spec?.instanceType?.name;
    if (!instanceTypeName || !allowedNames.includes(instanceTypeName)) {
      return false;
    }
  }

  if (filter === 'false') {
    return false;
  }

  if (filter.includes('this.metadata.project in')) {
    const projectMatches = [...filter.matchAll(/this\.metadata\.project in \[(.*?)\]/g)].flatMap(
      (match) =>
        match[1]
          .split(',')
          .map((part) => part.trim())
          .filter(Boolean)
          .map((part) => unescapeCelString(part.replace(/^"|"$/g, ''))),
    );
    const project = instance.metadata?.project;
    if (!project || !projectMatches.includes(project)) {
      return false;
    }
  }

  return true;
};

export const buildBareMetalListFilter = (
  criteria: BareMetalListFilterCriteria,
  instanceTypes: readonly BareMetalInstanceType[] = [],
): CelFilter<BareMetalInstance> | undefined => {
  const specs = criteria.specs ?? EMPTY_SPEC_FILTERS;
  const search = criteria.search?.trim();
  const hasCriteria = Boolean(search || criteria.powerState || hasBareMetalSpecFilters(specs));

  if (!hasCriteria) {
    return undefined;
  }

  return cel<BareMetalInstance>((filter) => {
    const clauses: CelFilter<BareMetalInstance>[] = [];

    if (search) {
      clauses.push(
        filter.or(
          filter.field('metadata.name').contains(search),
          filter.field('id').contains(search),
        ),
      );
    }

    if (criteria.powerState) {
      clauses.push(
        filter.field('status.state').equals(POWER_STATE_TO_INSTANCE_STATE[criteria.powerState]),
      );
    }

    if (specs.diskImage.length > 0) {
      clauses.push(filter.field('spec.diskImage.name').isIn(specs.diskImage));
    }

    const hardwareFiltersActive =
      specs.cpu.length > 0 || specs.ram.length > 0 || specs.gpu.length > 0;
    if (hardwareFiltersActive) {
      const matchingTypes = matchingInstanceTypes(instanceTypes, specs);
      const typeIds = matchingTypes.map((instanceType) => instanceType.id).filter(Boolean);
      const typeNames = matchingTypes
        .map((instanceType) => instanceType.metadata?.name)
        .filter((name): name is string => Boolean(name));

      if (typeIds.length === 0 && typeNames.length === 0) {
        clauses.push('false' as CelFilter<BareMetalInstance>);
      } else {
        clauses.push(
          filter.or(
            typeIds.length > 0 ? filter.field('spec.instanceType.id').isIn(typeIds) : undefined,
            typeNames.length > 0
              ? filter.field('spec.instanceType.name').isIn(typeNames)
              : undefined,
          ),
        );
      }
    }

    return filter.and(...clauses);
  });
};
