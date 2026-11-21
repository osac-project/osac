import { create } from '@bufbuild/protobuf';
import { screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import {
  ComputeInstanceSchema,
  SecurityGroupSchema,
  SubnetSchema,
  VirtualNetworkSchema,
} from '@osac/types';

import VmNetworkingSection from './VmNetworkingSection';
import { renderWithProviders } from '../../../test-utils/TestProviders';

const vm = create(ComputeInstanceSchema, {
  id: 'vm-1',
  metadata: { name: 'web-01' },
  spec: {
    networkAttachments: [
      {
        subnet: { id: 'subnet-1' },
        securityGroups: [{ id: 'sg-web' }, { id: 'sg-default' }],
      },
    ],
  },
  status: {
    internalIpAddress: '10.0.0.5',
    computeNetworkAttachmentStatuses: [{ subnetRef: 'subnet-1', ipAddress: '10.0.0.5' }],
  },
});

const fixtures = {
  virtualNetworks: [create(VirtualNetworkSchema, { id: 'vn-1', metadata: { name: 'prod-vn' } })],
  subnets: [
    create(SubnetSchema, {
      id: 'subnet-1',
      metadata: { name: 'prod-subnet' },
      spec: { virtualNetwork: { id: 'vn-1' } },
    }),
  ],
  securityGroups: [
    create(SecurityGroupSchema, { id: 'sg-web', metadata: { name: 'web-sg' } }),
    create(SecurityGroupSchema, { id: 'sg-default', metadata: { name: 'default-sg' } }),
  ],
};

describe('VmNetworkingSection', () => {
  it('renders loaded attachment references and status addresses read-only', async () => {
    const { container } = renderWithProviders(<VmNetworkingSection vm={vm} />, {
      apiFixtures: fixtures,
    });

    expect(screen.getByText('Networking')).toBeInTheDocument();
    expect(screen.getByRole('columnheader', { name: 'Virtual network' })).toBeInTheDocument();
    expect(screen.getByRole('columnheader', { name: 'Subnet' })).toBeInTheDocument();
    expect(screen.getByRole('columnheader', { name: 'Security groups' })).toBeInTheDocument();
    expect(screen.getByRole('columnheader', { name: 'Internal IP' })).toBeInTheDocument();
    expect(await screen.findByRole('cell', { name: '10.0.0.5' })).toBeInTheDocument();
    expect(await screen.findByRole('link', { name: 'prod-vn' })).toHaveAttribute(
      'href',
      '/networking/virtual-networks/vn-1',
    );
    expect(screen.getByRole('cell', { name: 'prod-subnet' })).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'web-sg' })).toHaveAttribute(
      'href',
      '/networking/security-groups/sg-web',
    );
    expect(screen.getByRole('link', { name: 'default-sg' })).toHaveAttribute(
      'href',
      '/networking/security-groups/sg-default',
    );
    expect(screen.getByRole('grid', { name: 'Networking' })).toHaveClass(
      'osac-vm-networking-table',
    );
    expect(screen.queryByRole('button')).not.toBeInTheDocument();
    expect(screen.queryByRole('combobox')).not.toBeInTheDocument();
    expect(container.querySelector('.pf-v6-c-card')).toBeNull();
  });

  it('shows an empty state when there are no attachments', async () => {
    renderWithProviders(<VmNetworkingSection vm={create(ComputeInstanceSchema, { id: 'vm-1' })} />);

    expect(await screen.findByText('No virtual networks configured.')).toBeInTheDocument();
    expect(screen.queryByRole('button')).not.toBeInTheDocument();
  });
});
