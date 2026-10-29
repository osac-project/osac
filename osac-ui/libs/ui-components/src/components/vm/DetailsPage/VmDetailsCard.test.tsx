import { create } from '@bufbuild/protobuf';
import { screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import {
  ComputeInstance,
  ComputeInstanceCatalogItemReferenceSchema,
  ComputeInstanceState,
  ComputeInstanceTemplateReferenceSchema,
  InstanceTypeReferenceSchema,
  SecretReferenceSchema,
  StorageTierReferenceSchema,
} from '@osac/types';

import VmDetailsCard from './VmDetailsCard';
import { renderWithProviders } from '../../../test-utils/TestProviders';

const catalogVm: ComputeInstance = {
  $typeName: 'osac.public.v1.ComputeInstance',
  id: 'vm-1',
  metadata: {
    $typeName: 'osac.public.v1.Metadata',
    displayName: '',
    description: '',
    name: 'web-01',
    creator: 'alice',
    creationTimestamp: {
      $typeName: 'google.protobuf.Timestamp',
      seconds: 1767225600n,
      nanos: 0,
    },
    annotations: {},
    labels: {},
    project: 'foo',
    tenant: 'foo',
    version: 1,
  },
  spec: {
    $typeName: 'osac.public.v1.ComputeInstanceSpec',
    catalogItem: create(ComputeInstanceCatalogItemReferenceSchema, {
      id: 'catalog-rhel-9',
      name: 'RHEL 9 catalog',
    }),
    sshKey: create(SecretReferenceSchema, { name: 'tenant-ssh-key' }),
    instanceType: create(InstanceTypeReferenceSchema, { id: 'standard-4-8' }),
    bootDisk: {
      $typeName: 'osac.public.v1.ComputeInstanceDisk',
      sizeGib: 40,
      storageTier: create(StorageTierReferenceSchema, { name: 'balanced' }),
    },
    //userData: '#cloud-config',
    additionalDisks: {
      $typeName: 'osac.public.v1.ComputeInstanceDiskList',
      items: [],
    },
    networkAttachments: [],
    template: create(ComputeInstanceTemplateReferenceSchema, { id: '' }),
    templateParameters: {},
    autoExternalIpAttachment: false,
  },
};

const renderCard = (vm: ComputeInstance = catalogVm) =>
  renderWithProviders(<VmDetailsCard vm={vm} />);

describe('VmDetailsCard', () => {
  it('shows catalog fields with the SSH Secret reference', () => {
    renderCard();

    expect(screen.getByText('Details')).toBeInTheDocument();
    expect(screen.getByText('RHEL 9 catalog')).toBeInTheDocument();
    expect(screen.getByText('standard-4-8')).toBeInTheDocument();
    expect(screen.getByText('web-01')).toBeInTheDocument();
    expect(screen.getByText('tenant-ssh-key')).toBeInTheDocument();
    expect(screen.getByText('40 GB, balanced')).toBeInTheDocument();
    expect(screen.queryByText('User Data')).not.toBeInTheDocument();
    expect(screen.queryByText('Run strategy')).not.toBeInTheDocument();
    expect(screen.queryByText('Tenants')).not.toBeInTheDocument();
    expect(screen.queryByText('Version')).not.toBeInTheDocument();
    expect(screen.queryByText('Creator')).not.toBeInTheDocument();
  });

  it('still shows spec fields when catalog item is missing', () => {
    renderCard({
      id: 'vm-2',
      metadata: { name: 'legacy-vm' },
      spec: { sshKey: { name: 'legacy-key' } },
    } as ComputeInstance);
    expect(
      screen.queryByText('Catalog configuration is unavailable for this virtual machine.'),
    ).not.toBeInTheDocument();
    expect(screen.getByText('legacy-vm')).toBeInTheDocument();
    expect(screen.getByText('legacy-key')).toBeInTheDocument();
    expect(screen.getByText('SSH public key')).toBeInTheDocument();
  });

  it('lists each additional disk with its resolved tier and exposes no edit control', () => {
    const vmWithAdditionalDisks = {
      ...catalogVm,
      spec: {
        ...catalogVm.spec,
        additionalDisks: {
          items: [
            {
              $typeName: 'osac.public.v1.ComputeInstanceDisk',
              sizeGib: 100,
              storageTier: create(StorageTierReferenceSchema, { name: 'fast' }),
            },
            {
              $typeName: 'osac.public.v1.ComputeInstanceDisk',
              sizeGib: 20,
              storageTier: create(StorageTierReferenceSchema, { name: 'legacy-tier' }),
            },
          ],
        },
      },
    } as ComputeInstance;

    renderCard(vmWithAdditionalDisks);

    expect(screen.getByText('40 GB, balanced')).toBeInTheDocument();
    expect(screen.getByText('Additional disk 1')).toBeInTheDocument();
    expect(screen.getByText('100 GB, fast')).toBeInTheDocument();
    expect(screen.getByText('Additional disk 2')).toBeInTheDocument();
    expect(screen.getByText('20 GB, legacy-tier')).toBeInTheDocument();
    expect(screen.queryAllByRole('combobox')).toHaveLength(0);
    expect(screen.getByRole('button', { name: 'Edit instance type' })).toBeInTheDocument();
  });

  it.each([
    ComputeInstanceState.RUNNING,
    ComputeInstanceState.PAUSED,
    ComputeInstanceState.STOPPED,
  ])('enables the instance type pencil when the VM is %s', (state) => {
    renderCard({ ...catalogVm, status: { state } } as ComputeInstance);

    expect(screen.getByRole('button', { name: 'Edit instance type' })).toBeEnabled();
  });

  it('disables the pencil while the VM is starting', async () => {
    const { user } = renderCard({
      ...catalogVm,
      status: { state: ComputeInstanceState.STARTING },
    } as ComputeInstance);

    const button = screen.getByRole('button', { name: 'Edit instance type' });
    expect(button).toBeDisabled();
    await user.hover(button);
    expect(
      await screen.findByText(
        'The instance type can be changed when the virtual machine is running, paused, or stopped.',
      ),
    ).toBeInTheDocument();
  });

  it('opens the edit instance type modal from the pencil', async () => {
    const { user } = renderCard({
      ...catalogVm,
      status: { state: ComputeInstanceState.RUNNING },
    } as ComputeInstance);

    await user.click(screen.getByRole('button', { name: 'Edit instance type' }));

    expect(await screen.findByRole('heading', { name: 'Edit instance type' })).toBeInTheDocument();
  });

  it('explains that a deleting VM cannot be edited', async () => {
    const { user } = renderCard({
      ...catalogVm,
      status: { state: ComputeInstanceState.DELETING },
    } as ComputeInstance);

    await user.hover(screen.getByRole('button', { name: 'Edit instance type' }));
    expect(await screen.findByText('This virtual machine is being deleted.')).toBeInTheDocument();
  });
});
