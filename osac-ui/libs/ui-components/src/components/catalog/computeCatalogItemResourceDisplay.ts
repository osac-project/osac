import type { TFunction } from 'i18next';

import type {
  ComputeInstanceCatalogItem,
  ComputeInstanceCatalogItemFields,
  DiskImage,
  DiskImageReference,
  InstanceType,
  InstanceTypeReference,
} from '@osac/types';

import { bareMetalCatalogDiskImageLabel } from './bareMetalCatalogItemResourceDisplay';
import {
  catalogFieldPolicyIsConfigured,
  catalogResourceHasDisplayValue,
} from './catalogFieldPolicyDisplay';

type BootDiskSizePolicy = NonNullable<
  NonNullable<ComputeInstanceCatalogItemFields['bootDisk']>['sizeGib']
>;

type ComputeInstanceTypePolicy = NonNullable<ComputeInstanceCatalogItemFields['instanceType']>;
type ComputeDiskImagePolicy = NonNullable<ComputeInstanceCatalogItemFields['diskImage']>;

export const computeCatalogItemFields = (
  catalogItem: ComputeInstanceCatalogItem,
): ComputeInstanceCatalogItemFields | undefined => catalogItem.fields;

export const int32ValueFromPolicy = (
  policy: BootDiskSizePolicy | undefined,
): number | undefined => {
  if (policy?.behavior.case === 'locked') {
    return policy.behavior.value;
  }
  if (policy?.behavior.case === 'editable') {
    return policy.behavior.value.defaultValue;
  }
  return undefined;
};

export const computeInstanceTypeReferenceFromPolicy = (
  policy: ComputeInstanceTypePolicy | undefined,
): InstanceTypeReference | undefined => {
  if (policy?.behavior.case === 'locked') {
    return policy.behavior.value;
  }
  if (policy?.behavior.case === 'editable') {
    return policy.behavior.value.defaultValue;
  }
  return undefined;
};

export const computeDiskImageReferenceFromPolicy = (policy: ComputeDiskImagePolicy | undefined) => {
  if (policy?.behavior.case === 'locked') {
    return policy.behavior.value;
  }
  if (policy?.behavior.case === 'editable') {
    return policy.behavior.value.defaultValue;
  }
  return undefined;
};

export const findComputeInstanceTypeForReference = (
  instanceTypes: InstanceType[],
  reference: InstanceTypeReference | undefined,
): InstanceType | undefined => {
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
    return instanceTypes.find(
      (instanceType) =>
        instanceType.metadata?.name === reference.name &&
        instanceType.metadata?.project === reference.project,
    );
  }
  return undefined;
};

export const formatComputeCatalogVCpu = (
  fields: ComputeInstanceCatalogItemFields | undefined,
  instanceType: InstanceType | undefined,
  t: TFunction,
): string | undefined => {
  if (!catalogFieldPolicyIsConfigured(fields?.instanceType)) {
    return undefined;
  }
  const vcpus = instanceType?.spec?.vcpus;
  if (vcpus === undefined) {
    return undefined;
  }
  return t('{{vcpus}} vCPU', { vcpus });
};

export const formatComputeCatalogMemory = (
  fields: ComputeInstanceCatalogItemFields | undefined,
  instanceType: InstanceType | undefined,
  t: TFunction,
): string | undefined => {
  if (!catalogFieldPolicyIsConfigured(fields?.instanceType)) {
    return undefined;
  }
  const memoryGib = instanceType?.spec?.memoryGib;
  if (memoryGib === undefined) {
    return undefined;
  }
  return t('{{memoryGib}} GiB', { memoryGib });
};

export const formatComputeCatalogStorage = (
  fields: ComputeInstanceCatalogItemFields | undefined,
  sizeGib: number | undefined,
  t: TFunction,
): string | undefined => {
  if (!catalogFieldPolicyIsConfigured(fields?.bootDisk?.sizeGib)) {
    return undefined;
  }
  if (sizeGib === undefined) {
    return undefined;
  }
  return t('{{sizeGib}} GiB', { sizeGib });
};

export const formatComputeCatalogDiskImage = (
  fields: ComputeInstanceCatalogItemFields | undefined,
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
