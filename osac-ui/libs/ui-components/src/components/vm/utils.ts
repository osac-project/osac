import {
  ComputeInstance,
  ComputeInstanceState,
  ExternalIPAttachment,
  ExternalIPAttachmentState,
  InstanceType,
  InstanceTypeState,
} from '@osac/types';
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

export const vmExternalIPStatus = (
  vm: ComputeInstance,
  externalIpAttachment: ExternalIPAttachment | undefined,
  isFetchingExternalIpAttachments = false,
  isLoadingExternalIpAttachments = false,
  externalIpAttachmentsError?: unknown,
) => {
  const state = vm.status?.state;
  const canAttachExternalIp =
    state === ComputeInstanceState.RUNNING && !vm.status?.externalIpAddress;
  const hasAttachedExternalIp = Boolean(vm.status?.externalIpAddress);
  const isAttachingExternalIp =
    externalIpAttachment?.status?.state ===
      ExternalIPAttachmentState.EXTERNAL_IP_ATTACHMENT_STATE_PENDING ||
    (isFetchingExternalIpAttachments && !isLoadingExternalIpAttachments && !hasAttachedExternalIp);
  const isDetachingExternalIp =
    externalIpAttachment?.status?.state ===
    ExternalIPAttachmentState.EXTERNAL_IP_ATTACHMENT_STATE_DELETING;
  const isExternalIpAttachmentFailed =
    externalIpAttachment?.status?.state ===
    ExternalIPAttachmentState.EXTERNAL_IP_ATTACHMENT_STATE_FAILED;
  const showDetachExternalIp =
    Boolean(externalIpAttachment) && (hasAttachedExternalIp || isExternalIpAttachmentFailed);
  const externalIpAttachmentMissing =
    hasAttachedExternalIp &&
    !isLoadingExternalIpAttachments &&
    !externalIpAttachmentsError &&
    !externalIpAttachment;

  return {
    hasAttachedExternalIp,
    externalIpAttachmentMissing,
    isExternalIpAttachmentFailed,
    showDetachExternalIp,
    isAttachingExternalIp,
    isDetachingExternalIp,
    canAttachExternalIp,
  };
};
