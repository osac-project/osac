import type { ComponentProps } from 'react';
import { create } from '@bufbuild/protobuf';
import { screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import type { ComputeInstance, InstanceType } from '@osac/types';
import {
  ComputeInstanceCatalogItemReferenceSchema,
  ComputeInstanceState,
  ComputeInstanceTemplateReferenceSchema,
  InstanceTypeReferenceSchema,
} from '@osac/types';

import { VmTable } from './VmTable';
import { renderWithProviders } from '../../test-utils/TestProviders';

vi.mock('./VmActionsMenu', () => ({
  VmActionsMenu: () => <button type="button">Actions</button>,
}));

vi.mock('./VmInstanceTypeLabel', () => ({
  VmInstanceTypeLabel: ({
    instanceTypeId,
    instanceType,
  }: {
    instanceTypeId?: string;
    instanceType?: { metadata?: { name?: string } };
  }) => <span>{instanceType?.metadata?.name ?? instanceTypeId ?? '—'}</span>,
}));

const standardInstanceType = {
  id: 'standard-4-8',
  metadata: { name: 'Standard 4 vCPU / 8 GiB' },
} as InstanceType;

const runningVm: ComputeInstance = {
  $typeName: 'osac.public.v1.ComputeInstance',
  id: 'vm-1',
  metadata: {
    name: 'web-01',
    $typeName: 'osac.public.v1.Metadata',
    displayName: '',
    description: '',
    annotations: {},
    creator: 'foo',
    labels: {},
    project: 'foo',
    tenant: 'foo',
    version: 1,
    creationTimestamp: {
      $typeName: 'google.protobuf.Timestamp',
      seconds: 1767225600n,
      nanos: 0,
    },
  },
  spec: {
    $typeName: 'osac.public.v1.ComputeInstanceSpec',
    additionalDisks: {
      $typeName: 'osac.public.v1.ComputeInstanceDiskList',
      items: [],
    },
    catalogItem: create(ComputeInstanceCatalogItemReferenceSchema, { id: '' }),
    networkAttachments: [],
    template: create(ComputeInstanceTemplateReferenceSchema, { id: '' }),
    templateParameters: {},
    instanceType: create(InstanceTypeReferenceSchema, { id: 'standard-4-8' }),
    autoExternalIpAttachment: false,
  },
  status: {
    $typeName: 'osac.public.v1.ComputeInstanceStatus',
    conditions: [],
    state: ComputeInstanceState.RUNNING,
    internalIpAddress: '10.0.0.5',
    externalIpAddress: '203.0.113.1',
    computeNetworkAttachmentStatuses: [],
  },
};

const renderTable = (props: Partial<ComponentProps<typeof VmTable>> = {}) =>
  renderWithProviders(
    <VmTable vms={[runningVm]} instanceTypes={[standardInstanceType]} {...props} />,
  );

describe('VmTable', () => {
  it('renders updated column headers', () => {
    renderTable();

    expect(screen.getByRole('columnheader', { name: 'Name' })).toBeInTheDocument();
    expect(screen.getByRole('columnheader', { name: 'Status' })).toBeInTheDocument();
    expect(screen.getByRole('columnheader', { name: 'Instance type' })).toBeInTheDocument();
    expect(screen.getByRole('columnheader', { name: 'Internal IP' })).toBeInTheDocument();
    expect(screen.getByRole('columnheader', { name: 'External IP' })).toBeInTheDocument();
    expect(screen.getByRole('columnheader', { name: 'Created' })).toBeInTheDocument();
    expect(screen.queryByRole('columnheader', { name: 'vCPU' })).not.toBeInTheDocument();
    expect(screen.queryByRole('columnheader', { name: 'Memory' })).not.toBeInTheDocument();
    expect(screen.queryByRole('columnheader', { name: 'IP' })).not.toBeInTheDocument();
  });

  it('shows friendly instance type name, split IP addresses, and created timestamp', () => {
    renderTable();

    expect(screen.getByText('Standard 4 vCPU / 8 GiB')).toBeInTheDocument();
    expect(screen.getByText('10.0.0.5')).toBeInTheDocument();
    expect(screen.getByText('203.0.113.1')).toBeInTheDocument();
    expect(screen.getByRole('time')).toHaveAttribute('dateTime', '2026-01-01T00:00:00.000Z');
  });

  it('falls back to raw instance type id when lookup data is missing', () => {
    renderTable({ instanceTypes: [], isInstanceTypesLoading: false });

    expect(screen.getByText('standard-4-8')).toBeInTheDocument();
  });

  it('links the VM name to its details page', () => {
    renderTable();

    expect(screen.getByRole('link', { name: 'web-01' })).toHaveAttribute('href', '/vms/vm-1');
  });

  it('masks IPs and disables the name link while deleting', () => {
    const deletingVm = {
      ...runningVm,
      status: {
        ...runningVm.status,
        state: ComputeInstanceState.DELETING,
      },
    } as ComputeInstance;

    renderWithProviders(<VmTable vms={[deletingVm]} instanceTypes={[standardInstanceType]} />);

    expect(screen.queryByRole('link', { name: 'web-01' })).not.toBeInTheDocument();
    expect(screen.getAllByText('—')).toHaveLength(2);
  });
});
