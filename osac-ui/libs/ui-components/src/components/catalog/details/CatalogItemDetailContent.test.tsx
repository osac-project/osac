import { create } from '@bufbuild/protobuf';
import { screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import {
  BareMetalInstanceCatalogItemSchema,
  BareMetalInstanceTypeReferenceSchema,
  ClusterCatalogItemSchema,
  ClusterVersionReferenceSchema,
  ComputeInstanceCatalogItemSchema,
  DiskImageSchema,
  GuestOSFamily,
  InstanceTypeSchema,
  InstanceTypeState,
} from '@osac/types';

import { CatalogItemDetailContent } from './CatalogItemDetailContent';
import { renderWithProviders } from '../../../test-utils/TestProviders';

const INSTANCE_TYPE_ID = 'it-4-8';

const catalogApiFixtures = {
  instanceTypes: [
    create(InstanceTypeSchema, {
      id: INSTANCE_TYPE_ID,
      metadata: { name: 'standard-4-8' },
      spec: { vcpus: 4, memoryGib: 8, state: InstanceTypeState.ACTIVE },
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
  published: true,
  fields: {
    instanceType: {
      behavior: { case: 'locked', value: { id: INSTANCE_TYPE_ID, name: 'standard-4-8' } },
    },
    diskImage: {
      behavior: { case: 'editable', value: {} },
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
  it('shows configured compute fields with governance and omits excluded policies', () => {
    renderWithProviders(<CatalogItemDetailContent item={vmItem} />, {
      apiFixtures: catalogApiFixtures,
    });

    expect(screen.getByRole('heading', { name: 'Overview' })).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: 'Publishing' })).toBeInTheDocument();
    expect(screen.getByText('Virtual Machine')).toBeInTheDocument();
    expect(screen.getByText('foo')).toBeInTheDocument();
    const detailsCard = screen.getByRole('region', { name: 'Details' });
    const configurationCard = screen.getByRole('region', { name: 'Configuration' });
    expect(detailsCard).toHaveClass('pf-m-full-height');
    expect(detailsCard.querySelector('dl')).toHaveClass('pf-m-compact');
    expect(detailsCard.querySelector('dl')).not.toHaveClass('pf-m-horizontal');
    expect(configurationCard).toHaveClass('pf-m-secondary');
    expect(configurationCard.querySelector('dl')).toHaveClass('pf-m-compact');
    expect(configurationCard.querySelector('dl')).not.toHaveClass('pf-m-horizontal');

    expect(screen.getByText('Instance type')).toBeInTheDocument();
    expect(screen.getByText('standard-4-8')).toBeInTheDocument();
    expect(screen.getByText('Disk image')).toBeInTheDocument();
    expect(screen.getByText('User data')).toBeInTheDocument();
    expect(screen.getByText('#cloud-config')).toBeInTheDocument();
    expect(screen.getByText('64 GiB')).toBeInTheDocument();
    expect(screen.getByText('fast')).toBeInTheDocument();
    expect(screen.getByText('100 GiB (fast)')).toBeInTheDocument();
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

  it('shows an empty state when no supported fields are governed', () => {
    const item = create(ComputeInstanceCatalogItemSchema, {
      id: 'catalog-empty',
      metadata: { name: 'catalog-empty', tenant: 'tenant-1' },
      fields: {},
    });

    renderWithProviders(<CatalogItemDetailContent item={item} />, {
      apiFixtures: catalogApiFixtures,
    });

    expect(screen.getByText('No governed configuration')).toBeInTheDocument();
  });
});
