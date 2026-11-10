import { Code, ConnectError } from '@connectrpc/connect';
import { screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import StorageBackendDeleteConfirmModal from './StorageBackendDeleteConfirmModal';
import { renderWithProviders } from '../../test-utils/TestProviders';

const mockBackend = {
  id: 'backend-1',
  metadata: { name: 'vast-prod' },
  spec: { provider: 'vast', endpoint: 'vast.example.com' },
};

describe('StorageBackendDeleteConfirmModal', () => {
  it('deletes the backend and calls onSuccess', async () => {
    const onStorageBackendDelete = vi.fn(() => ({}));
    const onSuccess = vi.fn();
    const { user } = renderWithProviders(
      <StorageBackendDeleteConfirmModal
        backend={mockBackend as never}
        onClose={vi.fn()}
        onSuccess={onSuccess}
      />,
      { transportOverrides: { onStorageBackendDelete } },
    );

    expect(screen.getByRole('dialog')).toBeInTheDocument();

    await user.click(screen.getByRole('button', { name: /^Delete$/i }));

    await waitFor(() => {
      expect(onStorageBackendDelete).toHaveBeenCalledWith(
        expect.objectContaining({ id: 'backend-1' }),
      );
      expect(onSuccess).toHaveBeenCalled();
    });
  });

  it('shows the referential-integrity error and does not call onSuccess', async () => {
    const onSuccess = vi.fn();
    const { user } = renderWithProviders(
      <StorageBackendDeleteConfirmModal
        backend={mockBackend as never}
        onClose={vi.fn()}
        onSuccess={onSuccess}
      />,
      {
        transportOverrides: {
          onStorageBackendDelete: () => {
            throw new ConnectError(
              'backend is referenced by an active storage tier',
              Code.FailedPrecondition,
            );
          },
        },
      },
    );

    await user.click(screen.getByRole('button', { name: /^Delete$/i }));

    expect(
      await screen.findByText(/backend is referenced by an active storage tier/i),
    ).toBeInTheDocument();
    expect(onSuccess).not.toHaveBeenCalled();
  });

  it('calls onClose when Cancel is clicked', async () => {
    const onClose = vi.fn();
    const { user } = renderWithProviders(
      <StorageBackendDeleteConfirmModal
        backend={mockBackend as never}
        onClose={onClose}
        onSuccess={vi.fn()}
      />,
    );

    await user.click(screen.getByRole('button', { name: /Cancel/i }));
    expect(onClose).toHaveBeenCalled();
  });
});
