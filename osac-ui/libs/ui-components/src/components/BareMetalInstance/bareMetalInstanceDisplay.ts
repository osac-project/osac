import type { TFunction } from 'i18next';

import type {
  BareMetalHardwareSpec,
  BareMetalInstance,
  BareMetalInstanceType,
  Metadata,
} from '@osac/types';
import { BareMetalInstanceState } from '@osac/types';

import type { OsType } from '../shared/GuestOsIcon';

export type BareMetalPowerStateFilter =
  | 'running'
  | 'stopped'
  | 'provisioning'
  | 'restarting'
  | 'failing';

export const BARE_METAL_POWER_STATE_FILTERS: readonly BareMetalPowerStateFilter[] = [
  'running',
  'stopped',
  'provisioning',
  'restarting',
  'failing',
];

export const isBareMetalPowerStateFilter = (value: string): value is BareMetalPowerStateFilter =>
  (BARE_METAL_POWER_STATE_FILTERS as readonly string[]).includes(value);

const POWER_STATE_FILTER_TO_INSTANCE_STATE: Record<
  BareMetalPowerStateFilter,
  BareMetalInstanceState
> = {
  running: BareMetalInstanceState.RUNNING,
  stopped: BareMetalInstanceState.STOPPED,
  provisioning: BareMetalInstanceState.PROVISIONING,
  restarting: BareMetalInstanceState.STARTING,
  failing: BareMetalInstanceState.FAILED,
};

export const bareMetalPowerStateFilterLabel = (
  filter: BareMetalPowerStateFilter,
  t: TFunction,
): string => {
  switch (filter) {
    case 'running':
      return t('Running');
    case 'stopped':
      return t('Stopped');
    case 'provisioning':
      return t('Provisioning');
    case 'restarting':
      return t('Restarting');
    case 'failing':
      return t('Failed');
  }
};

export const filterBareMetalInstancesByPowerState = (
  items: BareMetalInstance[],
  powerStateFilter: BareMetalPowerStateFilter | undefined,
): BareMetalInstance[] => {
  if (!powerStateFilter) {
    return items;
  }

  const expectedState = POWER_STATE_FILTER_TO_INSTANCE_STATE[powerStateFilter];
  return items.filter((item) => item.status?.state === expectedState);
};

export type BareMetalSpecFilterCategory = 'diskImage' | 'gpu' | 'ram' | 'cpu';

export interface BareMetalSpecFilterGroups {
  diskImage: string[];
  gpu: string[];
  ram: string[];
  cpu: string[];
}

export interface BareMetalSpecFilters {
  diskImage: string[];
  gpu: string[];
  ram: string[];
  cpu: string[];
}

export const isBareMetalSpecFilterCategory = (
  value: string,
): value is BareMetalSpecFilterCategory =>
  value === 'diskImage' || value === 'gpu' || value === 'ram' || value === 'cpu';

export const isBareMetalSpecFilterValue = (value: string): value is string =>
  value.trim().length > 0;

export const resolveBareMetalSpecFilterValues = (
  values: readonly string[],
  available: readonly string[],
): string[] => {
  const availableSet = new Set(available);
  return values.filter((value) => availableSet.has(value));
};

export const bareMetalSpecFiltersFromQuery = (
  params: BareMetalSpecFilters,
  groups: BareMetalSpecFilterGroups,
): BareMetalSpecFilters => ({
  diskImage: resolveBareMetalSpecFilterValues(params.diskImage, groups.diskImage),
  gpu: resolveBareMetalSpecFilterValues(params.gpu, groups.gpu),
  ram: resolveBareMetalSpecFilterValues(params.ram, groups.ram),
  cpu: resolveBareMetalSpecFilterValues(params.cpu, groups.cpu),
});

export const getBareMetalDiskImageName = (instance: BareMetalInstance): string | undefined => {
  const name = instance.spec?.diskImage?.name?.trim();
  return name || undefined;
};

export const createBareMetalHardwareResolver =
  (instanceTypes: BareMetalInstanceType[] | undefined) =>
  (instance: BareMetalInstance): BareMetalHardwareSpec | undefined => {
    const reference = instance.spec?.instanceType;
    if (!reference || !instanceTypes) {
      return undefined;
    }

    const instanceType = instanceTypes.find(
      (item) => item.id === reference.id || item.metadata?.name === reference.name,
    );

    return instanceType?.spec?.hardware;
  };

const CARD_CREATED_FORMAT = new Intl.DateTimeFormat('en-US', {
  month: 'short',
  day: 'numeric',
  hour: 'numeric',
  minute: '2-digit',
  hour12: true,
});

export const formatBareMetalCpu = (hardware?: BareMetalHardwareSpec): string => {
  const cpu = hardware?.cpu;
  if (!cpu?.cores) {
    return '—';
  }

  const threadsPerCore = cpu.threadsPerCore > 0 ? cpu.threadsPerCore : 1;
  return `${cpu.cores * threadsPerCore} vCPU`;
};

export const formatBareMetalMemory = (hardware?: BareMetalHardwareSpec): string => {
  const memory = hardware?.memory;
  if (!memory?.totalGb) {
    return '—';
  }

  return `${memory.totalGb} GB`;
};

export const formatBareMetalGpu = (hardware?: BareMetalHardwareSpec): string => {
  const accelerator =
    hardware?.accelerators.find((item) => item.type === 'GPU') ?? hardware?.accelerators[0];
  if (!accelerator) {
    return '—';
  }

  return [
    accelerator.vendor,
    accelerator.model,
    accelerator.memoryGb ? `${accelerator.memoryGb} GB` : undefined,
  ]
    .filter(Boolean)
    .join(' ');
};

