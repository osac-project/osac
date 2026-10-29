import { test } from '@playwright/test';
import { readFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { parse } from 'yaml';

import type { DiskImageCreateParams } from '../../../../libs/test-utils/src/disk_images.page';
import type { InstanceTypeGpu } from '../../../../libs/test-utils/src/instance_types.page';
import {
  type LifecycleAction,
  lifecycleActionOrder,
} from '../../../../libs/test-utils/src/lifecycle';
import type {
  StorageTierCreateParams,
  StorageTierProtocol,
} from '../../../../libs/test-utils/src/storage.page';

interface RawInstanceTypeGpu {
  count: number;
  resource_name: string;
  pci_device_selector: string;
}

interface RawInstanceType {
  name?: string;
  description: string;
  vcpus: number;
  memory_gib: number;
  gpu?: RawInstanceTypeGpu;
}

interface RawDiskImage {
  name?: string;
  source_type: 'Registry';
  source_ref: string;
  guest_os_family: string;
  architectures: [string, ...string[]];
  scope: string;
}

interface RawCatalogInstanceTypeField {
  name: string;
  shared: boolean;
  vcpus: number;
  memory_gib: number;
}

interface RawCatalogFields {
  instance_type: RawCatalogInstanceTypeField;
  boot_disk: {
    size_gib: number;
  };
  disk_image: {
    name: string;
    shared: boolean;
  };
}

interface RawCatalog {
  name: string;
  template: string;
  title: string;
  description: string;
  type: string;
  status: string;
  usage: string;
  fields: RawCatalogFields;
}

interface RawStorageTier {
  name?: string;
  description: string;
  backend: string;
  protocol: StorageTierProtocol;
}

interface RawExtravars {
  catalog: RawCatalog;
  instance_type: RawInstanceType;
  disk_image: RawDiskImage;
  storage_tier: RawStorageTier;
  lifecycle_actions: LifecycleAction[];
  tenant_name?: string;
}

export interface CatalogFieldsTestData {
  instanceType: {
    name: string;
    shared: boolean;
    vcpus: number;
    memoryGib: number;
  };
  bootDisk: {
    sizeGib: number;
  };
  diskImage: {
    name: string;
    shared: boolean;
  };
}

export interface CatalogTestData {
  description: string;
  fields: CatalogFieldsTestData;
  name: string;
  status: string;
  template: string;
  title: string;
  type: string;
  usage: string;
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

const requiredBoolean = (value: unknown, field: string): boolean => {
  if (typeof value !== 'boolean') {
    throw new Error(`Invalid e2e extravars: ${field} must be a boolean.`);
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

const storageTierProtocol = (value: unknown): StorageTierProtocol => {
  const protocol = requiredString(value, 'storage_tier.protocol');
  if (protocol !== 'NFS' && protocol !== 'Block') {
    throw new Error('Invalid e2e extravars: storage_tier.protocol must be NFS or Block.');
  }

  return protocol;
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
  const catalog = asRecord(root.catalog, 'catalog');
  const catalogFields = asRecord(catalog.fields, 'catalog.fields');
  const catalogInstanceType = asRecord(catalogFields.instance_type, 'catalog.fields.instance_type');
  const catalogBootDisk = asRecord(catalogFields.boot_disk, 'catalog.fields.boot_disk');
  const catalogDiskImage = asRecord(catalogFields.disk_image, 'catalog.fields.disk_image');
  const instanceType = asRecord(root.instance_type, 'instance_type');
  const diskImage = asRecord(root.disk_image, 'disk_image');
  const storageTier = asRecord(root.storage_tier, 'storage_tier');
  const gpu =
    instanceType.gpu === undefined ? undefined : asRecord(instanceType.gpu, 'instance_type.gpu');

  return {
    catalog: {
      name: requiredString(catalog.name, 'catalog.name'),
      template: requiredString(catalog.template, 'catalog.template'),
      title: requiredString(catalog.title, 'catalog.title'),
      description: requiredString(catalog.description, 'catalog.description'),
      type: requiredString(catalog.type, 'catalog.type'),
      status: requiredString(catalog.status, 'catalog.status'),
      usage: requiredString(catalog.usage, 'catalog.usage'),
      fields: {
        instance_type: {
          name: requiredString(catalogInstanceType.name, 'catalog.fields.instance_type.name'),
          shared: requiredBoolean(
            catalogInstanceType.shared,
            'catalog.fields.instance_type.shared',
          ),
          vcpus: positiveInteger(catalogInstanceType.vcpus, 'catalog.fields.instance_type.vcpus'),
          memory_gib: positiveInteger(
            catalogInstanceType.memory_gib,
            'catalog.fields.instance_type.memory_gib',
          ),
        },
        boot_disk: {
          size_gib: positiveInteger(catalogBootDisk.size_gib, 'catalog.fields.boot_disk.size_gib'),
        },
        disk_image: {
          name: requiredString(catalogDiskImage.name, 'catalog.fields.disk_image.name'),
          shared: requiredBoolean(catalogDiskImage.shared, 'catalog.fields.disk_image.shared'),
        },
      },
    },
    tenant_name: optionalString(root.tenant_name, 'tenant_name'),
    instance_type: {
      name: optionalString(instanceType.name, 'instance_type.name'),
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
      name: optionalString(diskImage.name, 'disk_image.name'),
      source_type: registrySourceType(diskImage.source_type),
      source_ref: requiredString(diskImage.source_ref, 'disk_image.source_ref'),
      guest_os_family: requiredString(diskImage.guest_os_family, 'disk_image.guest_os_family'),
      architectures: stringTuple(diskImage.architectures, 'disk_image.architectures'),
      scope: requiredString(diskImage.scope, 'disk_image.scope'),
    },
    storage_tier: {
      name: optionalString(storageTier.name, 'storage_tier.name'),
      description: requiredString(storageTier.description, 'storage_tier.description'),
      backend: requiredString(storageTier.backend, 'storage_tier.backend'),
      protocol: storageTierProtocol(storageTier.protocol),
    },
    lifecycle_actions: lifecycleActions(root.lifecycle_actions),
  };
};

const extravarsPath = path.resolve(
  path.dirname(fileURLToPath(import.meta.url)),
  '../../extravars.yaml',
);

const rawExtravars = parseExtravars(parse(readFileSync(extravarsPath, 'utf8')));

const {
  catalog: rawCatalog,
  instance_type: rawInstanceType,
  disk_image: rawDiskImage,
  storage_tier: rawStorageTier,
  lifecycle_actions: rawLifecycleActions,
  tenant_name: rawTenantName,
} = rawExtravars;
const diskImageTestRunId = requiredString(
  process.env.OSAC_DISK_IMAGE_TEST_RUN_ID,
  'OSAC_DISK_IMAGE_TEST_RUN_ID',
);
const storageTierTestRunId = requiredString(
  process.env.OSAC_STORAGE_TIER_TEST_RUN_ID,
  'OSAC_STORAGE_TIER_TEST_RUN_ID',
);
const catalogTestRunId = requiredString(
  process.env.OSAC_CATALOG_TEST_RUN_ID,
  'OSAC_CATALOG_TEST_RUN_ID',
);
const diskImageTestName = rawDiskImage.name
  ? `${rawDiskImage.name}-${diskImageTestRunId}`
  : undefined;
const storageTierTestName = rawStorageTier.name
  ? `${rawStorageTier.name}-${storageTierTestRunId}`
  : undefined;

export const tenantName: string | undefined = rawTenantName;

export const tenantNameOrSkip = (): string | undefined => {
  if (tenantName !== undefined) {
    return tenantName;
  }

  test.skip(true, 'tenant_name is not set in apps/e2e-tests/extravars.yaml.');
  return undefined;
};

export const instanceTypeNameOrSkip = (): string | undefined => {
  if (rawInstanceType.name !== undefined) {
    return rawInstanceType.name;
  }

  test.skip(true, 'instance_type.name is not set in apps/e2e-tests/extravars.yaml.');
  return undefined;
};

export const diskImageNameOrSkip = (): string | undefined => {
  if (diskImageTestName !== undefined) {
    return diskImageTestName;
  }

  test.skip(true, 'disk_image.name is not set in apps/e2e-tests/extravars.yaml.');
  return undefined;
};

export const storageTierNameOrSkip = (): string | undefined => {
  if (storageTierTestName !== undefined) {
    return storageTierTestName;
  }

  test.skip(true, 'storage_tier.name is not set in apps/e2e-tests/extravars.yaml.');
  return undefined;
};

const catalogName = rawCatalog.name?.trim();
if (!catalogName) {
  throw new Error('catalog.name must be set in extravars.yaml');
}

export const catalogTestData: CatalogTestData = {
  description: rawCatalog.description,
  fields: {
    instanceType: {
      name: rawCatalog.fields.instance_type.name,
      shared: rawCatalog.fields.instance_type.shared,
      vcpus: rawCatalog.fields.instance_type.vcpus,
      memoryGib: rawCatalog.fields.instance_type.memory_gib,
    },
    bootDisk: {
      sizeGib: rawCatalog.fields.boot_disk.size_gib,
    },
    diskImage: {
      name: rawCatalog.fields.disk_image.name,
      shared: rawCatalog.fields.disk_image.shared,
    },
  },
  name: catalogName,
  status: rawCatalog.status,
  template: rawCatalog.template,
  title: rawCatalog.title,
  type: rawCatalog.type,
  usage: rawCatalog.usage,
};

const safeCatalogName = (name: string): string =>
  name.replace(/[^a-zA-Z0-9._-]/g, '_') || 'catalog';

export const catalogOwnershipFilePath = (name: string): string =>
  path.join(tmpdir(), `osac-catalog-${safeCatalogName(name)}-${catalogTestRunId}.created`);

export const instanceTypeTestData: InstanceTypeTestData = {
  name: rawInstanceType.name ?? '',
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
  name: diskImageTestName ?? '',
  sourceType: rawDiskImage.source_type,
  sourceRef: rawDiskImage.source_ref,
  guestOsFamily: rawDiskImage.guest_os_family,
  architectures: rawDiskImage.architectures,
  scope: rawDiskImage.scope,
  lifecycleActions: rawLifecycleActions,
};

export const storageTierTestData: StorageTierCreateParams = {
  name: storageTierTestName ?? '',
  description: rawStorageTier.description,
  backend: rawStorageTier.backend,
  protocol: rawStorageTier.protocol,
};
