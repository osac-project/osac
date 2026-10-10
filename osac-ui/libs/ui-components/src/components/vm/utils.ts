import { InstanceType, InstanceTypeState } from '@osac/types';
import { resourceDisplayName } from '@osac/ui-components/api/v1/networking';

export const isObsoleteInstanceType = (instanceType: InstanceType): boolean =>
  instanceType.spec?.state === InstanceTypeState.OBSOLETE;

export const isDeprecatedInstanceType = (instanceType: InstanceType): boolean =>
  instanceType.spec?.state === InstanceTypeState.DEPRECATED;

export const instanceTypeName = (instanceType: InstanceType): string =>
  resourceDisplayName(instanceType.metadata, instanceType.id);

export const formatInstanceTypeSizing = (instanceType: InstanceType): string => {
  const vCpus = instanceType.spec?.vcpus;
  const memoryGib = instanceType.spec?.memoryGib;
  if (vCpus == null || memoryGib == null) {
    return '—';
  }
  return `${vCpus} vCPUs, ${memoryGib} GiB`;
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
