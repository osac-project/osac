import { create } from '@bufbuild/protobuf';
import { describe, expect, it } from 'vitest';

import { type BareMetalInstanceCatalogItem, BareMetalInstanceTypeSchema } from '@osac/types';

import { buildBareMetalListFilter } from './baremetal-instance-list-filter';

const gpuLargeInstanceType = create(BareMetalInstanceTypeSchema, {
  id: 'gpu-large',
  metadata: { name: 'gpu-large' },
  spec: {
    description: 'GPU-enabled bare metal host',
    hardware: {
      cpu: { cores: 64, architecture: 'x86_64', model: 'EPYC', threadsPerCore: 1 },
      memory: { totalGb: 512n, type: 'DDR5' },
      accelerators: [{ type: 'GPU', model: 'A100', vendor: 'NVIDIA', memoryGb: 80 }],
      disks: [],
      networkPorts: [],
      capabilities: {},
    },
  },
});

const gpuTrainingCatalogItem = {
  id: 'catalog-gpu',
  metadata: { name: 'bare-metal-gpu-training-server' },
  fields: {
    instanceType: {
      behavior: {
        case: 'locked',
        value: {
          id: 'gpu-large',
          name: 'gpu-large',
          project: 'default',
          shared: false,
        },
      },
    },
  },
} as BareMetalInstanceCatalogItem;

describe('buildBareMetalListFilter', () => {
  it('includes catalog item references when hardware filters match the catalog configured type', () => {
    const filter = buildBareMetalListFilter(
      { specs: { diskImage: [], gpu: [], ram: ['512 GB'], cpu: [] } },
      [gpuLargeInstanceType],
      [gpuTrainingCatalogItem],
    );

    expect(filter).toBe(
      '(this.spec.instance_type.id in ["gpu-large"] || this.spec.instance_type.name in ["gpu-large"] || (!has(this.spec.instance_type) && (this.spec.catalog_item.id in ["catalog-gpu"] || this.spec.catalog_item.name in ["bare-metal-gpu-training-server"])))',
    );
  });

  it('matches editable catalog defaults only when the instance omits an explicit instance type', () => {
    const editableGpuCatalogItem = {
      id: 'catalog-editable-gpu',
      metadata: { name: 'editable-gpu-server' },
      fields: {
        instanceType: {
          behavior: {
            case: 'editable',
            value: {
              defaultValue: {
                id: 'gpu-large',
                name: 'gpu-large',
                project: 'default',
                shared: false,
              },
            },
          },
        },
      },
    } as BareMetalInstanceCatalogItem;

    const filter = buildBareMetalListFilter(
      { specs: { diskImage: [], gpu: [], ram: ['512 GB'], cpu: [] } },
      [gpuLargeInstanceType],
      [editableGpuCatalogItem],
    );

    expect(filter).toBe(
      '(this.spec.instance_type.id in ["gpu-large"] || this.spec.instance_type.name in ["gpu-large"] || (!has(this.spec.instance_type) && (this.spec.catalog_item.id in ["catalog-editable-gpu"] || this.spec.catalog_item.name in ["editable-gpu-server"])))',
    );
  });

  it('combines locked and editable catalog fallbacks under the absent instance type guard', () => {
    const editableGpuCatalogItem = {
      id: 'catalog-editable-gpu',
      metadata: { name: 'editable-gpu-server' },
      fields: {
        instanceType: {
          behavior: {
            case: 'editable',
            value: {
              defaultValue: {
                id: 'gpu-large',
                name: 'gpu-large',
                project: 'default',
                shared: false,
              },
            },
          },
        },
      },
    } as BareMetalInstanceCatalogItem;

    const filter = buildBareMetalListFilter(
      { specs: { diskImage: [], gpu: [], ram: ['512 GB'], cpu: [] } },
      [gpuLargeInstanceType],
      [editableGpuCatalogItem, gpuTrainingCatalogItem],
    );

    expect(filter).toBe(
      '(this.spec.instance_type.id in ["gpu-large"] || this.spec.instance_type.name in ["gpu-large"] || (!has(this.spec.instance_type) && (this.spec.catalog_item.id in ["catalog-editable-gpu", "catalog-gpu"] || this.spec.catalog_item.name in ["editable-gpu-server", "bare-metal-gpu-training-server"])))',
    );
  });

  it('matches instances with explicit instance type references only when catalog items are omitted', () => {
    const filter = buildBareMetalListFilter(
      { specs: { diskImage: [], gpu: [], ram: ['512 GB'], cpu: [] } },
      [gpuLargeInstanceType],
    );

    expect(filter).toBe(
      '(this.spec.instance_type.id in ["gpu-large"] || this.spec.instance_type.name in ["gpu-large"])',
    );
  });
});
