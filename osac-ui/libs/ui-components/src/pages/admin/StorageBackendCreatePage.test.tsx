import { Route, Routes } from 'react-router-dom';
import { create } from '@bufbuild/protobuf';
import { Code, ConnectError } from '@connectrpc/connect';
import { screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import {
  StorageBackendSchema,
  type StorageBackendsCreateRequest,
  type StorageBackendsCreateResponse,
  StorageBackendsCreateResponseSchema,
  type StorageBackendsUpdateRequest,
  StorageBackendsUpdateResponseSchema,
} from '@osac/types/private';

import { StorageBackendCreatePage } from './StorageBackendCreatePage';
import type {
  MockApiFixtures,
  MockTransportOverrides,
} from '../../test-utils/createMockConnectTransport';
import { renderWithProviders } from '../../test-utils/TestProviders';

const mockNavigate = vi.fn();
vi.mock('react-router-dom', async (importOriginal) => {
  const actual = await importOriginal<typeof import('react-router-dom')>();
  return {
    ...actual,
    useNavigate: () => mockNavigate,
    // useBlocker requires a data router; this test harness renders under a plain
    // MemoryRouter, so LeaveFormConfirmation's blocking behavior is stubbed out
    // rather than exercised here (its only other caller has no test coverage
    // for it either).
    useBlocker: () => ({ state: 'unblocked' as const }),
  };
});

const testBackendPassword = 'test-password';

const providerLabels: Record<string, string> = {
  vast: 'VAST Data',
  netapp: 'NetApp ONTAP',
  'pure-flashblade': 'Pure FlashBlade',
  'pure-flasharray': 'Pure FlashArray',
};

const renderCreatePage = (overrides?: MockTransportOverrides) =>
  renderWithProviders(<StorageBackendCreatePage />, {
    transportOverrides: overrides,
  });

const EDIT_ROUTE_PATH = '/admin/infrastructure/storage/backends/:id/edit';
const EDIT_PATH = '/admin/infrastructure/storage/backends/b-1/edit';

const existingBackend = create(StorageBackendSchema, {
  id: 'b-1',
  metadata: { name: 'vast-prod-1', version: 7 },
  spec: {
    provider: 'vast',
    endpoint: 'vast.example.com:443',
    description: 'primary array',
    credentials: { username: 'test-existing-admin', password: 'test-existing-secret' },
  },
});

const renderEditPage = (overrides?: MockTransportOverrides, apiFixtures?: MockApiFixtures) =>
  renderWithProviders(
    <Routes>
      <Route path={EDIT_ROUTE_PATH} element={<StorageBackendCreatePage />} />
    </Routes>,
    {
      routerEntries: [EDIT_PATH],
      apiFixtures: { storageBackends: [existingBackend], ...apiFixtures },
      transportOverrides: overrides,
    },
  );

describe('StorageBackendCreatePage', () => {
  beforeEach(() => {
    mockNavigate.mockReset();
  });

  describe('create mode', () => {
    const fillValidForm = async (
      user: ReturnType<typeof renderCreatePage>['user'],
      provider = 'vast',
    ) => {
      await user.type(screen.getByRole('textbox', { name: 'Name' }), 'vast-prod-1');
      await user.click(screen.getByLabelText(/^Provider/));
      await user.click(screen.getByRole('option', { name: providerLabels[provider] }));
      await user.type(screen.getByRole('textbox', { name: 'Endpoint' }), 'vast.example.com:443');
      await user.type(screen.getByLabelText(/^Username/), 'test-admin');
      await user.type(screen.getByLabelText(/^Password/), testBackendPassword);
    };

    it('renders the page title, breadcrumb, and all fields', () => {
      renderCreatePage();

      expect(screen.getByRole('heading', { name: 'Create storage backend' })).toBeInTheDocument();
      expect(screen.getByRole('button', { name: 'Storage Backends' })).toBeInTheDocument();
      expect(screen.getByRole('textbox', { name: 'Name' })).toBeInTheDocument();
      expect(screen.getByLabelText(/^Provider/)).toBeInTheDocument();
      expect(screen.getByRole('textbox', { name: 'Endpoint' })).toBeInTheDocument();
      expect(screen.getByRole('textbox', { name: 'Description' })).toBeInTheDocument();
      expect(screen.getByLabelText(/^Username/)).toBeInTheDocument();
      expect(screen.getByLabelText(/^Password/)).toBeInTheDocument();
      expect(screen.getByRole('button', { name: 'Create' })).toBeInTheDocument();
      expect(screen.getByRole('button', { name: 'Cancel' })).toBeInTheDocument();
    });

    it('renders name and provider as enabled', () => {
      renderCreatePage();

      expect(screen.getByRole('textbox', { name: 'Name' })).toBeEnabled();
      expect(screen.getByLabelText(/^Provider/)).toBeEnabled();
    });

    it('offers only the supported product choices', async () => {
      const { user } = renderCreatePage();

      await user.click(screen.getByLabelText(/^Provider/));

      expect(screen.getAllByRole('option').map((option) => option.textContent)).toEqual([
        'VAST Data',
        'NetApp ONTAP',
        'Pure FlashBlade',
        'Pure FlashArray',
      ]);
    });

    it.each(['vast', 'netapp', 'pure-flashblade', 'pure-flasharray'])(
      'submits the selected product routing key "%s"',
      async (provider) => {
        let capturedRequest: StorageBackendsCreateRequest | undefined;
        const { user } = renderCreatePage({
          onStorageBackendCreate: (req) => {
            capturedRequest = req;
            return create(StorageBackendsCreateResponseSchema, {
              object: { id: 'new-backend-1' },
            });
          },
        });

        await fillValidForm(user, provider);
        await user.click(screen.getByRole('button', { name: 'Create' }));

        await waitFor(() => {
          expect(capturedRequest?.object?.spec?.provider).toBe(provider);
        });
      },
    );

    it('renders the password field as a masked input', () => {
      renderCreatePage();

      const passwordField = screen.getByLabelText(/^Password/);
      expect(passwordField).toHaveAttribute('type', 'password');
    });

    it('shows a DNS-label validation error for an invalid name and does not submit', async () => {
      const onStorageBackendCreate = vi.fn();
      const { user } = renderCreatePage({ onStorageBackendCreate });

      await user.type(screen.getByRole('textbox', { name: 'Name' }), 'Invalid_Name');
      await user.click(screen.getByRole('button', { name: 'Create' }));

      await waitFor(() => {
        expect(
          screen.getByText(
            'Name must only contain lowercase letters (a-z), digits (0-9), and hyphens (-)',
          ),
        ).toBeInTheDocument();
      });
      expect(onStorageBackendCreate).not.toHaveBeenCalled();
      expect(mockNavigate).not.toHaveBeenCalled();
    });

    it('shows required-field validation errors for endpoint, username, and password', async () => {
      const onStorageBackendCreate = vi.fn();
      const { user } = renderCreatePage({ onStorageBackendCreate });

      await user.type(screen.getByRole('textbox', { name: 'Name' }), 'vast-prod-1');
      await user.click(screen.getByLabelText(/^Provider/));
      await user.click(screen.getByRole('option', { name: 'VAST Data' }));
      await user.click(screen.getByRole('button', { name: 'Create' }));

      await waitFor(() => {
        expect(screen.getByText('Endpoint is required')).toBeInTheDocument();
      });
      expect(screen.getByText('Username is required')).toBeInTheDocument();
      expect(screen.getByText('Password is required')).toBeInTheDocument();
      expect(onStorageBackendCreate).not.toHaveBeenCalled();
    });

    it('requires a provider/product key when the field is empty', async () => {
      const onStorageBackendCreate = vi.fn();
      const { user } = renderCreatePage({ onStorageBackendCreate });

      await user.type(screen.getByRole('textbox', { name: 'Name' }), 'vast-prod-1');
      await user.type(screen.getByRole('textbox', { name: 'Endpoint' }), 'vast.example.com:443');
      await user.type(screen.getByLabelText(/^Username/), 'test-admin');
      await user.type(screen.getByLabelText(/^Password/), testBackendPassword);
      await user.click(screen.getByRole('button', { name: 'Create' }));

      await waitFor(() => {
        expect(screen.getByText('Provider or product key is required')).toBeInTheDocument();
      });
      expect(onStorageBackendCreate).not.toHaveBeenCalled();
    });

    it('disables Create while the submission is pending, to prevent duplicate submissions', async () => {
      let resolveCreate: (() => void) | undefined;
      const onStorageBackendCreate = () =>
        new Promise<StorageBackendsCreateResponse>((resolve) => {
          resolveCreate = () =>
            resolve(
              create(StorageBackendsCreateResponseSchema, { object: { id: 'new-backend-1' } }),
            );
        });

      const { user } = renderCreatePage({ onStorageBackendCreate });

      await fillValidForm(user);
      await user.click(screen.getByRole('button', { name: 'Create' }));

      // Once isLoading is true, PatternFly's Spinner contributes its own
      // "Contents" accessible name to the button, so an exact "Create" match
      // no longer resolves — match by substring instead (same pattern already
      // used elsewhere in this file for accessible-name additions).
      await waitFor(() => {
        expect(screen.getByRole('button', { name: /Create/ })).toBeDisabled();
      });
      expect(screen.getByRole('button', { name: 'Cancel' })).toBeDisabled();

      resolveCreate?.();

      await waitFor(() => {
        expect(mockNavigate).toHaveBeenCalledWith(
          '/admin/infrastructure/storage/backends/new-backend-1',
        );
      });
    }, 15000);

    it('submits the expected payload and navigates to the created backend details page on success', async () => {
      let capturedRequest: StorageBackendsCreateRequest | undefined;
      const { user } = renderCreatePage({
        onStorageBackendCreate: (req) => {
          capturedRequest = req;
          return create(StorageBackendsCreateResponseSchema, {
            object: {
              id: 'new-backend-1',
              metadata: req.object?.metadata,
              spec: req.object?.spec,
            },
          });
        },
      });

      await fillValidForm(user);
      await user.click(screen.getByRole('button', { name: 'Create' }));

      await waitFor(() => {
        expect(mockNavigate).toHaveBeenCalledWith(
          '/admin/infrastructure/storage/backends/new-backend-1',
        );
      });

      expect(capturedRequest?.object?.metadata?.name).toBe('vast-prod-1');
      expect(capturedRequest?.object?.spec?.provider).toBe('vast');
      expect(capturedRequest?.object?.spec?.endpoint).toBe('vast.example.com:443');
      expect(capturedRequest?.object?.spec?.credentials?.username).toBe('test-admin');
      expect(capturedRequest?.object?.spec?.credentials?.password).toBe(testBackendPassword);
    }, 15000);

    it('shows a form-level error and does not navigate when the name already exists', async () => {
      const { user } = renderCreatePage({
        onStorageBackendCreate: () => {
          throw new ConnectError('Storage backend name already exists', Code.AlreadyExists);
        },
      });

      await fillValidForm(user);
      await user.click(screen.getByRole('button', { name: 'Create' }));

      await waitFor(() => {
        expect(screen.getByText('Failed to create storage backend')).toBeInTheDocument();
      });
      expect(screen.getByText('Storage backend name already exists')).toBeInTheDocument();
      expect(mockNavigate).not.toHaveBeenCalled();
    }, 15000);

    it('navigates back to the backends list on cancel', async () => {
      const { user } = renderCreatePage();

      await user.click(screen.getByRole('button', { name: 'Cancel' }));

      expect(mockNavigate).toHaveBeenCalledWith('/admin/infrastructure/storage/backends');
    });

    it('navigates back to the backends list via breadcrumb', async () => {
      const { user } = renderCreatePage();

      await user.click(screen.getByRole('button', { name: 'Storage Backends' }));

      expect(mockNavigate).toHaveBeenCalledWith('/admin/infrastructure/storage/backends');
    });
  });

  describe('edit mode', () => {
    it('shows a loading spinner before the backend has been fetched', () => {
      renderEditPage();

      expect(screen.getByRole('progressbar')).toBeInTheDocument();
    });

    it('renders the page prefilled with the backend endpoint and description', async () => {
      renderEditPage();

      await waitFor(() => {
        expect(screen.getByRole('heading', { name: 'Edit storage backend' })).toBeInTheDocument();
      });
      expect(screen.getByRole('textbox', { name: 'Endpoint' })).toHaveValue('vast.example.com:443');
      expect(screen.getByRole('textbox', { name: 'Description' })).toHaveValue('primary array');
      expect(screen.getByRole('textbox', { name: 'Name' })).toHaveValue('vast-prod-1');
      expect(screen.getByLabelText(/^Provider/)).toHaveTextContent('VAST Data');
    });

    it('renders name and provider as disabled', async () => {
      renderEditPage();

      await waitFor(() => {
        expect(screen.getByRole('textbox', { name: 'Name' })).toBeInTheDocument();
      });
      expect(screen.getByRole('textbox', { name: 'Name' })).toBeDisabled();
      expect(screen.getByLabelText(/^Provider/)).toBeDisabled();
    });

    it('renders credential fields blank regardless of the fetched record', async () => {
      renderEditPage();

      await waitFor(() => {
        expect(screen.getByLabelText(/^Username/)).toBeInTheDocument();
      });
      expect(screen.getByLabelText(/^Username/)).toHaveValue('');
      expect(screen.getByLabelText(/^Password/)).toHaveValue('');
    });

    it('shows a validation error and does not submit when only username is filled', async () => {
      const onStorageBackendUpdate = vi.fn();
      const { user } = renderEditPage({ onStorageBackendUpdate });

      await waitFor(() => {
        expect(screen.getByLabelText(/^Username/)).toBeInTheDocument();
      });
      await user.type(screen.getByLabelText(/^Username/), 'test-new-admin');
      await user.click(screen.getByRole('button', { name: 'Save' }));

      await waitFor(() => {
        expect(
          screen.getAllByText('Enter both username and password, or leave both blank').length,
        ).toBeGreaterThan(0);
      });
      expect(onStorageBackendUpdate).not.toHaveBeenCalled();
      expect(mockNavigate).not.toHaveBeenCalled();
    });

    it('shows a validation error and does not submit when only password is filled', async () => {
      const onStorageBackendUpdate = vi.fn();
      const { user } = renderEditPage({ onStorageBackendUpdate });

      await waitFor(() => {
        expect(screen.getByLabelText(/^Password/)).toBeInTheDocument();
      });
      await user.type(screen.getByLabelText(/^Password/), 'test-new-secret');
      await user.click(screen.getByRole('button', { name: 'Save' }));

      await waitFor(() => {
        expect(
          screen.getAllByText('Enter both username and password, or leave both blank').length,
        ).toBeGreaterThan(0);
      });
      expect(onStorageBackendUpdate).not.toHaveBeenCalled();
      expect(mockNavigate).not.toHaveBeenCalled();
    });

    it('omits credentials and never submits name/provider when both credential fields are left blank', async () => {
      let capturedRequest: StorageBackendsUpdateRequest | undefined;
      const { user } = renderEditPage({
        onStorageBackendUpdate: (req) => {
          capturedRequest = req;
          return create(StorageBackendsUpdateResponseSchema, { object: existingBackend });
        },
      });

      await waitFor(() => {
        expect(screen.getByRole('textbox', { name: 'Endpoint' })).toHaveValue(
          'vast.example.com:443',
        );
      });
      await user.clear(screen.getByRole('textbox', { name: 'Endpoint' }));
      await user.type(screen.getByRole('textbox', { name: 'Endpoint' }), 'new.example.com:443');
      await user.click(screen.getByRole('button', { name: 'Save' }));

      await waitFor(() => {
        expect(mockNavigate).toHaveBeenCalledWith('/admin/infrastructure/storage/backends');
      });
      expect(capturedRequest?.object?.spec?.endpoint).toBe('new.example.com:443');
      expect(capturedRequest?.object?.spec?.credentials).toBeUndefined();
      expect(capturedRequest?.object?.metadata?.name).toBe('');
      expect(capturedRequest?.updateMask?.paths).not.toContain('spec.provider');
      expect(capturedRequest?.updateMask?.paths).not.toContain('metadata.name');
      expect(capturedRequest?.updateMask?.paths).not.toContain('spec.credentials');
      expect(capturedRequest?.lock).toBe(true);
      expect(capturedRequest?.object?.metadata?.version).toBe(7);
    });

    it('submits a complete credentials object when both fields are filled', async () => {
      let capturedRequest: StorageBackendsUpdateRequest | undefined;
      const { user } = renderEditPage({
        onStorageBackendUpdate: (req) => {
          capturedRequest = req;
          return create(StorageBackendsUpdateResponseSchema, { object: existingBackend });
        },
      });

      await waitFor(() => {
        expect(screen.getByLabelText(/^Username/)).toBeInTheDocument();
      });
      await user.type(screen.getByLabelText(/^Username/), 'test-new-admin');
      await user.type(screen.getByLabelText(/^Password/), 'test-new-secret');
      await user.click(screen.getByRole('button', { name: 'Save' }));

      await waitFor(() => {
        expect(mockNavigate).toHaveBeenCalledWith('/admin/infrastructure/storage/backends');
      });
      expect(capturedRequest?.object?.spec?.credentials).toMatchObject({
        username: 'test-new-admin',
        password: 'test-new-secret',
      });
    });

    it('shows a submission error and does not navigate on a stale-version conflict', async () => {
      const { user } = renderEditPage({
        onStorageBackendUpdate: () => {
          throw new ConnectError('Storage backend was modified by another request', Code.Aborted);
        },
      });

      await waitFor(() => {
        expect(screen.getByRole('textbox', { name: 'Endpoint' })).toHaveValue(
          'vast.example.com:443',
        );
      });
      await user.click(screen.getByRole('button', { name: 'Save' }));

      await waitFor(() => {
        expect(screen.getByText('Failed to update storage backend')).toBeInTheDocument();
      });
      expect(
        screen.getByText('Storage backend was modified by another request'),
      ).toBeInTheDocument();
      expect(mockNavigate).not.toHaveBeenCalled();
    });

    it('navigates back to the backends list on cancel', async () => {
      const { user } = renderEditPage();

      await waitFor(() => {
        expect(screen.getByRole('button', { name: 'Cancel' })).toBeInTheDocument();
      });
      await user.click(screen.getByRole('button', { name: 'Cancel' }));

      expect(mockNavigate).toHaveBeenCalledWith('/admin/infrastructure/storage/backends');
    });
  });
});
