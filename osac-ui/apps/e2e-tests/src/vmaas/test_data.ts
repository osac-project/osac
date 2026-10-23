import { readFileSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { parse } from 'yaml';

import type {
  InstanceTypeGpu,
  InstanceTypeLifecycleAction,
} from '../../../../libs/test-utils/src/instance_types.page';

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
  lifecycle_actions: InstanceTypeLifecycleAction[];
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

interface RawExtravars {
  catalog: RawCatalog;
  instance_type: RawInstanceType;
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
  lifecycleActions: InstanceTypeLifecycleAction[];
}

const extravarsPath = path.resolve(
  path.dirname(fileURLToPath(import.meta.url)),
  '../../extravars.yaml',
);

const rawExtravars = parse(readFileSync(extravarsPath, 'utf8')) as RawExtravars;

const { catalog: rawCatalog } = rawExtravars;
const { instance_type: rawInstanceType } = rawExtravars;

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
  lifecycleActions: rawInstanceType.lifecycle_actions,
};
