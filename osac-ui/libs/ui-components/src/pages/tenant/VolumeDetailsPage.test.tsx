import { Route, Routes } from 'react-router-dom';
import { Code, ConnectError } from '@connectrpc/connect';
import { screen, waitFor } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import { type Volume, VolumeAccessMode, VolumeState } from '@osac/types';

import { VolumeDetailsPage } from './VolumeDetailsPage';
import type { MockApiFixtures } from '../../test-utils/createMockConnectTransport';
import { renderWithProviders } from '../../test-utils/TestProviders';

const testVolume: Volume = {
  $typeName: 'osac.public.v1.Volume',
  id: 'vol-test-1',
  metadata: {
    $typeName: 'osac.public.v1.Metadata',
    name: 'test-volume',
    tenant: 'test-tenant',
    project: 'test-project',
    creator: 'bob',
    creationTimestamp: {
      $typeName: 'google.protobuf.Timestamp',
      seconds: 1767225600n,
      nanos: 0,
    },
    displayName: '',
    description: '',
    annotations: {},
    labels: {},
    version: 1,
  },
  spec: {
    $typeName: 'osac.public.v1.VolumeSpec',
    storageTier: 'balanced',
    sizeGib: 50n,
    accessMode: VolumeAccessMode.READ_WRITE_ONCE,
  },
  status: {
    $typeName: 'osac.public.v1.VolumeStatus',
    state: VolumeState.AVAILABLE,
  },
} as Volume;

const renderAt = (path: string, fixtures?: MockApiFixtures) =>
  renderWithProviders(
    <Routes>
      <Route path="/storage/volumes/:id" element={<VolumeDetailsPage />} />
      <Route path="/storage/volumes" element={<div>navigated-to-list</div>} />
    </Routes>,
    { routerEntries: [path], apiFixtures: fixtures },
  );

describe('VolumeDetailsPage', () => {
  it('shows the loading state while the volume is fetching', () => {
    renderWithProviders(
      <Routes>
        <Route path="/storage/volumes/:id" element={<VolumeDetailsPage />} />
      </Routes>,
      {
        routerEntries: ['/storage/volumes/vol-test-1'],
        transportOverrides: {
          onVolumeGet: () => new Promise(() => undefined),
        },
      },
    );

    expect(screen.getByText('Loading resource title')).toBeInTheDocument();
  });

  it('renders volume details on success', async () => {
    renderAt('/storage/volumes/vol-test-1', { volumes: [testVolume] });

    await waitFor(() => {
      expect(screen.getByRole('heading', { name: 'test-volume' })).toBeInTheDocument();
    });
    expect(screen.getByRole('link', { name: 'Volumes' })).toHaveAttribute(
      'href',
      '/storage/volumes',
    );
    expect(screen.getAllByText('Available').length).toBeGreaterThanOrEqual(1);
  });

  it('renders a not-found state for an unknown volume id', async () => {
    renderAt('/storage/volumes/unknown-id', { volumes: [testVolume] });

    await waitFor(() => {
      expect(screen.getByText('Volume not found')).toBeInTheDocument();
    });
  });

  it('renders an error state when the volume fails to load', async () => {
    renderWithProviders(
      <Routes>
        <Route path="/storage/volumes/:id" element={<VolumeDetailsPage />} />
        <Route path="/storage/volumes" element={<div>navigated-to-list</div>} />
      </Routes>,
      {
        routerEntries: ['/storage/volumes/vol-test-1'],
        transportOverrides: {
          onVolumeGet: () => {
            throw new ConnectError('volume service unavailable', Code.Unavailable);
          },
        },
      },
    );

    await waitFor(() => {
      expect(screen.getByText('Could not load volume')).toBeInTheDocument();
    });
  });
});
