import { create } from '@bufbuild/protobuf';
import { Code, ConnectError } from '@connectrpc/connect';
import { screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { type Volume, VolumeSchema, VolumesDeleteResponseSchema } from '@osac/types';

import VolumeDeleteConfirmModal from './VolumeDeleteConfirmModal';
import { renderWithProviders } from '../../test-utils/TestProviders';

const mockNavigate = vi.fn();
vi.mock('react-router-dom', async (importOriginal) => {
  const actual = await importOriginal<typeof import('react-router-dom')>();
  return { ...actual, useNavigate: () => mockNavigate };
});

const mockVolume: Volume = create(VolumeSchema, {
  id: 'vol-1',
  metadata: { name: 'test-volume' },
});

describe('VolumeDeleteConfirmModal', () => {
  beforeEach(() => {
    mockNavigate.mockReset();
  });

  it('renders the warning text including "and all of its data"', () => {
    renderWithProviders(
      <VolumeDeleteConfirmModal volume={mockVolume} onClose={vi.fn()} onSuccess={vi.fn()} />,
    );

    expect(
      screen.getByText(
        'This permanently deletes the volume and all of its data. This action cannot be undone.',
      ),
    ).toBeInTheDocument();
  });

  it('navigates to the volumes list and shows a success toast on successful delete', async () => {
    let deleteCalled = false;
    const onSuccess = vi.fn();
    const { user } = renderWithProviders(
      <VolumeDeleteConfirmModal volume={mockVolume} onClose={vi.fn()} onSuccess={onSuccess} />,
      {
        transportOverrides: {
          onVolumeDelete: (req) => {
            deleteCalled = true;
            expect(req.id).toBe('vol-1');
            return create(VolumesDeleteResponseSchema);
          },
        },
      },
    );

    await user.click(screen.getByRole('button', { name: /^Delete$/i }));

    await waitFor(() => expect(onSuccess).toHaveBeenCalled());
    expect(deleteCalled).toBe(true);
    expect(mockNavigate).toHaveBeenCalledWith('/storage/volumes');
    expect(screen.getByText('Volume deleted')).toBeInTheDocument();
  });

  it('shows an inline error alert when delete fails', async () => {
    const onSuccess = vi.fn();
    const { user } = renderWithProviders(
      <VolumeDeleteConfirmModal volume={mockVolume} onClose={vi.fn()} onSuccess={onSuccess} />,
      {
        transportOverrides: {
          onVolumeDelete: () => {
            throw new ConnectError('volume is attached to instance vm-1', Code.FailedPrecondition);
          },
        },
      },
    );

    await user.click(screen.getByRole('button', { name: /^Delete$/i }));

    await waitFor(() => {
      expect(screen.getByText('Failed to delete volume')).toBeInTheDocument();
    });
    expect(screen.getByText('volume is attached to instance vm-1')).toBeInTheDocument();
    expect(screen.getByRole('dialog')).toBeInTheDocument();
    expect(onSuccess).not.toHaveBeenCalled();
  });

  it('calls onClose when Cancel is clicked', async () => {
    const onClose = vi.fn();
    const { user } = renderWithProviders(
      <VolumeDeleteConfirmModal volume={mockVolume} onClose={onClose} onSuccess={vi.fn()} />,
    );

    await user.click(screen.getByRole('button', { name: /Cancel/i }));
    expect(onClose).toHaveBeenCalled();
  });
});
