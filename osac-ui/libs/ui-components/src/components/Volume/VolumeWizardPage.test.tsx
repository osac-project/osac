import { Route, Routes, useLocation, useParams } from 'react-router-dom';
import { create } from '@bufbuild/protobuf';
import { Code, ConnectError } from '@connectrpc/connect';
import { screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import {
  StorageProtocol,
  StorageTierSchema,
  StorageTierState,
  VolumeAccessMode,
  type VolumesCreateRequest,
  type VolumesCreateResponse,
  VolumesCreateResponseSchema,
} from '@osac/types';

import VolumeWizardPage from './VolumeWizardPage';
import type { RenderWithProvidersOptions } from '../../test-utils/TestProviders';
import { renderWithProviders } from '../../test-utils/TestProviders';

vi.mock('react-router-dom', async (importOriginal) => {
  const actual = await importOriginal<typeof import('react-router-dom')>();
  return { ...actual, useBlocker: () => ({ state: 'unblocked' as const }) };
});

const storageTier = create(StorageTierSchema, {
  id: 'tier-block',
  metadata: { name: 'block-tier' },
  spec: { description: 'Block storage', protocol: StorageProtocol.BLOCK },
  status: { state: StorageTierState.ACTIVE },
});

const createErrorCases = [
  {
    label: 'InvalidArgument',
    code: Code.InvalidArgument,
    backendMessage: 'Volume size must be greater than zero',
  },
  {
    label: 'AlreadyExists',
    code: Code.AlreadyExists,
    backendMessage: 'A volume with this name already exists',
  },
  {
    label: 'PermissionDenied',
    code: Code.PermissionDenied,
    backendMessage: 'permission details from the backend',
  },
  {
    label: 'Internal',
    code: Code.Internal,
    backendMessage: 'internal stack details',
  },
] as const;

const VolumeDetailProbe = () => {
  const { id } = useParams();
  return <div>Volume detail: {id}</div>;
};

const NavigationProbe = () => {
  const location = useLocation();
  return <div>Current path: {location.pathname}</div>;
};

const renderAt = (path: string, options: Omit<RenderWithProvidersOptions, 'routerEntries'> = {}) =>
  renderWithProviders(
    <Routes>
      <Route path="/storage/volumes/create" element={<VolumeWizardPage />} />
      <Route path="/storage/volumes/:id" element={<VolumeDetailProbe />} />
      <Route path="*" element={<NavigationProbe />} />
    </Routes>,
    { ...options, routerEntries: [path] },
  );

const fillValidWizard = async (user: ReturnType<typeof renderWithProviders>['user']) => {
  await user.type(screen.getByRole('textbox', { name: 'Name' }), 'new-volume');
  await user.click(screen.getByRole('button', { name: 'Next' }));
  expect(await screen.findByRole('heading', { name: 'Configuration' })).toBeInTheDocument();

  const tierToggle = await screen.findByLabelText(/^Storage tier/);
  await user.click(tierToggle);
  await user.click(screen.getByRole('option', { name: 'block-tier' }));
  await user.type(screen.getByRole('spinbutton', { name: 'Size (GiB)' }), '64');
  await user.click(screen.getByRole('button', { name: 'Access Mode' }));
  await user.click(screen.getByRole('option', { name: 'ReadWriteOnce' }));
  await user.click(screen.getByRole('button', { name: 'Next' }));
  expect(await screen.findByRole('heading', { name: 'Review' })).toBeInTheDocument();
};

describe('VolumeWizardPage', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('renders the create page and wizard without loading a volume', async () => {
    renderAt('/storage/volumes/create');

    expect(await screen.findByRole('heading', { name: 'Create volume' })).toBeInTheDocument();
    expect(screen.getByRole('region', { name: 'Volume wizard' })).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: 'General' })).toBeInTheDocument();
  });

  it('creates a volume and navigates to its detail route', async () => {
    let capturedRequest: VolumesCreateRequest | undefined;
    const { user } = renderAt('/storage/volumes/create', {
      apiFixtures: { publicStorageTiers: [storageTier] },
      transportOverrides: {
        onVolumeCreate: (request) => {
          capturedRequest = request;
          return create(VolumesCreateResponseSchema, {
            object: {
              id: 'created-volume',
              metadata: request.object?.metadata,
              spec: request.object?.spec,
            },
          });
        },
      },
    });

    await fillValidWizard(user);
    await user.click(screen.getByRole('button', { name: 'Create volume' }));

    expect(await screen.findByText('Volume detail: created-volume')).toBeInTheDocument();
    expect(capturedRequest?.object).toMatchObject({
      metadata: { name: 'new-volume' },
      spec: {
        storageTier: 'block-tier',
        sizeGib: 64n,
        accessMode: VolumeAccessMode.READ_WRITE_ONCE,
      },
    });
  });

  it('shows a create error without navigating when the API rejects the request', async () => {
    const { user } = renderAt('/storage/volumes/create', {
      apiFixtures: { publicStorageTiers: [storageTier] },
      transportOverrides: {
        onVolumeCreate: () => {
          throw new ConnectError('backend unavailable', Code.Unavailable);
        },
      },
    });

    await fillValidWizard(user);
    await user.click(screen.getByRole('button', { name: 'Create volume' }));

    expect(await screen.findByText('Failed to create resource')).toBeInTheDocument();
    expect(screen.getByText('backend unavailable')).toBeInTheDocument();
    expect(screen.queryByText(/Volume detail:/)).not.toBeInTheDocument();
  });

  it.each(createErrorCases)(
    'maps a $label create error and keeps the form populated for retry',
    async ({ code, backendMessage }) => {
      const { user } = renderAt('/storage/volumes/create', {
        apiFixtures: { publicStorageTiers: [storageTier] },
        transportOverrides: {
          onVolumeCreate: () => {
            throw new ConnectError(backendMessage, code);
          },
        },
      });

      await fillValidWizard(user);
      await user.click(screen.getByRole('button', { name: 'Create volume' }));

      const alert = (
        await screen.findByRole('heading', { name: 'Danger alert: Failed to create resource' })
      ).closest('.pf-v6-c-alert');
      expect(alert).toHaveClass('pf-m-danger');
      expect(screen.queryByText(/Volume detail:/)).not.toBeInTheDocument();
      expect(screen.getByRole('button', { name: 'Create volume' })).toBeEnabled();

      await user.click(screen.getByRole('button', { name: 'Back' }));
      expect(await screen.findByRole('heading', { name: 'Configuration' })).toBeInTheDocument();
      expect(screen.getByRole('spinbutton', { name: 'Size (GiB)' })).toHaveValue(64);

      await user.click(screen.getByRole('button', { name: 'Back' }));
      expect(await screen.findByRole('heading', { name: 'General' })).toBeInTheDocument();
      expect(screen.getByRole('textbox', { name: 'Name' })).toHaveValue('new-volume');
    },
  );

  it('disables submission while creating and enables retry after a rejected request', async () => {
    let attempts = 0;
    let rejectFirstAttempt!: (reason?: unknown) => void;
    const firstAttempt = new Promise<VolumesCreateResponse>((_, reject) => {
      rejectFirstAttempt = reject;
    });
    const { user } = renderAt('/storage/volumes/create', {
      apiFixtures: { publicStorageTiers: [storageTier] },
      transportOverrides: {
        onVolumeCreate: () => {
          attempts += 1;
          if (attempts === 1) {
            return firstAttempt;
          }
          return create(VolumesCreateResponseSchema, { object: { id: 'retried-volume' } });
        },
      },
    });

    await fillValidWizard(user);
    const submitButton = screen.getByRole('button', { name: 'Create volume' });
    await user.click(submitButton);

    await waitFor(() => {
      expect(attempts).toBe(1);
      expect(submitButton).toBeDisabled();
    });

    rejectFirstAttempt(new ConnectError('temporary failure', Code.Internal));
    expect(await screen.findByText('Unexpected error occurred')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Create volume' })).toBeEnabled();

    await user.click(screen.getByRole('button', { name: 'Create volume' }));
    expect(await screen.findByText('Volume detail: retried-volume')).toBeInTheDocument();
    expect(attempts).toBe(2);
  });
});
