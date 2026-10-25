import { create } from '@bufbuild/protobuf';
import { screen, within } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import {
  BareMetalInstanceCatalogItemSchema,
  BareMetalInstanceTypeReferenceSchema,
  ClusterCatalogItemSchema,
  ClusterVersionReferenceSchema,
  ComputeInstanceCatalogItemSchema,
  ComputeInstanceTemplateReferenceSchema,
  DiskImageSchema,
  GpuSpecSchema,
  GuestOSFamily,
  InstanceTypeSchema,
  InstanceTypeState,
} from '@osac/types';

import { CatalogItemDetailContent } from './CatalogItemDetailContent';
import VmCatalogItemDetailContent from './ComputeCatalogItemDetailContent';
import { renderWithProviders } from '../../../test-utils/TestProviders';

const INSTANCE_TYPE_ID = 'it-4-8';

const catalogApiFixtures = {
  instanceTypes: [
    create(InstanceTypeSchema, {
      id: INSTANCE_TYPE_ID,
      metadata: { name: 'standard-4-8' },
      spec: {
        vcpus: 4,
        memoryGib: 8,
        state: InstanceTypeState.ACTIVE,
        gpu: create(GpuSpecSchema, { count: 2, resourceName: 'nvidia.com/A100' }),
      },
    }),
  ],
  diskImages: [
    create(DiskImageSchema, {
      id: 'di-rhel-10',
      metadata: { name: 'RHEL 10' },
      spec: { guestOsFamily: GuestOSFamily.GUEST_OS_FAMILY_LINUX },
    }),
  ],
};

const vmItem = create(ComputeInstanceCatalogItemSchema, {
  id: 'catalog-rhel-9',
  metadata: {
    name: 'catalog-rhel-9',
    labels: { 'run-strategy': 'Always' },
    project: 'foo',
    tenant: 'foo',
  },
  title: 'RHEL 9 catalog',
  description: 'RHEL 9 base image',
  template: create(ComputeInstanceTemplateReferenceSchema, { id: 'template-1' }),
  published: true,
  fields: {
    instanceType: {
      behavior: { case: 'locked', value: { id: INSTANCE_TYPE_ID, name: 'standard-4-8' } },
    },
    diskImage: {
      behavior: {
        case: 'editable',
        value: { defaultValue: { id: 'di-rhel-10', name: 'RHEL 10' } },
      },
    },
    userData: {
      behavior: { case: 'locked', value: '#cloud-config' },
    },
    bootDisk: {
      sizeGib: { behavior: { case: 'locked', value: 64 } },
      storageTier: {
        behavior: {
          case: 'editable',
          value: { defaultValue: { id: 'tier-fast', name: 'fast' } },
        },
      },
    },
    additionalDisks: {
      behavior: {
        case: 'locked',
        value: { items: [{ sizeGib: 100, storageTier: { id: 'tier-fast', name: 'fast' } }] },
      },
    },
    runStrategy: { behavior: { case: 'locked', value: 1 } },
    sshKey: { behavior: { case: 'locked', value: { name: 'ssh-key' } } },
  },
});

const clusterItem = create(ClusterCatalogItemSchema, {
  id: 'catalog-cluster',
  metadata: { name: 'catalog-cluster', tenant: 'tenant-1' },
  fields: {
    version: {
      behavior: {
        case: 'locked',
        value: create(ClusterVersionReferenceSchema, { name: '4.16' }),
      },
    },
    nodeSets: {
      behavior: {
        case: 'editable',
        value: {
          defaultValue: {
            items: {
              'control-plane': {
                size: 3,
                baremetalInstanceType: { name: 'bm-cpu-64c' },
              },
              workers: {
                size: 5,
                baremetalInstanceType: { name: 'bm-cpu-32c' },
              },
              'gpu-workers': {
                size: 2,
                baremetalInstanceType: { name: 'bm-gpu-h100' },
              },
            },
          },
        },
      },
    },
    network: {
      podCidr: { behavior: { case: 'locked', value: '10.128.0.0/14' } },
      serviceCidr: { behavior: { case: 'editable', value: { defaultValue: '172.30.0.0/16' } } },
    },
    sshPublicKey: { behavior: { case: 'locked', value: 'ssh-rsa hidden' } },
    pullSecretSecret: { behavior: { case: 'locked', value: { name: 'pull-secret' } } },
    autoExternalIpAttachment: { behavior: { case: 'locked', value: true } },
    networkAttachment: { behavior: { case: 'locked', value: {} } },
  },
});

