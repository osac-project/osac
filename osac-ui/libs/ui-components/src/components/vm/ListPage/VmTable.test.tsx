import type { ComponentProps } from 'react';
import { create } from '@bufbuild/protobuf';
import { screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import type { ComputeInstance } from '@osac/types';
import {
  ComputeInstanceCatalogItemReferenceSchema,
  ComputeInstanceState,
  ComputeInstanceTemplateReferenceSchema,
  DiskImageReferenceSchema,
  InstanceTypeReferenceSchema,
} from '@osac/types';

import { VmTable } from './VmTable';
import { renderWithProviders } from '../../../test-utils/TestProviders';

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
    instanceType: create(InstanceTypeReferenceSchema, {
      id: 'standard-4-8',
      name: 'Standard 4 vCPU / 8 GiB',
    }),
    diskImage: create(DiskImageReferenceSchema, {
      id: 'img-1',
      name: 'rhel-9',
    }),
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
  renderWithProviders(<VmTable vms={[runningVm]} {...props} />);

describe('VmTable', () => {
  it('renders column headers', () => {
    renderTable();

    expect(screen.getByRole('columnheader', { name: 'Name' })).toBeInTheDocument();
    expect(screen.getByRole('columnheader', { name: 'Status' })).toBeInTheDocument();
    expect(screen.getByRole('columnheader', { name: 'Project' })).toBeInTheDocument();
    expect(screen.getByRole('columnheader', { name: 'Instance type' })).toBeInTheDocument();
    expect(screen.getByRole('columnheader', { name: 'Disk image' })).toBeInTheDocument();
    expect(screen.getByRole('columnheader', { name: 'External IP' })).toBeInTheDocument();
    expect(screen.getByRole('columnheader', { name: 'Created' })).toBeInTheDocument();
    expect(screen.getByRole('columnheader', { name: 'Actions' })).toBeInTheDocument();
    expect(screen.queryByRole('columnheader', { name: 'Internal IP' })).not.toBeInTheDocument();
    expect(screen.queryByRole('columnheader', { name: 'vCPU' })).not.toBeInTheDocument();
    expect(screen.queryByRole('columnheader', { name: 'Memory' })).not.toBeInTheDocument();
  });

  it('shows instance type, disk image, project, external IP, and created timestamp', () => {
    renderTable();

    expect(screen.getByText('Standard 4 vCPU / 8 GiB')).toBeInTheDocument();
    expect(screen.getByText('rhel-9')).toBeInTheDocument();
    expect(screen.getByText('foo')).toBeInTheDocument();
    expect(screen.getByText('203.0.113.1')).toBeInTheDocument();
    expect(screen.queryByText('10.0.0.5')).not.toBeInTheDocument();
    expect(screen.getByRole('time')).toHaveAttribute('dateTime', '2026-01-01T00:00:00.000Z');
  });

  it('shows a dash when instance type name is missing', () => {
    const vmWithoutInstanceTypeName = {
      ...runningVm,
      spec: {
        ...runningVm.spec,
        instanceType: create(InstanceTypeReferenceSchema, { id: 'standard-4-8' }),
      },
    } as ComputeInstance;

    renderWithProviders(<VmTable vms={[vmWithoutInstanceTypeName]} />);

    expect(screen.getByText('-')).toBeInTheDocument();
  });

  it('links the VM name to its details page', () => {
    renderTable();

    expect(screen.getByRole('link', { name: 'web-01' })).toHaveAttribute('href', '/vms/vm-1');
  });

  it('masks disk image and external IP and disables the name link while deleting', () => {
    const deletingVm = {
      ...runningVm,
      status: {
        ...runningVm.status,
        state: ComputeInstanceState.DELETING,
      },
    } as ComputeInstance;

    renderWithProviders(<VmTable vms={[deletingVm]} />);

    expect(screen.queryByRole('link', { name: 'web-01' })).not.toBeInTheDocument();
    expect(screen.queryByText('rhel-9')).not.toBeInTheDocument();
    expect(screen.queryByText('203.0.113.1')).not.toBeInTheDocument();
    expect(screen.getAllByText('—')).toHaveLength(2);
  });
});
