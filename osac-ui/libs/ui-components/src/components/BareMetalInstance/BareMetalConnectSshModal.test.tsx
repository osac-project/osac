import { screen, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import BareMetalConnectSshModal from './BareMetalConnectSshModal';
import { renderWithProviders } from '../../test-utils/TestProviders';

describe('BareMetalConnectSshModal', () => {
  it('shows host and SSH command with copy control', async () => {
    const onClose = vi.fn();
    const { user } = renderWithProviders(
      <BareMetalConnectSshModal host="203.0.113.23" username="cloud-user" onClose={onClose} />,
    );

    expect(screen.getByRole('dialog', { name: 'Connect via SSH' })).toBeInTheDocument();
    expect(
      screen.getByText(
        'Use the SSH public key from launch to connect to this host when it is running.',
      ),
    ).toBeInTheDocument();
    expect(screen.getByText('203.0.113.23')).toBeInTheDocument();
    expect(screen.getByDisplayValue('ssh cloud-user@203.0.113.23')).toBeInTheDocument();

    const dialog = screen.getByRole('dialog', { name: 'Connect via SSH' });
    const footer = dialog.querySelector('footer');
    expect(footer).not.toBeNull();
    await user.click(within(footer as HTMLElement).getByRole('button', { name: 'Close' }));
    expect(onClose).toHaveBeenCalled();
  });
});