const sortSpecFilterLabels = (values: Set<string>) =>
  [...values].sort((a, b) => a.localeCompare(b));

export const getBareMetalSpecFilterGroupsFromInstanceTypes = (
  instanceTypes: BareMetalInstanceType[] | undefined,
  diskImageNames: readonly string[] = [],
): BareMetalSpecFilterGroups => {
  const diskImage = new Set(diskImageNames.map((name) => name.trim()).filter(Boolean));
  const gpu = new Set<string>();
  const ram = new Set<string>();
  const cpu = new Set<string>();

  for (const instanceType of instanceTypes ?? []) {
    const hardware = instanceType.spec?.hardware;
    const cpuLabel = formatBareMetalCpu(hardware);
    const ramLabel = formatBareMetalMemory(hardware);
    const gpuLabel = formatBareMetalGpu(hardware);

    if (cpuLabel !== '—') {
      cpu.add(cpuLabel);
    }
    if (ramLabel !== '—') {
      ram.add(ramLabel);
    }
    if (gpuLabel !== '—') {
      gpu.add(gpuLabel);
    }
  }

  return {
    diskImage: sortSpecFilterLabels(diskImage),
    gpu: sortSpecFilterLabels(gpu),
    ram: sortSpecFilterLabels(ram),
    cpu: sortSpecFilterLabels(cpu),
  };
};

export const getBareMetalSpecFilterGroups = (
  instances: BareMetalInstance[],
  resolveHardware: (instance: BareMetalInstance) => BareMetalHardwareSpec | undefined,
): BareMetalSpecFilterGroups => {
  const diskImage = new Set<string>();
  const gpu = new Set<string>();
  const ram = new Set<string>();
  const cpu = new Set<string>();

  for (const instance of instances) {
    const diskImageName = getBareMetalDiskImageName(instance);
    if (diskImageName) {
      diskImage.add(diskImageName);
    }

    const hardware = resolveHardware(instance);
    const cpuLabel = formatBareMetalCpu(hardware);
    const ramLabel = formatBareMetalMemory(hardware);
    const gpuLabel = formatBareMetalGpu(hardware);

    if (cpuLabel !== '—') {
      cpu.add(cpuLabel);
    }
    if (ramLabel !== '—') {
      ram.add(ramLabel);
    }
    if (gpuLabel !== '—') {
      gpu.add(gpuLabel);
    }
  }

  return {
    diskImage: sortSpecFilterLabels(diskImage),
    gpu: sortSpecFilterLabels(gpu),
    ram: sortSpecFilterLabels(ram),
    cpu: sortSpecFilterLabels(cpu),
  };
};

export const hasBareMetalSpecFilters = (filters: BareMetalSpecFilters): boolean =>
  filters.diskImage.length > 0 ||
  filters.gpu.length > 0 ||
  filters.ram.length > 0 ||
  filters.cpu.length > 0;

export const filterBareMetalInstancesBySpecs = (
  items: BareMetalInstance[],
  filters: BareMetalSpecFilters,
  resolveHardware: (instance: BareMetalInstance) => BareMetalHardwareSpec | undefined,
): BareMetalInstance[] => {
  if (!hasBareMetalSpecFilters(filters)) {
    return items;
  }

  return items.filter((instance) => {
    const diskImageName = getBareMetalDiskImageName(instance);
    const hardware = resolveHardware(instance);
    const cpuLabel = formatBareMetalCpu(hardware);
    const ramLabel = formatBareMetalMemory(hardware);
    const gpuLabel = formatBareMetalGpu(hardware);

    const matchesDiskImage =
      filters.diskImage.length === 0 ||
      (diskImageName !== undefined && filters.diskImage.includes(diskImageName));
    const matchesGpu =
      filters.gpu.length === 0 || (gpuLabel !== '—' && filters.gpu.includes(gpuLabel));
    const matchesRam =
      filters.ram.length === 0 || (ramLabel !== '—' && filters.ram.includes(ramLabel));
    const matchesCpu =
      filters.cpu.length === 0 || (cpuLabel !== '—' && filters.cpu.includes(cpuLabel));

    return matchesDiskImage && matchesGpu && matchesRam && matchesCpu;
  });
};

export const inferDiskImageOs = (diskImageName?: string): OsType => {
  const normalized = (diskImageName ?? '').toLowerCase();
  if (normalized.includes('rhel') || normalized.includes('red hat')) {
    return 'rhel';
  }
  if (normalized.includes('windows')) {
    return 'windows';
  }
  return 'linux';
};

export const formatBareMetalCreatedAt = (value?: Metadata): string => {
  const timestamp = value?.creationTimestamp;
  if (!timestamp?.seconds) {
    return '-';
  }

  const date = new Date(
    Number(timestamp.seconds) * 1000 + Math.floor((timestamp.nanos ?? 0) / 1_000_000),
  );
  if (Number.isNaN(date.getTime())) {
    return '-';
  }

  return CARD_CREATED_FORMAT.format(date);
};

export const getBareMetalSshHost = (instance: BareMetalInstance): string | undefined => {
  const statuses = instance.status?.networkAttachmentStatuses ?? [];
  const primary = statuses.find((status) => status.primary) ?? statuses[0];
  const ipAddress = primary?.ipAddress?.trim();
  return ipAddress || undefined;
};

export const buildBareMetalSshCommand = (username: string, host: string): string =>
  `ssh ${username}@${host}`;

export const copyBareMetalSshCommand = async (username: string, host: string): Promise<void> => {
  await navigator.clipboard.writeText(buildBareMetalSshCommand(username, host));
};
