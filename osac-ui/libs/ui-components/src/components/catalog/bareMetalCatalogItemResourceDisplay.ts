import { TFunction } from 'i18next';

import type {
  BareMetalInstanceCatalogItem,
  BareMetalInstanceCatalogItemFields,
  BareMetalInstanceType,
  BareMetalInstanceTypeLocalReference,
  DiskImage,
  DiskImageReference,
} from '@osac/types';
import { GuestOSFamily } from '@osac/types';

import {
  catalogFieldPolicyIsConfigured,
  catalogResourceHasDisplayValue,
} from './catalogFieldPolicyDisplay';
import type { OsType } from '../shared/GuestOsIcon';

type BareMetalInstanceTypePolicy = NonNullable<BareMetalInstanceCatalogItemFields['instanceType']>;
type BareMetalDiskImagePolicy = NonNullable<BareMetalInstanceCatalogItemFields['diskImage']>;

export const bareMetalInstanceTypeReferenceFromPolicy = (
  policy: BareMetalInstanceTypePolicy | undefined,
): BareMetalInstanceTypeLocalReference | undefined => {
  if (policy?.behavior.case === 'locked') {
    return policy.behavior.value;
  }
  if (policy?.behavior.case === 'editable') {
    return policy.behavior.value.defaultValue;
  }
  return undefined;
};

export const bareMetalDiskImageReferenceFromPolicy = (
  policy: BareMetalDiskImagePolicy | undefined,
): DiskImageReference | undefined => {
  if (policy?.behavior.case === 'locked') {
    return policy.behavior.value;
  }
  if (policy?.behavior.case === 'editable') {
    return policy.behavior.value.defaultValue;
  }
  return undefined;
};

export const findBareMetalInstanceTypeForReference = (
  instanceTypes: BareMetalInstanceType[],
  reference: BareMetalInstanceTypeLocalReference | undefined,
): BareMetalInstanceType | undefined => {
  if (!reference) {
    return undefined;
  }
  if (reference.id) {
    const byId = instanceTypes.find((instanceType) => instanceType.id === reference.id);
    if (byId) {
      return byId;
    }
  }
  if (reference.name) {
    return instanceTypes.find((instanceType) => instanceType.metadata?.name === reference.name);
  }
  return undefined;
};

export const findDiskImageForReference = (
  diskImages: DiskImage[],
  reference: DiskImageReference | undefined,
): DiskImage | undefined => {
  if (!reference) {
    return undefined;
  }
  if (reference.id) {
    const byId = diskImages.find((diskImage) => diskImage.id === reference.id);
    if (byId) {
      return byId;
    }
  }
  if (reference.name) {
    return diskImages.find(
      (diskImage) =>
        diskImage.metadata?.name === reference.name &&
        diskImage.metadata?.project === reference.project,
    );
  }
  return undefined;
};

export const formatBareMetalCatalogCpu = (
  fields: BareMetalInstanceCatalogItemFields | undefined,
  instanceType: BareMetalInstanceType | undefined,
  t: TFunction,
): string | undefined => {
  if (!catalogFieldPolicyIsConfigured(fields?.instanceType)) {
    return undefined;
  }
  const cores = instanceType?.spec?.hardware?.cpu?.cores;
  if (cores === undefined) {
    return undefined;
  }
  return t('{{cores}} vCPU', { cores });
};

export const formatBareMetalCatalogRam = (
  fields: BareMetalInstanceCatalogItemFields | undefined,
  instanceType: BareMetalInstanceType | undefined,
  t: TFunction,
): string | undefined => {
  if (!catalogFieldPolicyIsConfigured(fields?.instanceType)) {
    return undefined;
  }
  const totalGb = instanceType?.spec?.hardware?.memory?.totalGb;
  if (totalGb === undefined) {
    return undefined;
  }
  return t('{{totalGb}} GB', { totalGb });
};

export const formatBareMetalCatalogGpu = (
  fields: BareMetalInstanceCatalogItemFields | undefined,
  instanceType: BareMetalInstanceType | undefined,
  t: TFunction,
): string | undefined => {
  if (!catalogFieldPolicyIsConfigured(fields?.instanceType)) {
    return undefined;
  }
  const accelerator = instanceType?.spec?.hardware?.accelerators[0];
  if (!accelerator) {
    return undefined;
  }
  const parts = [
    accelerator.vendor,
    accelerator.model,
    accelerator.memoryGb === undefined
      ? undefined
      : t('{{memoryGb}} GB', { memoryGb: accelerator.memoryGb }),
  ].filter(Boolean);
  return parts.length > 0 ? parts.join(' ') : '—';
};

export const bareMetalCatalogDiskImageLabel = (
  diskImage: DiskImage | undefined,
  reference: DiskImageReference | undefined,
): string => {
  return diskImage?.metadata?.name || diskImage?.metadata?.displayName || reference?.name || '—';
};

export const diskImageGuestOsIconType = (diskImage: DiskImage | undefined): OsType | undefined => {
  if (!diskImage) {
    return undefined;
  }
  if (diskImage.spec?.guestOsFamily === GuestOSFamily.GUEST_OS_FAMILY_WINDOWS) {
    return 'windows';
  }
  const name = (diskImage.metadata?.name ?? diskImage.metadata?.displayName ?? '').toLowerCase();
  if (name.includes('rhel') || name.includes('red hat')) {
    return 'rhel';
  }
  return 'linux';
};

export const bareMetalCatalogItemFields = (
  catalogItem: BareMetalInstanceCatalogItem,
): BareMetalInstanceCatalogItemFields | undefined => catalogItem.fields;

export const formatBareMetalCatalogDiskImage = (
  fields: BareMetalInstanceCatalogItemFields | undefined,
  diskImage: DiskImage | undefined,
  reference: DiskImageReference | undefined,
): string | undefined => {
  if (!catalogFieldPolicyIsConfigured(fields?.diskImage)) {
    return undefined;
  }
  const label = bareMetalCatalogDiskImageLabel(diskImage, reference);
  if (!catalogResourceHasDisplayValue(label)) {
    return undefined;
  }
  return label;
};
