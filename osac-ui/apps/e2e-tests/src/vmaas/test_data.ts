import { test } from '@playwright/test';
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { parse } from 'yaml';

import type { DiskImageCreateParams } from '../../../../libs/test-utils/src/disk_images.page';
import type { InstanceTypeGpu } from '../../../../libs/test-utils/src/instance_types.page';
import {
  type LifecycleAction,
  lifecycleActionOrder,
} from '../../../../libs/test-utils/src/lifecycle';

interface RawInstanceTypeGpu {
  count: number;
  resource_name: string;
  pci_device_selector: string;
}

interface RawInstanceType {
  name: string;
  description: string;
  vcpus: number;
  memory_gib: number;
  gpu?: RawInstanceTypeGpu;
}

interface RawDiskImage {
  name: string;
  source_type: 'Registry';
  source_ref: string;
  guest_os_family: string;
  architectures: [string, ...string[]];
  scope: string;
}

interface RawExtravars {
  instance_type: RawInstanceType;
  disk_image: RawDiskImage;
  lifecycle_actions: LifecycleAction[];
  tenant_name?: string;
}

export interface InstanceTypeTestData {
  name: string;
  description: string;
  vcpus: number;
  memoryGib: number;
  gpu?: InstanceTypeGpu;
  lifecycleActions: LifecycleAction[];
}

export interface DiskImageTestData extends DiskImageCreateParams {
  lifecycleActions: LifecycleAction[];
}

const expectedLifecycleActions: readonly LifecycleAction[] = lifecycleActionOrder;

const asRecord = (value: unknown, field: string): Record<string, unknown> => {
  if (typeof value !== 'object' || value === null || Array.isArray(value)) {
    throw new Error(`Invalid e2e extravars: ${field} must be an object.`);
  }

  return value as Record<string, unknown>;
};

const requiredString = (value: unknown, field: string): string => {
  if (typeof value !== 'string' || value.trim().length === 0) {
    throw new Error(`Invalid e2e extravars: ${field} must be a non-empty string.`);
  }

  return value;
};

const registrySourceType = (value: unknown): 'Registry' => {
  const sourceType = requiredString(value, 'disk_image.source_type');
  if (sourceType !== 'Registry') {
    throw new Error('Invalid e2e extravars: disk_image.source_type must be Registry.');
  }

  return sourceType;
};

const optionalString = (value: unknown, field: string): string | undefined => {
  if (value === undefined) {
    return undefined;
  }

  if (typeof value !== 'string') {
    throw new Error(`Invalid e2e extravars: ${field} must be a string.`);
  }

  return value.trim() || undefined;
};

const positiveInteger = (value: unknown, field: string): number => {
  if (typeof value !== 'number' || !Number.isInteger(value) || value < 1) {
    throw new Error(`Invalid e2e extravars: ${field} must be a positive integer.`);
  }

  return value;
};

const stringTuple = (value: unknown, field: string): [string, ...string[]] => {
  if (!Array.isArray(value) || value.length === 0) {
    throw new Error(`Invalid e2e extravars: ${field} must contain at least one value.`);
  }

  return value.map((item, index) => requiredString(item, `${field}[${index}]`)) as [
    string,
    ...string[],
  ];
};

const lifecycleActions = (value: unknown): LifecycleAction[] => {
  if (
    !Array.isArray(value) ||
    value.length !== expectedLifecycleActions.length ||
    value.some((action, index) => action !== expectedLifecycleActions[index])
  ) {
    throw new Error(
      `Invalid e2e extravars: lifecycle_actions must be ${expectedLifecycleActions.join(', ')} in that order.`,
    );
  }

  return [...expectedLifecycleActions];
};

const parseExtravars = (value: unknown): RawExtravars => {
  const root = asRecord(value, 'root');
  const instanceType = asRecord(root.instance_type, 'instance_type');
  const diskImage = asRecord(root.disk_image, 'disk_image');
  const gpu =
    instanceType.gpu === undefined ? undefined : asRecord(instanceType.gpu, 'instance_type.gpu');

  return {
    tenant_name: optionalString(root.tenant_name, 'tenant_name'),
    instance_type: {
      name: requiredString(instanceType.name, 'instance_type.name'),
      description: requiredString(instanceType.description, 'instance_type.description'),
      vcpus: positiveInteger(instanceType.vcpus, 'instance_type.vcpus'),
      memory_gib: positiveInteger(instanceType.memory_gib, 'instance_type.memory_gib'),
      ...(gpu
        ? {
            gpu: {
              count: positiveInteger(gpu.count, 'instance_type.gpu.count'),
              resource_name: requiredString(gpu.resource_name, 'instance_type.gpu.resource_name'),
              pci_device_selector: requiredString(
                gpu.pci_device_selector,
                'instance_type.gpu.pci_device_selector',
              ),
            },
          }
        : {}),
    },
    disk_image: {
      name: requiredString(diskImage.name, 'disk_image.name'),
      source_type: registrySourceType(diskImage.source_type),
      source_ref: requiredString(diskImage.source_ref, 'disk_image.source_ref'),
      guest_os_family: requiredString(diskImage.guest_os_family, 'disk_image.guest_os_family'),
      architectures: stringTuple(diskImage.architectures, 'disk_image.architectures'),
      scope: requiredString(diskImage.scope, 'disk_image.scope'),
    },
    lifecycle_actions: lifecycleActions(root.lifecycle_actions),
  };
};

const extravarsPath = path.resolve(
  path.dirname(fileURLToPath(import.meta.url)),
  '../../extravars.yaml',
);

const {
  instance_type: rawInstanceType,
  disk_image: rawDiskImage,
  lifecycle_actions: rawLifecycleActions,
  tenant_name: rawTenantName,
} = parseExtravars(parse(readFileSync(extravarsPath, 'utf8')));
const diskImageTestRunId = requiredString(
  process.env.OSAC_DISK_IMAGE_TEST_RUN_ID,
  'OSAC_DISK_IMAGE_TEST_RUN_ID',
);

export const tenantName: string | undefined = rawTenantName;

export const tenantNameOrSkip = (): string | undefined => {
  if (tenantName !== undefined) {
    return tenantName;
  }

  test.skip(true, 'tenant_name is not set in apps/e2e-tests/extravars.yaml.');
  return undefined;
};

export const instanceTypeTestData: InstanceTypeTestData = {
  name: rawInstanceType.name,
  description: rawInstanceType.description,
  vcpus: rawInstanceType.vcpus,
  memoryGib: rawInstanceType.memory_gib,
  gpu: rawInstanceType.gpu
    ? {
        count: rawInstanceType.gpu.count,
        resourceName: rawInstanceType.gpu.resource_name,
        pciDeviceSelector: rawInstanceType.gpu.pci_device_selector,
      }
    : undefined,
  lifecycleActions: rawLifecycleActions,
};

export const diskImagesTestData: DiskImageTestData = {
  name: `${rawDiskImage.name}-${diskImageTestRunId}`,
  sourceType: rawDiskImage.source_type,
  sourceRef: rawDiskImage.source_ref,
  guestOsFamily: rawDiskImage.guest_os_family,
  architectures: rawDiskImage.architectures,
  scope: rawDiskImage.scope,
  lifecycleActions: rawLifecycleActions,
};
