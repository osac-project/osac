import { Route, Routes } from 'react-router-dom';
import { create } from '@bufbuild/protobuf';
import { Code, ConnectError } from '@connectrpc/connect';
import { screen, waitFor, within } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import {
  Architecture,
  type DiskImage,
  DiskImageLifecycle,
  DiskImageSchema,
  DiskImagesDeleteResponseSchema,
  GuestOSFamily,
  SourceType,
} from '@osac/types';

import DiskImageDetailPage from './DiskImageDetailPage';
import type { MockApiFixtures } from '../../test-utils/createMockConnectTransport';
import { renderWithProviders } from '../../test-utils/TestProviders';

const DETAIL_ROUTE = '/admin/infrastructure/disk-images/:id';
const LIST_PATH = '/admin/infrastructure/disk-images';

const deprecatedImage: DiskImage = create(DiskImageSchema, {
  id: 'disk-1',
  metadata: {
    name: 'fedora-41',
    tenant: 'shared',
    creationTimestamp: { seconds: BigInt(1700000000), nanos: 0 },
  },
  spec: {
    sourceType: SourceType.REGISTRY,
    sourceRef: 'quay.io/containerdisks/fedora:41',
    guestOsFamily: GuestOSFamily.GUEST_OS_FAMILY_LINUX,
    architecture: [Architecture.AMD64, Architecture.ARM64],
    lifecycle: DiskImageLifecycle.DEPRECATED,
    deprecation: {
      deprecationTimestamp: { seconds: BigInt(1700100000), nanos: 0 },
      obsolescenceTimestamp: { seconds: BigInt(1700200000), nanos: 0 },
    },
  },
});

const availableImage: DiskImage = create(DiskImageSchema, {
  id: 'disk-2',
  metadata: {
    name: 'ubuntu-24',
    tenant: 'tenant-a',
    creationTimestamp: { seconds: BigInt(1700000000), nanos: 0 },
  },
  spec: {
    sourceType: SourceType.REGISTRY,
    sourceRef: 'quay.io/containerdisks/ubuntu:24.04',
    guestOsFamily: GuestOSFamily.GUEST_OS_FAMILY_LINUX,
    architecture: [Architecture.AMD64],
    lifecycle: DiskImageLifecycle.AVAILABLE,
  },
});

const obsoleteImage: DiskImage = create(DiskImageSchema, {
  id: 'disk-3',
  metadata: { name: 'centos-9', tenant: 'shared' },
  spec: {
    sourceType: SourceType.REGISTRY,
    sourceRef: 'quay.io/containerdisks/centos:9',
    guestOsFamily: GuestOSFamily.GUEST_OS_FAMILY_LINUX,
    architecture: [Architecture.AMD64],
    lifecycle: DiskImageLifecycle.OBSOLETE,
  },
});

const renderAt = (path: string, fixtures?: MockApiFixtures) =>
  renderWithProviders(
    <Routes>
      <Route path={DETAIL_ROUTE} element={<DiskImageDetailPage />} />
      <Route path={LIST_PATH} element={<div>navigated-to-list</div>} />
    </Routes>,
    { routerEntries: [path], apiFixtures: fixtures },
  );

