import { screen } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import DiskImageCreatePage from './DiskImageCreatePage';
import { renderWithProviders } from '../../test-utils/TestProviders';

const mockNavigate = vi.fn();
vi.mock('react-router-dom', async (importOriginal) => {
  const actual = await importOriginal<typeof import('react-router-dom')>();
  return {
    ...actual,
    useNavigate: () => mockNavigate,
  };
});

describe('DiskImageCreatePage', () => {
  beforeEach(() => {
    mockNavigate.mockReset();
  });

  it('renders the create form', () => {
    renderWithProviders(<DiskImageCreatePage />, {
      routerEntries: ['/admin/infrastructure/disk-images/create'],
      session: { role: 'tenant-user', username: 'testuser', tenantId: 'tenant-1' },
    });

    expect(screen.getByText('Create disk image')).toBeInTheDocument();
    expect(screen.getByRole('textbox', { name: 'Name' })).toBeInTheDocument();
  });
});
