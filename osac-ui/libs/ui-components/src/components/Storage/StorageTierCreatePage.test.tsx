import { Route, Routes } from 'react-router-dom';
import { create } from '@bufbuild/protobuf';
import { Code, ConnectError } from '@connectrpc/connect';
import { screen, waitFor } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import type {
  StorageBackend,
  StorageTier,
  StorageTiersCreateRequest,
  StorageTiersCreateResponse,
  StorageTiersUpdateRequest,
} from '@osac/types/private';
import {
  StorageBackendState,
  StorageProtocol,
  StorageTierState,
  StorageTiersCreateResponseSchema,
  StorageTiersUpdateResponseSchema,
} from '@osac/types/private';

import StorageTierCreatePage from './StorageTierCreatePage';
import {
  type RenderWithProvidersOptions,
  renderWithProviders,
} from '../../test-utils/TestProviders';

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

const CREATE_ROUTE_PATH = '/admin/infrastructure/storage/tiers/create';
const TIERS_LIST_PATH = '/admin/infrastructure/storage/tiers';

const renderCreatePage = (options: RenderWithProvidersOptions = {}) =>
  renderWithProviders(
    <Routes>
      <Route path={CREATE_ROUTE_PATH} element={<StorageTierCreatePage />} />
      <Route path={`${TIERS_LIST_PATH}/:id`} element={<div>navigated-to-tier-details</div>} />
      <Route path={TIERS_LIST_PATH} element={<div>navigated-to-tiers</div>} />
    </Routes>,
    { routerEntries: [CREATE_ROUTE_PATH], ...options },
  );

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
      <Route path={TIERS_LIST_PATH} element={<div>navigated-to-tiers</div>} />
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
      let createRequest: StorageTiersCreateRequest | undefined;

      const { user } = renderCreatePage({
        apiFixtures: { storageBackends: [readyBackend] },
        transportOverrides: {
          onStorageTierCreate: (request) => {
            createRequest = request;
            return create(StorageTiersCreateResponseSchema, {
              object: {
                id: 'new-tier-1',
                metadata: request.object?.metadata,
                spec: request.object?.spec,
                status: { state: StorageTierState.ACTIVE },
              },
            });
          },
        },
      });

      await fillValidForm(user);
      await user.click(screen.getByRole('button', { name: 'Create' }));

      expect(await screen.findByText('navigated-to-tier-details')).toBeInTheDocument();

      expect(createRequest?.object?.metadata).toMatchObject({ name: 'fast-tier' });
      expect(createRequest?.object?.spec).toMatchObject({
        protocol: StorageProtocol.NFS,
        backends: [{ backendId: 'backend-1' }],
      });
      expect(createRequest?.object?.spec?.backends[0]).not.toHaveProperty('protocol');
      expect(createRequest?.object?.spec?.backends[0]).not.toHaveProperty('quotaGib');
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

      expect(await screen.findByText('navigated-to-tier-details')).toBeInTheDocument();
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
      expect(screen.getByRole('heading', { name: 'Create storage tier' })).toBeInTheDocument();
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
      expect(screen.getByRole('heading', { name: 'Create storage tier' })).toBeInTheDocument();
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
      let updateRequest: StorageTiersUpdateRequest | undefined;
      const { user } = renderEditPage({
        transportOverrides: {
          onStorageTierUpdate: (request) => {
            updateRequest = request;
            return create(StorageTiersUpdateResponseSchema, { object: existingTier });
          },
        },
      });

      const descriptionInput = await screen.findByRole('textbox', { name: 'Description' });
      await user.clear(descriptionInput);
      await user.type(descriptionInput, 'updated description');
      await user.click(screen.getByRole('button', { name: 'Save' }));

      expect(await screen.findByText('navigated-to-tiers')).toBeInTheDocument();

      expect(updateRequest?.object?.spec?.description).toBe('updated description');
      expect(updateRequest?.object?.spec?.backends).toMatchObject([{ backendId: 'backend-2' }]);
      expect(updateRequest?.updateMask?.paths).toEqual([
        'metadata.version',
        'spec.protocol',
        'spec.backends',
        'spec.description',
      ]);
      expect(updateRequest?.object?.metadata).toMatchObject({ version: 5 });
    });

    it('submits protocol and spec.backends together, masked accordingly, when the protocol changes', async () => {
      let updateRequest: StorageTiersUpdateRequest | undefined;
      const { user } = renderEditPage({
        transportOverrides: {
          onStorageTierUpdate: (request) => {
            updateRequest = request;
            return create(StorageTiersUpdateResponseSchema, { object: existingTier });
          },
        },
      });

      await screen.findByRole('radio', { name: 'NFS' });
      await user.click(screen.getByRole('radio', { name: 'Block' }));
      await user.click(screen.getByRole('button', { name: 'Save' }));

      expect(await screen.findByText('navigated-to-tiers')).toBeInTheDocument();

      expect(updateRequest?.updateMask?.paths).toEqual([
        'metadata.version',
        'spec.protocol',
        'spec.backends',
      ]);
      expect(updateRequest?.object?.spec).toMatchObject({
        protocol: StorageProtocol.BLOCK,
      });
      expect(updateRequest?.object?.spec?.backends).toMatchObject([
        {
          backendId: 'backend-2',
        },
      ]);
    });

    it('submits protocol and spec.backends with the same update_mask even when nothing changed', async () => {
      let updateRequest: StorageTiersUpdateRequest | undefined;
      const { user } = renderEditPage({
        transportOverrides: {
          onStorageTierUpdate: (request) => {
            updateRequest = request;
            return create(StorageTiersUpdateResponseSchema, { object: existingTier });
          },
        },
      });

      await screen.findByRole('textbox', { name: 'Description' });
      await user.click(screen.getByRole('button', { name: 'Save' }));

      expect(await screen.findByText('navigated-to-tiers')).toBeInTheDocument();

      expect(updateRequest?.updateMask?.paths).toEqual([
        'metadata.version',
        'spec.protocol',
        'spec.backends',
      ]);
      expect(updateRequest?.object?.spec?.backends).toMatchObject([{ backendId: 'backend-2' }]);
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
      expect(screen.queryByText('navigated-to-tiers')).not.toBeInTheDocument();
    });

    it('navigates back to the tiers list on cancel', async () => {
      const { user } = renderEditPage();

      await screen.findByRole('button', { name: 'Cancel' });
      await user.click(screen.getByRole('button', { name: 'Cancel' }));

      expect(await screen.findByText('navigated-to-tiers')).toBeInTheDocument();
    });
  });
});