describe('DiskImageDetailPage', () => {
  it('shows the loading state while the disk image is fetching', () => {
    renderWithProviders(
      <Routes>
        <Route path={DETAIL_ROUTE} element={<DiskImageDetailPage />} />
      </Routes>,
      {
        routerEntries: ['/admin/infrastructure/disk-images/disk-1'],
        transportOverrides: {
          onDiskImageGet: () => new Promise(() => undefined),
        },
      },
    );

    expect(screen.getByText('Loading resource title')).toBeInTheDocument();
  });

  it('renders every AC field for a disk image with deprecation data set', async () => {
    renderAt('/admin/infrastructure/disk-images/disk-1', { diskImages: [deprecatedImage] });

    await waitFor(() => {
      expect(screen.getByRole('heading', { name: 'fedora-41' })).toBeInTheDocument();
    });

    expect(screen.getByText('Registry')).toBeInTheDocument();
    expect(screen.getByText('quay.io/containerdisks/fedora:41')).toBeInTheDocument();
    expect(screen.getByText('Linux')).toBeInTheDocument();
    expect(screen.getByText('amd64, arm64')).toBeInTheDocument();
    expect(screen.getByText('Deprecated')).toBeInTheDocument();
    expect(screen.getByText('Global')).toBeInTheDocument();
    expect(screen.getByText('Deprecation timestamp')).toBeInTheDocument();
    expect(screen.getByText('Obsolescence timestamp')).toBeInTheDocument();
    expect(screen.getByText('Created')).toBeInTheDocument();
  });

  it('omits deprecation and obsolescence fields when unset', async () => {
    renderAt('/admin/infrastructure/disk-images/disk-2', { diskImages: [availableImage] });

    await waitFor(() => {
      expect(screen.getByRole('heading', { name: 'ubuntu-24' })).toBeInTheDocument();
    });

    expect(screen.getByText('Tenant')).toBeInTheDocument();
    expect(screen.queryByText('Deprecation timestamp')).not.toBeInTheDocument();
    expect(screen.queryByText('Obsolescence timestamp')).not.toBeInTheDocument();
  });

  it('renders a not-found state and returns to the list for an unknown id', async () => {
    const { user } = renderAt('/admin/infrastructure/disk-images/unknown-id', {
      diskImages: [availableImage],
    });

    await waitFor(() => {
      expect(screen.getByText('Disk image not found')).toBeInTheDocument();
    });

    await user.click(screen.getByRole('button', { name: /Return to disk images/i }));

    await waitFor(() => {
      expect(screen.getByText('navigated-to-list')).toBeInTheDocument();
    });
  });

  it('renders an error state and retries the request when Retry is clicked', async () => {
    let callCount = 0;
    const { user } = renderWithProviders(
      <Routes>
        <Route path={DETAIL_ROUTE} element={<DiskImageDetailPage />} />
      </Routes>,
      {
        routerEntries: ['/admin/infrastructure/disk-images/disk-1'],
        transportOverrides: {
          onDiskImageGet: () => {
            callCount += 1;
            throw new ConnectError('disk image service unavailable', Code.Unavailable);
          },
        },
      },
    );

    await waitFor(() => {
      expect(screen.getByText('Could not load disk image')).toBeInTheDocument();
    });
    expect(callCount).toBe(1);

    await user.click(screen.getByRole('button', { name: 'Retry' }));

    await waitFor(() => {
      expect(callCount).toBe(2);
    });
  });

  it('shows Deprecate and Obsolete buttons, not Reactivate or Delete, for an AVAILABLE image', async () => {
    renderAt('/admin/infrastructure/disk-images/disk-2', { diskImages: [availableImage] });

    await waitFor(() => {
      expect(screen.getByRole('heading', { name: 'ubuntu-24' })).toBeInTheDocument();
    });

    expect(screen.getByRole('button', { name: 'Deprecate' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Obsolete' })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Reactivate' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Delete' })).not.toBeInTheDocument();
  });

  it('shows Obsolete and Reactivate buttons, not Deprecate or Delete, for a DEPRECATED image', async () => {
    renderAt('/admin/infrastructure/disk-images/disk-1', { diskImages: [deprecatedImage] });

    await waitFor(() => {
      expect(screen.getByRole('heading', { name: 'fedora-41' })).toBeInTheDocument();
    });

    expect(screen.getByRole('button', { name: 'Obsolete' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Reactivate' })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Deprecate' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Delete' })).not.toBeInTheDocument();
  });

  it('shows Deprecate, Reactivate, and Delete buttons, not Obsolete, for an OBSOLETE image', async () => {
    renderAt('/admin/infrastructure/disk-images/disk-3', { diskImages: [obsoleteImage] });

    await waitFor(() => {
      expect(screen.getByRole('heading', { name: 'centos-9' })).toBeInTheDocument();
    });

    expect(screen.getByRole('button', { name: 'Deprecate' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Reactivate' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Delete' })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Obsolete' })).not.toBeInTheDocument();
  });

  it('opens the delete confirmation modal without issuing a request when Delete is clicked', async () => {
    let deleteCalled = false;
    const { user } = renderWithProviders(
      <Routes>
        <Route path={DETAIL_ROUTE} element={<DiskImageDetailPage />} />
      </Routes>,
      {
        routerEntries: ['/admin/infrastructure/disk-images/disk-3'],
        apiFixtures: { diskImages: [obsoleteImage] },
        transportOverrides: {
          onDiskImageDelete: () => {
            deleteCalled = true;
            return create(DiskImagesDeleteResponseSchema);
          },
        },
      },
    );

    await waitFor(() => {
      expect(screen.getByRole('heading', { name: 'centos-9' })).toBeInTheDocument();
    });

    await user.click(screen.getByRole('button', { name: 'Delete' }));

    expect(screen.getByRole('dialog')).toBeInTheDocument();
    expect(deleteCalled).toBe(false);
  });

  it('deletes the image and navigates to the list on success', async () => {
    let deleteCalled = false;
    const { user } = renderWithProviders(
      <Routes>
        <Route path={DETAIL_ROUTE} element={<DiskImageDetailPage />} />
        <Route path={LIST_PATH} element={<div>navigated-to-list</div>} />
      </Routes>,
      {
        routerEntries: ['/admin/infrastructure/disk-images/disk-3'],
        apiFixtures: { diskImages: [obsoleteImage] },
        transportOverrides: {
          onDiskImageDelete: (req) => {
            deleteCalled = true;
            expect(req.id).toBe('disk-3');
            return create(DiskImagesDeleteResponseSchema);
          },
        },
      },
    );

    await waitFor(() => {
      expect(screen.getByRole('heading', { name: 'centos-9' })).toBeInTheDocument();
    });

    await user.click(screen.getByRole('button', { name: 'Delete' }));
    const dialog = screen.getByRole('dialog');
    await user.click(within(dialog).getByRole('button', { name: 'Delete' }));

    await waitFor(() => {
      expect(screen.getByText('navigated-to-list')).toBeInTheDocument();
    });
    expect(deleteCalled).toBe(true);
  });
});
