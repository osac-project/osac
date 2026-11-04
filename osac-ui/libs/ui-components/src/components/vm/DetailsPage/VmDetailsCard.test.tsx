import { create } from '@bufbuild/protobuf';
import { screen, within } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import {
  ComputeInstance,
  ComputeInstanceCatalogItemReferenceSchema,
  ComputeInstanceState,
  ComputeInstanceTemplateReferenceSchema,
  GuestOSFamily,
  InstanceType,
  InstanceTypeReferenceSchema,
  SecretLocalReferenceSchema,
} from '@osac/types';
import type { DiskImage } from '@osac/types';

import VmDetailsCard from './VmDetailsCard';
import { renderWithProviders } from '../../../test-utils/TestProviders';

const vm = {
  $typeName: 'osac.public.v1.ComputeInstance',
  id: 'vm-1',
  metadata: {
    $typeName: 'osac.public.v1.Metadata',
    name: 'web-01',
    project: 'tenant-project',
    creationTimestamp: { seconds: 1767225600n, nanos: 0 },
  },
  spec: {
    $typeName: 'osac.public.v1.ComputeInstanceSpec',
    catalogItem: create(ComputeInstanceCatalogItemReferenceSchema, {
      id: 'catalog-rhel-9',
      name: 'RHEL 9 catalog',
    }),
    diskImage: { id: 'disk-image-rhel', name: 'RHEL 9.4' },
    sshKey: create(SecretLocalReferenceSchema, { id: 'secret-1', name: 'tenant-ssh-key' }),
    instanceType: create(InstanceTypeReferenceSchema, { id: 'standard-4-8' }),
    userDataSource: { source: { case: 'inline', value: '#cloud-config' } },
    template: create(ComputeInstanceTemplateReferenceSchema, { id: '' }),
  },
  status: { state: ComputeInstanceState.RUNNING },
} as unknown as ComputeInstance;

const instanceType = {
  id: 'standard-4-8',
  metadata: { name: 'Standard 4 vCPU / 8 GiB' },
  spec: {
    vcpus: 4,
    memoryGib: 8,
    gpu: { count: 1, resourceName: 'nvidia.com/gpu' },
  },
} as unknown as InstanceType;

const renderCard = (cardVm: ComputeInstance = vm) =>
  renderWithProviders(<VmDetailsCard vm={cardVm} />);

describe('VmDetailsCard', () => {
  it('renders overview, configuration, and access fields without a card wrapper', () => {
    const { container } = renderWithProviders(
      <VmDetailsCard
        vm={vm}
        instanceType={instanceType}
        isInstanceTypeLoading={false}
        diskImage={
          {
            spec: { guestOsFamily: GuestOSFamily.GUEST_OS_FAMILY_LINUX },
          } as unknown as DiskImage
        }
      />,
    );

    expect(screen.getByRole('heading', { name: 'Overview' })).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: 'Configuration' })).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: 'Access & Initial run' })).toBeInTheDocument();
    const externalIpTerm = screen.getByText('External IP');
    const sshTerm = screen.getByText('SSH public key');
    expect(
      externalIpTerm.compareDocumentPosition(sshTerm) & Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy();
    expect(screen.getByText('tenant-project')).toBeInTheDocument();
    expect(screen.getByText('RHEL 9 catalog')).toBeInTheDocument();
    expect(screen.getByText('Linux')).toBeInTheDocument();
    expect(screen.getByText('RHEL 9.4')).toBeInTheDocument();
    expect(screen.getByText('Standard 4 vCPU / 8 GiB')).toBeInTheDocument();
    expect(screen.getByText('4')).toBeInTheDocument();
    expect(screen.getByText('8 GiB')).toBeInTheDocument();
    expect(screen.getByText('nvidia.com/gpu')).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'tenant-ssh-key' })).toHaveAttribute(
      'href',
      '/secrets/secret-1',
    );
    expect(screen.getByText('Configured')).toBeInTheDocument();
    expect(container.querySelector('.pf-v6-c-card')).toBeNull();
  });

  it('uses a hyphen for unresolved VM user data', () => {
    renderWithProviders(
      <VmDetailsCard
        vm={{ id: 'vm-2', metadata: { name: 'legacy-vm' }, spec: {} } as ComputeInstance}
        isInstanceTypeLoading={false}
      />,
    );

    const cloudInitRow = screen.getByText('Cloud-init').closest('div') as HTMLElement;
    expect(within(cloudInitRow).getByText('-')).toBeInTheDocument();
  });

  it('uses hyphens instead of Not set for unset VM details', () => {
    renderWithProviders(
      <VmDetailsCard
        vm={{ id: 'vm-2', metadata: { name: 'legacy-vm' }, spec: {} } as ComputeInstance}
        isInstanceTypeLoading={false}
      />,
    );

    expect(screen.queryByText('Not set')).not.toBeInTheDocument();
    expect(screen.getAllByText('-').length).toBeGreaterThan(0);
  });

  it('expands inline user data from the Cloud-init row', async () => {
    const { user } = renderCard();

    await user.click(screen.getByRole('button', { name: 'Expand user data' }));

    expect(screen.getByText('#cloud-config')).toBeVisible();
  });

  it('links Secret-backed user data to the Secret resource', () => {
    renderCard({
      ...vm,
      spec: {
        ...vm.spec,
        userDataSource: {
          source: { case: 'secret', value: { id: 'user-data-secret', name: 'cloud-init-secret' } },
        },
      },
    } as ComputeInstance);

    expect(screen.getByRole('link', { name: 'cloud-init-secret' })).toHaveAttribute(
      'href',
      '/secrets/user-data-secret',
    );
  });

  it.each([
    ComputeInstanceState.RUNNING,
    ComputeInstanceState.PAUSED,
    ComputeInstanceState.STOPPED,
  ])('enables the instance type pencil when the VM is %s', (state) => {
    renderCard({ ...vm, status: { state } } as ComputeInstance);

    expect(screen.getByRole('button', { name: 'Edit instance type' })).toBeEnabled();
  });

  it('disables the pencil while the VM is starting', async () => {
    const { user } = renderCard({
      ...vm,
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
    const { user } = renderCard();

    await user.click(screen.getByRole('button', { name: 'Edit instance type' }));

    expect(await screen.findByRole('heading', { name: 'Edit instance type' })).toBeInTheDocument();
  });

  it('explains that a deleting VM cannot be edited', async () => {
    const { user } = renderCard({
      ...vm,
      status: { state: ComputeInstanceState.DELETING },
    } as ComputeInstance);

    await user.hover(screen.getByRole('button', { name: 'Edit instance type' }));
    expect(await screen.findByText('This virtual machine is being deleted.')).toBeInTheDocument();
  });
});
