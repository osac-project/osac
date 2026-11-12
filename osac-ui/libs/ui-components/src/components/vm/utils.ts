import { InstanceType, InstanceTypeState } from '@osac/types';
import { resourceDisplayName } from '@osac/ui-components/api/v1/networking';

export interface GpuSpecFields {
  count?: number;
  resourceName?: string;
  pciDeviceSelector?: string;
}

export const isObsoleteInstanceType = (instanceType: InstanceType): boolean =>
  instanceType.spec?.state === InstanceTypeState.OBSOLETE;

export const isDeprecatedInstanceType = (instanceType: InstanceType): boolean =>
  instanceType.spec?.state === InstanceTypeState.DEPRECATED;

export const instanceTypeName = (instanceType: InstanceType): string =>
  resourceDisplayName(instanceType.metadata, instanceType.id);

/** Match fulfillment resize validation: full GpuSpec equality (proto.Equal). */
export const areGpuSpecsEqual = (
  left: GpuSpecFields | undefined,
  right: GpuSpecFields | undefined,
): boolean => {
  if (!left && !right) {
    return true;
  }
  if (!left || !right) {
    return false;
  }
  return (
    left.count === right.count &&
    (left.resourceName ?? '') === (right.resourceName ?? '') &&
    (left.pciDeviceSelector ?? '') === (right.pciDeviceSelector ?? '')
  );
};

export const isGpuCompatibleInstanceType = (
  current: InstanceType | undefined,
  candidate: InstanceType,
): boolean => areGpuSpecsEqual(current?.spec?.gpu, candidate.spec?.gpu);

const formatGpuSizing = (gpu: GpuSpecFields): string => {
  const count = gpu.count ?? 0;
  const gpuLabel = count === 1 ? '1 GPU' : `${count} GPUs`;
  if (!gpu.resourceName) {
    return gpuLabel;
  }
  return `${gpuLabel} · ${gpu.resourceName}`;
};

export const formatInstanceTypeSizing = (instanceType: InstanceType): string => {
  const vCpus = instanceType.spec?.vcpus;
  const memoryGib = instanceType.spec?.memoryGib;
  if (vCpus == null || memoryGib == null) {
    return '—';
  }
  const sizing = `${vCpus} vCPUs · ${memoryGib} GiB`;
  const gpu = instanceType.spec?.gpu;
  if (!gpu) {
    return sizing;
  }
  return `${sizing} · ${formatGpuSizing(gpu)}`;
};

export const formatInstanceTypeOptionLabel = (
  instanceType: InstanceType,
  deprecatedSuffix = ' (deprecated)',
): string => {
  const name = instanceTypeName(instanceType);
  const sizing = formatInstanceTypeSizing(instanceType);
  const deprecated = isDeprecatedInstanceType(instanceType) ? deprecatedSuffix : '';
  return `${name} — ${sizing}${deprecated}`;
};

export const formatInstanceTypeDisplayName = (
  instanceType: InstanceType | undefined,
  deprecatedSuffix = ' (deprecated)',
  fallbackId?: string,
): string => {
  if (!instanceType) {
    return fallbackId?.trim() || '—';
  }
  const name = instanceTypeName(instanceType);
  const deprecated = isDeprecatedInstanceType(instanceType) ? deprecatedSuffix : '';
  return `${name}${deprecated}`;
};

export const formatInstanceTypeReviewLabelFromType = (
  instanceType: InstanceType | undefined,
  deprecatedSuffix = ' (deprecated)',
  fallbackId?: string,
): string => {
  if (!instanceType) {
    return fallbackId?.trim() || '—';
  }
  return formatInstanceTypeOptionLabel(instanceType, deprecatedSuffix);
};
