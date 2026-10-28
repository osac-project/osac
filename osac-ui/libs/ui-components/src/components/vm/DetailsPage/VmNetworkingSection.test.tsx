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
        virtualNetwork: 'prod-vn',
        subnet: 'prod-subnet',
        securityGroups: 'web-sg, default-sg',
        internalIp: '10.0.0.5',
      },
    ]);

    renderWithProviders(<VmNetworkingSection vm={{ id: 'vm-1' } as ComputeInstance} />);

    expect(screen.getByText('Networking')).toBeInTheDocument();
    expect(screen.getByRole('columnheader', { name: 'Virtual network' })).toBeInTheDocument();
    expect(screen.getByRole('columnheader', { name: 'Subnet' })).toBeInTheDocument();
    expect(screen.getByRole('columnheader', { name: 'Security groups' })).toBeInTheDocument();
    expect(screen.getByRole('columnheader', { name: 'Internal IP' })).toBeInTheDocument();
    expect(screen.getByRole('cell', { name: '10.0.0.5' })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /add network/i })).not.toBeInTheDocument();
  });

  it('shows an empty state when there are no attachments', () => {
    vi.mocked(useVmNetworkAttachmentRows).mockReturnValue([]);

    renderWithProviders(<VmNetworkingSection vm={{ id: 'vm-1' } as ComputeInstance} />);

    expect(screen.getByText('No virtual networks configured.')).toBeInTheDocument();
  });
});
