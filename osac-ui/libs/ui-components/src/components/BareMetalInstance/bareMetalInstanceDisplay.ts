import type { TFunction } from 'i18next';

import type {
  BareMetalHardwareSpec,
  BareMetalInstanceType,
  ExternalIPAttachment,
  Metadata,
} from '@osac/types';
import { ExternalIPAttachmentState } from '@osac/types';

import type { OsType } from '../shared/GuestOsIcon';

export type BareMetalPowerStateFilter =
  | 'running'
  | 'stopped'
  | 'provisioning'
  | 'starting'
  | 'failing';

export const BARE_METAL_POWER_STATE_FILTERS: readonly BareMetalPowerStateFilter[] = [
  'running',
  'stopped',
  'provisioning',
  'starting',
  'failing',
];

export const isBareMetalPowerStateFilter = (value: string): value is BareMetalPowerStateFilter =>
  (BARE_METAL_POWER_STATE_FILTERS as readonly string[]).includes(value);

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
    case 'starting':
      return t('Starting');
    case 'failing':
      return t('Failed');
  }
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

export const hasBareMetalSpecFilters = (filters: BareMetalSpecFilters): boolean =>
  filters.diskImage.length > 0 ||
  filters.gpu.length > 0 ||
  filters.ram.length > 0 ||
  filters.cpu.length > 0;

export const hasBareMetalHardwareSpecFilters = (filters: BareMetalSpecFilters): boolean =>
  filters.gpu.length > 0 || filters.ram.length > 0 || filters.cpu.length > 0;

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

export const getHostFromAttachments = (
  attachments: readonly ExternalIPAttachment[],
): string | undefined => {
  const readyWithAddress = attachments.filter((attachment) => {
    const address = attachment.status?.externalIpAddress?.trim();
    return (
      attachment.status?.state === ExternalIPAttachmentState.EXTERNAL_IP_ATTACHMENT_STATE_READY &&
      Boolean(address)
    );
  });
  if (readyWithAddress.length === 0) {
    return undefined;
  }

  const autoCreated = readyWithAddress.find(
    (attachment) => attachment.metadata?.labels?.['osac.openshift.io/auto-created'] === 'true',
  );
  return (autoCreated ?? readyWithAddress[0]).status?.externalIpAddress?.trim();
};

export const buildBareMetalSshHostByInstanceId = (
  attachments: readonly ExternalIPAttachment[],
): ReadonlyMap<string, string> => {
  const attachmentsByInstanceId = new Map<string, ExternalIPAttachment[]>();

  for (const attachment of attachments) {
    const target = attachment.spec?.target;
    if (target?.case !== 'baremetalInstance' || !target.value.id) {
      continue;
    }
    const instanceId = target.value.id;
    const existing = attachmentsByInstanceId.get(instanceId) ?? [];
    existing.push(attachment);
    attachmentsByInstanceId.set(instanceId, existing);
  }

  const hostsByInstanceId = new Map<string, string>();
  for (const [instanceId, instanceAttachments] of attachmentsByInstanceId) {
    const host = getHostFromAttachments(instanceAttachments);
    if (host) {
      hostsByInstanceId.set(instanceId, host);
    }
  }

  return hostsByInstanceId;
};

export const buildBareMetalSshCommand = (username: string, host: string): string =>
  `ssh ${username}@${host}`;
