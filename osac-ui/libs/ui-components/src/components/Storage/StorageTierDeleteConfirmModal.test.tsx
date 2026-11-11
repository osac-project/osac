import { Code, ConnectError } from '@connectrpc/connect';
import { screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import StorageTierDeleteConfirmModal from './StorageTierDeleteConfirmModal';
import { renderWithProviders } from '../../test-utils/TestProviders';

const mockTier = {
  id: 'tier-1',
  metadata: { name: 'fast' },
  spec: { description: '', backends: [] },
};

describe('StorageTierDeleteConfirmModal', () => {
  it('deletes the tier and calls onSuccess', async () => {
    const onStorageTierDelete = vi.fn(() => ({}));
    const onSuccess = vi.fn();
    const { user } = renderWithProviders(
      <StorageTierDeleteConfirmModal
        tier={mockTier as never}
        onClose={vi.fn()}
        onSuccess={onSuccess}
      />,
      { transportOverrides: { onStorageTierDelete } },
    );

    expect(screen.getByRole('dialog')).toBeInTheDocument();

    await user.click(screen.getByRole('button', { name: /^Delete$/i }));

    await waitFor(() => {
      expect(onStorageTierDelete).toHaveBeenCalledWith(expect.objectContaining({ id: 'tier-1' }));
      expect(onSuccess).toHaveBeenCalled();
    });
  });

  it('shows the referential-integrity error and does not call onSuccess', async () => {
    const onSuccess = vi.fn();
    const { user } = renderWithProviders(
      <StorageTierDeleteConfirmModal
        tier={mockTier as never}
        onClose={vi.fn()}
        onSuccess={onSuccess}
      />,
      {
        transportOverrides: {
          onStorageTierDelete: () => {
            throw new ConnectError(
              'Storage tier is referenced by a Tenant',
              Code.FailedPrecondition,
            );
          },
        },
      },
    );

    await user.click(screen.getByRole('button', { name: /^Delete$/i }));

    expect(await screen.findByText('Storage tier is referenced by a Tenant')).toBeInTheDocument();
    expect(onSuccess).not.toHaveBeenCalled();
  });

  it('calls onClose when Cancel is clicked', async () => {
    const onClose = vi.fn();
    const { user } = renderWithProviders(
      <StorageTierDeleteConfirmModal
        tier={mockTier as never}
        onClose={onClose}
        onSuccess={vi.fn()}
      />,
    );

    await user.click(screen.getByRole('button', { name: /Cancel/i }));
    expect(onClose).toHaveBeenCalled();
  });
});
