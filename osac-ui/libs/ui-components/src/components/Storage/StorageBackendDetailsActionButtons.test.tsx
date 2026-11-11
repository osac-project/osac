import { screen, waitFor, within } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import type { StorageBackend } from '@osac/types/private';

import StorageBackendDetailsActionButtons from './StorageBackendDetailsActionButtons';
import { renderWithProviders } from '../../test-utils/TestProviders';

const mockNavigate = vi.fn();
vi.mock('react-router-dom', async (importOriginal) => {
  const actual = await importOriginal<typeof import('react-router-dom')>();
  return {
    ...actual,
    useNavigate: () => mockNavigate,
  };
});

const mockBackend = {
  id: 'backend-1',
  metadata: { name: 'vast-prod' },
  spec: { provider: 'vast', endpoint: 'vast.example.com' },
} as StorageBackend;

const renderButtons = () =>
  renderWithProviders(<StorageBackendDetailsActionButtons backend={mockBackend} />, {
    apiFixtures: { storageBackends: [mockBackend] },
  });

describe('StorageBackendDetailsActionButtons', () => {
  beforeEach(() => {
    mockNavigate.mockReset();
  });

  it('renders Edit and Delete buttons', () => {
    renderButtons();

    expect(screen.getByRole('button', { name: /^Edit$/i })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /^Delete$/i })).toBeInTheDocument();
  });

  it('navigates to the edit route when Edit is clicked', async () => {
    const { user } = renderButtons();

    await user.click(screen.getByRole('button', { name: /^Edit$/i }));

    expect(mockNavigate).toHaveBeenCalledWith(
      '/admin/infrastructure/storage/backends/backend-1/edit',
    );
  });

  it('opens the delete confirmation dialog when Delete is clicked', async () => {
    const { user } = renderButtons();

    await user.click(screen.getByRole('button', { name: /^Delete$/i }));

    expect(screen.getByRole('dialog')).toBeInTheDocument();
  });

  it('navigates to the backends list after a successful delete', async () => {
    const { user } = renderButtons();

    await user.click(screen.getByRole('button', { name: /^Delete$/i }));
    await user.click(within(screen.getByRole('dialog')).getByRole('button', { name: /^Delete$/i }));

    await waitFor(() => {
      expect(mockNavigate).toHaveBeenCalledWith('/admin/infrastructure/storage/backends');
    });
  });
});
