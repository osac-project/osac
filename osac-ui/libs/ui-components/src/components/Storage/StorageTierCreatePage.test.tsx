import { Route, Routes } from 'react-router-dom';
import { create } from '@bufbuild/protobuf';
import { Code, ConnectError } from '@connectrpc/connect';
import { screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import type { StorageBackend, StorageTier, StorageTiersCreateResponse } from '@osac/types/private';
import {
  StorageBackendState,
  StorageProtocol,
  StorageTierState,
  StorageTiersCreateResponseSchema,
} from '@osac/types/private';

import StorageTierCreatePage from './StorageTierCreatePage';
import {
  type RenderWithProvidersOptions,
  renderWithProviders,
} from '../../test-utils/TestProviders';

const mockNavigate = vi.fn();
vi.mock('react-router-dom', async (importOriginal) => {
  const actual = await importOriginal<typeof import('react-router-dom')>();
  return {
    ...actual,
    useNavigate: () => mockNavigate,
    // useBlocker requires a data router; this test harness renders under a plain
    // MemoryRouter, so LeaveFormConfirmation's blocking behavior is stubbed out
    // rather than exercised here (see StorageBackendCreatePage.test.tsx for the
    // same pattern).
    useBlocker: () => ({ state: 'unblocked' as const }),
  };
});

const readyBackend = {
  id: 'backend-1',
  metadata: { name: 'fast-nvme' },
  spec: { provider: 'vast', endpoint: 'vast.example.com' },
  status: { state: StorageBackendState.READY },
} as StorageBackend;

const notReadyBackend = {
  id: 'backend-2',
  metadata: { name: 'not-ready-backend' },
  spec: { provider: 'ceph', endpoint: 'ceph.example.com' },
  status: { state: StorageBackendState.UNSPECIFIED },
} as StorageBackend;

const renderCreatePage = (options: RenderWithProvidersOptions = {}) =>
  renderWithProviders(<StorageTierCreatePage />, options);

const fillValidForm = async (user: ReturnType<typeof renderCreatePage>['user']) => {
  await user.type(screen.getByRole('textbox', { name: 'Name' }), 'fast-tier');
  await user.click(screen.getByLabelText(/^Backend/));
  await user.click(screen.getByRole('option', { name: 'fast-nvme' }));
  await user.click(screen.getByRole('radio', { name: 'NFS' }));
};

const EDIT_ROUTE_PATH = '/admin/infrastructure/storage/tiers/:id/edit';
const EDIT_PATH = '/admin/infrastructure/storage/tiers/tier-1/edit';

const assignedNonReadyBackend = {
  id: 'backend-2',
  metadata: { name: 'legacy-backend' },
  spec: { provider: 'ceph', endpoint: 'ceph.example.com' },
  status: { state: StorageBackendState.UNSPECIFIED },
} as StorageBackend;

const existingTier = {
  id: 'tier-1',
  metadata: { name: 'fast-tier', version: 5 },
  spec: {
    description: 'fast storage',
    protocol: StorageProtocol.NFS,
    backends: [
      {
        backendId: 'backend-2',
        maxReadBandwidthMbs: 100,
        maxWriteBandwidthMbs: 80,
        encryptionEnabled: false,
      },
    ],
  },
  status: { state: StorageTierState.ACTIVE },
} as StorageTier;

const renderEditPage = (options: RenderWithProvidersOptions = {}) =>
  renderWithProviders(
    <Routes>
      <Route path={EDIT_ROUTE_PATH} element={<StorageTierCreatePage />} />
    </Routes>,
    {
      routerEntries: [EDIT_PATH],
      apiFixtures: {
        storageTiers: [existingTier],
        storageBackends: [readyBackend, assignedNonReadyBackend],
      },
      ...options,
    },
  );

describe('StorageTierCreatePage', () => {
  beforeEach(() => {
    mockNavigate.mockReset();
  });

  describe('create mode', () => {
    it('renders the page title, fields, and actions', () => {
      renderCreatePage({ apiFixtures: { storageBackends: [readyBackend] } });

      expect(screen.getByRole('heading', { name: 'Create storage tier' })).toBeInTheDocument();
      expect(screen.getByRole('textbox', { name: 'Name' })).toBeInTheDocument();
      expect(screen.getByRole('textbox', { name: 'Description' })).toBeInTheDocument();
      expect(screen.getByLabelText(/^Backend/)).toBeInTheDocument();
      expect(screen.getByRole('radio', { name: 'NFS' })).toBeInTheDocument();
      expect(screen.getByRole('radio', { name: 'Block' })).toBeInTheDocument();
      expect(screen.getByRole('button', { name: 'Create' })).toBeInTheDocument();
      expect(screen.getByRole('button', { name: 'Cancel' })).toBeInTheDocument();
    });

    it('renders the name field enabled', () => {
      renderCreatePage({ apiFixtures: { storageBackends: [readyBackend] } });

      expect(screen.getByRole('textbox', { name: 'Name' })).toBeEnabled();
    });

    it('does not render an add-another-backend control', () => {
      renderCreatePage({ apiFixtures: { storageBackends: [readyBackend] } });

      expect(screen.queryByRole('button', { name: /add.*backend/i })).not.toBeInTheDocument();
    });

    it('only offers READY backends as options', async () => {
      const { user } = renderCreatePage({
        apiFixtures: { storageBackends: [readyBackend, notReadyBackend] },
      });

      await user.click(screen.getByLabelText(/^Backend/));

      expect(screen.getByRole('option', { name: 'fast-nvme' })).toBeInTheDocument();
      expect(screen.queryByRole('option', { name: 'not-ready-backend' })).not.toBeInTheDocument();
    });

    it('shows the loading placeholder while backends are loading', () => {
      renderCreatePage({ apiFixtures: { storageBackends: [readyBackend] } });

      expect(screen.getByLabelText(/^Backend/)).toHaveTextContent('Loading...');
    });

    it('offers no selectable backends when none are READY', async () => {
      const { user } = renderCreatePage({
        apiFixtures: { storageBackends: [notReadyBackend] },
      });

      await user.click(screen.getByLabelText(/^Backend/));

      expect(screen.queryAllByRole('option')).toHaveLength(0);
    });

    it('rejects a name that is not a valid DNS label', async () => {
      const { user } = renderCreatePage({ apiFixtures: { storageBackends: [readyBackend] } });

      await user.type(screen.getByRole('textbox', { name: 'Name' }), 'Invalid_Name');
      await user.click(screen.getByRole('button', { name: 'Create' }));

      await waitFor(() => {
        expect(
          screen.getByText(
            'Name must only contain lowercase letters (a-z), digits (0-9), and hyphens (-)',
          ),
        ).toBeInTheDocument();
      });
    });

    it('submits the protocol and backend association, navigating to the created tier details page on success', async () => {
      const onStorageTierCreate = vi.fn(
        (req: { object?: { metadata?: unknown; spec?: unknown } }) => ({
          object: {
            id: 'new-tier-1',
            metadata: req.object?.metadata,
            spec: req.object?.spec,
            status: { state: 0 },
          },
        }),
      );

      const { user } = renderCreatePage({
        apiFixtures: { storageBackends: [readyBackend] },
        transportOverrides: { onStorageTierCreate: onStorageTierCreate as never },
      });

      await fillValidForm(user);
      await user.click(screen.getByRole('button', { name: 'Create' }));

      await waitFor(() => {
        expect(onStorageTierCreate).toHaveBeenCalledTimes(1);
      });

      const request = onStorageTierCreate.mock.calls[0][0];
      expect(request.object?.metadata).toMatchObject({ name: 'fast-tier' });
      const spec = request.object?.spec as {
        protocol?: StorageProtocol;
        backends?: unknown[];
      };
      expect(spec).toMatchObject({
        protocol: StorageProtocol.NFS,
      });
      expect(Array.isArray(spec.backends)).toBe(true);
      expect(spec.backends).toHaveLength(1);
      expect(spec.backends?.[0]).toMatchObject({
        backendId: 'backend-1',
      });
      expect(spec.backends?.[0]).not.toHaveProperty('protocol');
      expect(spec.backends?.[0]).not.toHaveProperty('quotaGib');

      await waitFor(() => {
        expect(mockNavigate).toHaveBeenCalledWith('/admin/infrastructure/storage/tiers/new-tier-1');
      });
    });

    it('disables Create while the submission is pending, to prevent duplicate submissions', async () => {
      let resolveCreate: (() => void) | undefined;
      const onStorageTierCreate = () =>
        new Promise<StorageTiersCreateResponse>((resolve) => {
          resolveCreate = () =>
            resolve(create(StorageTiersCreateResponseSchema, { object: { id: 'new-tier-1' } }));
        });

      const { user } = renderCreatePage({
        apiFixtures: { storageBackends: [readyBackend] },
        transportOverrides: { onStorageTierCreate },
      });

      await fillValidForm(user);
      await user.click(screen.getByRole('button', { name: 'Create' }));

      // Once isLoading is true, PatternFly's Spinner contributes its own "Contents"
      // accessible name to the button, so an exact "Create" match no longer
      // resolves — match by substring instead.
      await waitFor(() => {
        expect(screen.getByRole('button', { name: /Create/ })).toBeDisabled();
      });
      expect(screen.getByRole('button', { name: 'Cancel' })).toBeDisabled();

      resolveCreate?.();

      await waitFor(() => {
        expect(mockNavigate).toHaveBeenCalledWith('/admin/infrastructure/storage/tiers/new-tier-1');
      });
    }, 15000);

    it('shows the ALREADY_EXISTS error as a form-level error without navigating away', async () => {
      const { user } = renderCreatePage({
        apiFixtures: { storageBackends: [readyBackend] },
        transportOverrides: {
          onStorageTierCreate: () => {
            throw new ConnectError('Storage tier name already exists', Code.AlreadyExists);
          },
        },
      });

      await fillValidForm(user);
      await user.click(screen.getByRole('button', { name: 'Create' }));

      await waitFor(() => {
        expect(screen.getByText('Storage tier name already exists')).toBeInTheDocument();
      });
      expect(mockNavigate).not.toHaveBeenCalled();
    });

    it('shows the NOT_FOUND error as a form-level error without navigating away', async () => {
      const { user } = renderCreatePage({
        apiFixtures: { storageBackends: [readyBackend] },
        transportOverrides: {
          onStorageTierCreate: () => {
            throw new ConnectError('Storage backend backend-1 not found', Code.NotFound);
          },
        },
      });

      await fillValidForm(user);
      await user.click(screen.getByRole('button', { name: 'Create' }));

      await waitFor(() => {
        expect(screen.getByText('Storage backend backend-1 not found')).toBeInTheDocument();
      });
      expect(mockNavigate).not.toHaveBeenCalled();
    });
  });

  describe('edit mode', () => {
    it('pre-fills the form with the tier’s current values', async () => {
      renderEditPage();

      expect(await screen.findByRole('textbox', { name: 'Name' })).toHaveValue('fast-tier');
      expect(screen.getByRole('textbox', { name: 'Description' })).toHaveValue('fast storage');
      await waitFor(() => {
        expect(screen.getByLabelText(/^Backend/)).toHaveTextContent('legacy-backend');
      });
      expect(screen.getByRole('radio', { name: 'NFS' })).toBeChecked();
    });

    it('renders the name field disabled', async () => {
      renderEditPage();

      expect(await screen.findByRole('textbox', { name: 'Name' })).toBeDisabled();
    });

    it('includes the tier’s own non-READY currently-assigned backend in the picker', async () => {
      const { user } = renderEditPage();

      await user.click(await screen.findByLabelText(/^Backend/));

      expect(await screen.findByRole('option', { name: 'fast-nvme' })).toBeInTheDocument();
      expect(await screen.findByRole('option', { name: 'legacy-backend' })).toBeInTheDocument();
    });

    it('does not show the QoS-change alert when only description changes', async () => {
      const { user } = renderEditPage();

      const descriptionInput = await screen.findByRole('textbox', { name: 'Description' });
      expect(screen.queryByText(/StorageClass/)).not.toBeInTheDocument();

      await user.clear(descriptionInput);
      await user.type(descriptionInput, 'updated description');

      expect(screen.queryByText(/StorageClass/)).not.toBeInTheDocument();
    });

    it('shows the QoS-change alert when the protocol changes', async () => {
      const { user } = renderEditPage();

      await screen.findByRole('radio', { name: 'NFS' });
      await user.click(screen.getByRole('radio', { name: 'Block' }));

      expect(screen.getByText(/StorageClass/)).toBeInTheDocument();
    });

    it('submits spec.backends and spec.description together when the description changes', async () => {
      const onStorageTierUpdate = vi.fn(
        (req: { object?: { metadata?: unknown; spec?: unknown } }) => ({
          object: {
            ...existingTier,
            spec: { ...existingTier.spec, ...(req.object?.spec as object) },
          },
        }),
      );
      const { user } = renderEditPage({
        transportOverrides: { onStorageTierUpdate: onStorageTierUpdate as never },
      });

      const descriptionInput = await screen.findByRole('textbox', { name: 'Description' });
      await user.clear(descriptionInput);
      await user.type(descriptionInput, 'updated description');
      await user.click(screen.getByRole('button', { name: 'Save' }));

      await waitFor(() => expect(onStorageTierUpdate).toHaveBeenCalledTimes(1));
      const request = onStorageTierUpdate.mock.calls[0][0] as {
        object?: {
          spec?: { description?: string; backends?: unknown; protocol?: unknown };
          metadata?: unknown;
        };
        updateMask?: { paths?: string[] };
        lock?: boolean;
      };
      expect(request.object?.spec?.description).toBe('updated description');
      expect(request.object?.spec?.backends).toMatchObject([{ backendId: 'backend-2' }]);
      expect(request.updateMask?.paths).toEqual([
        'spec.protocol',
        'spec.backends',
        'spec.description',
      ]);
      expect(request.lock).toBe(true);
      expect(request.object?.metadata).toMatchObject({ version: 5 });

      await waitFor(() =>
        expect(mockNavigate).toHaveBeenCalledWith('/admin/infrastructure/storage/tiers'),
      );
    });

    it('submits protocol and spec.backends together, masked accordingly, when the protocol changes', async () => {
      const onStorageTierUpdate = vi.fn((_req: { object?: unknown; updateMask?: unknown }) => ({
        object: { ...existingTier },
      }));
      const { user } = renderEditPage({
        transportOverrides: { onStorageTierUpdate: onStorageTierUpdate as never },
      });

      await screen.findByRole('radio', { name: 'NFS' });
      await user.click(screen.getByRole('radio', { name: 'Block' }));
      await user.click(screen.getByRole('button', { name: 'Save' }));

      await waitFor(() => expect(onStorageTierUpdate).toHaveBeenCalledTimes(1));
      const request = onStorageTierUpdate.mock.calls[0][0] as {
        object?: {
          spec?: {
            protocol?: StorageProtocol;
            backends?: unknown;
          };
        };
        updateMask?: { paths?: string[] };
      };
      expect(request.updateMask?.paths).toEqual(['spec.protocol', 'spec.backends']);
      expect(request.object?.spec).toMatchObject({
        protocol: StorageProtocol.BLOCK,
      });
      expect(request.object?.spec?.backends).toMatchObject([
        {
          backendId: 'backend-2',
        },
      ]);
    });

    it('submits protocol and spec.backends with the same update_mask even when nothing changed', async () => {
      const onStorageTierUpdate = vi.fn((_req: { object?: unknown; updateMask?: unknown }) => ({
        object: { ...existingTier },
      }));
      const { user } = renderEditPage({
        transportOverrides: { onStorageTierUpdate: onStorageTierUpdate as never },
      });

      await screen.findByRole('textbox', { name: 'Description' });
      await user.click(screen.getByRole('button', { name: 'Save' }));

      await waitFor(() => expect(onStorageTierUpdate).toHaveBeenCalledTimes(1));
      const request = onStorageTierUpdate.mock.calls[0][0] as {
        object?: { spec?: { backends?: unknown; description?: unknown } };
        updateMask?: { paths?: string[] };
      };
      expect(request.updateMask?.paths).toEqual(['spec.protocol', 'spec.backends']);
      expect(request.object?.spec?.backends).toMatchObject([{ backendId: 'backend-2' }]);
    });

    it('shows a stale-version conflict as a submission error without navigating', async () => {
      const { user } = renderEditPage({
        transportOverrides: {
          onStorageTierUpdate: () => {
            throw new ConnectError(
              'Storage tier has been modified since it was read',
              Code.FailedPrecondition,
            );
          },
        },
      });

      const descriptionInput = await screen.findByRole('textbox', { name: 'Description' });
      await user.clear(descriptionInput);
      await user.type(descriptionInput, 'updated description');
      await user.click(screen.getByRole('button', { name: 'Save' }));

      await waitFor(() => {
        expect(
          screen.getByText('Storage tier has been modified since it was read'),
        ).toBeInTheDocument();
      });
      expect(mockNavigate).not.toHaveBeenCalled();
    });

    it('navigates back to the tiers list on cancel', async () => {
      const { user } = renderEditPage();

      await screen.findByRole('button', { name: 'Cancel' });
      await user.click(screen.getByRole('button', { name: 'Cancel' }));

      expect(mockNavigate).toHaveBeenCalledWith('/admin/infrastructure/storage/tiers');
    });
  });
});
