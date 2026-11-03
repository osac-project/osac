import { screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import type { ComputeInstance } from '@osac/types';

import VmNetworkingSection from './VmNetworkingSection';
import { renderWithProviders } from '../../../test-utils/TestProviders';

vi.mock('./useVmNetworkAttachmentRows', () => ({
  useVmNetworkAttachmentRows: vi.fn(),
}));

const { useVmNetworkAttachmentRows } = await import('./useVmNetworkAttachmentRows');

describe('VmNetworkingSection', () => {
  it('renders the read-only networking table including internal IP', () => {
    vi.mocked(useVmNetworkAttachmentRows).mockReturnValue([
      {
        virtualNetwork: { id: 'vn-1', name: 'prod-vn' },
        subnet: 'prod-subnet',
        securityGroups: [
          { id: 'sg-web', name: 'web-sg' },
          { id: 'sg-default', name: 'default-sg' },
        ],
        internalIp: '10.0.0.5',
      },
    ]);

    const { container } = renderWithProviders(
      <VmNetworkingSection vm={{ id: 'vm-1' } as ComputeInstance} />,
    );

    expect(screen.getByText('Networking')).toBeInTheDocument();
    expect(screen.getByRole('columnheader', { name: 'Virtual network' })).toBeInTheDocument();
    expect(screen.getByRole('columnheader', { name: 'Subnet' })).toBeInTheDocument();
    expect(screen.getByRole('columnheader', { name: 'Security groups' })).toBeInTheDocument();
    expect(screen.getByRole('columnheader', { name: 'Internal IP' })).toBeInTheDocument();
    expect(screen.getByRole('cell', { name: '10.0.0.5' })).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'prod-vn' })).toHaveAttribute(
      'href',
      '/networking/virtual-networks/vn-1',
    );
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
    expect(screen.queryByRole('button', { name: /add network/i })).not.toBeInTheDocument();
    expect(container.querySelector('.pf-v6-c-card')).toBeNull();
  });

  it('shows an empty state when there are no attachments', () => {
    vi.mocked(useVmNetworkAttachmentRows).mockReturnValue([]);

    renderWithProviders(<VmNetworkingSection vm={{ id: 'vm-1' } as ComputeInstance} />);

    expect(screen.getByText('No virtual networks configured.')).toBeInTheDocument();
  });
});
