import type {
  BareMetalInstance,
  BareMetalInstanceCatalogItem,
  BareMetalInstanceType,
} from '@osac/types';
import { BareMetalInstanceState } from '@osac/types';
import { type CelBuilder, type CelFilter, cel } from '@osac/ui-components/api/cel';

import {
  bareMetalInstanceTypeReferenceFromPolicy,
  findBareMetalInstanceTypeForReference,
} from '../../catalog/bareMetalCatalogItemResourceDisplay';
import {
  type BareMetalPowerStateFilter,
  type BareMetalSpecFilters,
  formatBareMetalCpu,
  formatBareMetalGpu,
  formatBareMetalMemory,
  hasBareMetalSpecFilters,
} from '../bareMetalInstanceDisplay';

const POWER_STATE_TO_INSTANCE_STATE: Record<BareMetalPowerStateFilter, BareMetalInstanceState> = {
  running: BareMetalInstanceState.RUNNING,
  stopped: BareMetalInstanceState.STOPPED,
  provisioning: BareMetalInstanceState.PROVISIONING,
  starting: BareMetalInstanceState.STARTING,
  failing: BareMetalInstanceState.FAILED,
};

const EMPTY_SPEC_FILTERS: BareMetalSpecFilters = {
  diskImage: [],
  gpu: [],
  ram: [],
  cpu: [],
};

const ABSENT_INSTANCE_TYPE_CLAUSE = '!has(this.spec.instance_type)' as CelFilter<BareMetalInstance>;

export type BareMetalListFilterCriteria = {
  search?: string;
  powerState?: BareMetalPowerStateFilter;
  specs?: BareMetalSpecFilters;
};

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

const matchingCatalogItems = (
  catalogItems: readonly BareMetalInstanceCatalogItem[],
  instanceTypes: readonly BareMetalInstanceType[],
  filters: BareMetalSpecFilters,
): BareMetalInstanceCatalogItem[] => {
  const hasHardwareFilters =
    filters.cpu.length > 0 || filters.ram.length > 0 || filters.gpu.length > 0;
  if (!hasHardwareFilters) {
    return [];
  }

  return catalogItems.filter((catalogItem) => {
    const reference = bareMetalInstanceTypeReferenceFromPolicy(catalogItem.fields?.instanceType);
    const instanceType = findBareMetalInstanceTypeForReference([...instanceTypes], reference);
    return instanceType !== undefined && instanceTypeMatchesSpecFilters(instanceType, filters);
  });
};

const catalogItemHardwareMatchClause = (
  filter: CelBuilder<BareMetalInstance>,
  catalogItemIds: string[],
  catalogItemNames: string[],
): CelFilter<BareMetalInstance> | undefined => {
  if (catalogItemIds.length === 0 && catalogItemNames.length === 0) {
    return undefined;
  }

  const catalogMatch = filter.and(
    ABSENT_INSTANCE_TYPE_CLAUSE,
    filter.or(
      catalogItemIds.length > 0
        ? filter.field('spec.catalogItem.id').isIn(catalogItemIds)
        : undefined,
      catalogItemNames.length > 0
        ? filter.field('spec.catalogItem.name').isIn(catalogItemNames)
        : undefined,
    ),
  );

  const grouped = filter.group(catalogMatch);
  return grouped === '' ? undefined : grouped;
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

export const buildBareMetalListFilter = (
  criteria: BareMetalListFilterCriteria,
  instanceTypes: readonly BareMetalInstanceType[] = [],
  catalogItems: readonly BareMetalInstanceCatalogItem[] = [],
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

      const matchingCatalog = matchingCatalogItems(catalogItems, instanceTypes, specs);
      const catalogItemIds = matchingCatalog.map((catalogItem) => catalogItem.id).filter(Boolean);
      const catalogItemNames = matchingCatalog
        .map((catalogItem) => catalogItem.metadata?.name)
        .filter((name): name is string => Boolean(name));

      const hasHardwareMatch =
        typeIds.length > 0 ||
        typeNames.length > 0 ||
        catalogItemIds.length > 0 ||
        catalogItemNames.length > 0;

      if (!hasHardwareMatch) {
        clauses.push('false' as CelFilter<BareMetalInstance>);
      } else {
        clauses.push(
          filter.or(
            typeIds.length > 0 ? filter.field('spec.instanceType.id').isIn(typeIds) : undefined,
            typeNames.length > 0
              ? filter.field('spec.instanceType.name').isIn(typeNames)
              : undefined,
            catalogItemHardwareMatchClause(filter, catalogItemIds, catalogItemNames),
          ),
        );
      }
    }

    return filter.and(...clauses);
  });
};