const bareMetalItem = create(BareMetalInstanceCatalogItemSchema, {
  id: 'catalog-bm',
  metadata: { name: 'catalog-bm', tenant: 'tenant-1' },
  fields: {
    instanceType: {
      behavior: {
        case: 'locked',
        value: create(BareMetalInstanceTypeReferenceSchema, { name: 'bm.large' }),
      },
    },
    diskImage: {
      behavior: {
        case: 'editable',
        value: { defaultValue: { id: 'di-rhel-10', name: 'RHEL 10' } },
      },
    },
    userData: { behavior: { case: 'editable', value: { defaultValue: 'echo ready' } } },
    runStrategy: { behavior: { case: 'locked', value: 1 } },
    networkAttachments: { behavior: { case: 'locked', value: {} } },
    autoExternalIpAttachment: { behavior: { case: 'locked', value: true } },
  },
});

describe('CatalogItemDetailContent', () => {
  it('shows VM detail columns with lookups, governance, and safe access summaries', async () => {
    const { container } = renderWithProviders(<VmCatalogItemDetailContent item={vmItem} />, {
      apiFixtures: catalogApiFixtures,
    });

    expect(screen.getByRole('heading', { name: 'Overview' })).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: 'Configuration' })).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: 'Access & Initial run' })).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: 'Storage' })).toBeInTheDocument();
    expect(screen.getAllByRole('separator')).toHaveLength(1);
    expect(container.querySelector('.pf-v6-c-card')).toBeNull();
    expect(screen.getByText('Virtual machines')).toBeInTheDocument();
    expect(screen.getByText('template-1')).toBeInTheDocument();
    expect(screen.getByText('foo')).toBeInTheDocument();
    expect(screen.getByText('Live')).toBeInTheDocument();

    expect(screen.getByText('Instance type')).toBeInTheDocument();
    expect(screen.getByText('standard-4-8')).toBeInTheDocument();
    expect(screen.getByText('RHEL 10')).toBeInTheDocument();
    expect(await screen.findByText('Linux')).toBeInTheDocument();
    expect(screen.getByText('Disk image')).toBeInTheDocument();
    expect(screen.getByText('Cloud-init')).toBeInTheDocument();
    expect(screen.getByText('Configured')).toBeInTheDocument();
    expect(screen.queryByText('#cloud-config')).not.toBeInTheDocument();
    expect(screen.getByText('Boot disk size')).toBeInTheDocument();
    expect(screen.getByText('Boot storage tier')).toBeInTheDocument();
    const additionalDisksTerm = screen.getByText('Additional disks').closest('dt');
    expect(additionalDisksTerm).not.toBeNull();
    expect(within(additionalDisksTerm as HTMLElement).getByText('Locked')).toBeInTheDocument();
    const storageTable = screen.getByRole('grid', { name: 'Additional disks' });
    expect(within(storageTable).getAllByRole('columnheader')).toHaveLength(2);
    expect(within(storageTable).getByRole('columnheader', { name: 'Size' })).toBeInTheDocument();
    expect(
      within(storageTable).getByRole('columnheader', { name: 'Storage tier' }),
    ).toBeInTheDocument();
    expect(screen.getByText('64 GiB')).toBeInTheDocument();
    expect(within(storageTable).getByText('100 GiB')).toBeInTheDocument();
    expect(screen.getAllByText('fast')).toHaveLength(2);
    expect(screen.getByText('GPU count')).toBeInTheDocument();
    expect(await screen.findByText('2')).toBeInTheDocument();
    expect(screen.getByText('Resource name')).toBeInTheDocument();
    expect(await screen.findByText('nvidia.com/A100')).toBeInTheDocument();
    expect(screen.getAllByText('Locked').length).toBeGreaterThan(0);
    expect(screen.getAllByText('Editable').length).toBeGreaterThan(0);
    expect(screen.queryByText('Run strategy')).not.toBeInTheDocument();
    expect(screen.queryByText('SSH key')).not.toBeInTheDocument();
    expect(screen.queryByText('Network attachments')).not.toBeInTheDocument();
    expect(screen.queryByText('Auto external IP')).not.toBeInTheDocument();
  });

  it('shows configured cluster fields and excludes unsupported policies', () => {
    const { container } = renderWithProviders(<CatalogItemDetailContent item={clusterItem} />, {
      apiFixtures: catalogApiFixtures,
    });

    expect(screen.getByText('Cluster version')).toBeInTheDocument();
    expect(screen.getByText('OpenShift 4.16')).toBeInTheDocument();
    expect(screen.getByText('Node sets')).toBeInTheDocument();
    expect(screen.getByText('control-plane')).toBeInTheDocument();
    expect(screen.getByText('bm-cpu-64c · 3 nodes')).toBeInTheDocument();
    expect(screen.getByText('workers')).toBeInTheDocument();
    expect(screen.getByText('bm-cpu-32c · 5 nodes')).toBeInTheDocument();
    expect(screen.getByText('gpu-workers')).toBeInTheDocument();
    expect(screen.getByText('bm-gpu-h100 · 2 nodes')).toBeInTheDocument();
    expect(screen.getAllByText('Editable').length).toBeGreaterThan(0);
    expect(container.querySelector('dl[aria-label="Node sets"]')).toHaveClass(
      'catalog-node-sets__list',
    );
    expect(screen.queryByText(/vCPU|GB RAM/)).not.toBeInTheDocument();
    expect(screen.getByText('Pod CIDR')).toBeInTheDocument();
    expect(screen.getByText('10.128.0.0/14')).toBeInTheDocument();
    expect(screen.getByText('Service CIDR')).toBeInTheDocument();
    expect(screen.getByText('172.30.0.0/16')).toBeInTheDocument();
    expect(screen.queryByText('SSH public key')).not.toBeInTheDocument();
    expect(screen.queryByText('Pull secret')).not.toBeInTheDocument();
    expect(screen.queryByText('Network attachment')).not.toBeInTheDocument();
    expect(screen.queryByText('Automatic external IP')).not.toBeInTheDocument();
  });

  it('shows configured bare metal fields and excludes run strategy', () => {
    renderWithProviders(<CatalogItemDetailContent item={bareMetalItem} />, {
      apiFixtures: catalogApiFixtures,
    });

    expect(screen.getByText('Instance type')).toBeInTheDocument();
    expect(screen.getByText('bm.large')).toBeInTheDocument();
    expect(screen.getByText('RHEL 10')).toBeInTheDocument();
    expect(screen.getByText('echo ready')).toBeInTheDocument();
    expect(screen.queryByText('Run strategy')).not.toBeInTheDocument();
  });

  it('renders all VM sections with safe empty values when fields are unconfigured', () => {
    const item = create(ComputeInstanceCatalogItemSchema, {
      id: 'catalog-empty',
      metadata: { name: 'catalog-empty', tenant: 'tenant-1' },
      fields: {},
    });

    renderWithProviders(<VmCatalogItemDetailContent item={item} />, {
      apiFixtures: catalogApiFixtures,
    });

    expect(screen.getByRole('heading', { name: 'Overview' })).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: 'Configuration' })).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: 'Access & Initial run' })).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: 'Storage' })).toBeInTheDocument();
    expect(screen.queryByRole('grid', { name: 'Additional disks' })).not.toBeInTheDocument();
    expect(screen.getByText('Additional disks')).toBeInTheDocument();
    expect(screen.getAllByText('Not set').length).toBeGreaterThan(0);
    expect(screen.queryByText(/cloud-config|script|userdata/i)).not.toBeInTheDocument();
  });
});
