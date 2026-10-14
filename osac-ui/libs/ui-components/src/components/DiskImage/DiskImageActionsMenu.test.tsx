import { create } from '@bufbuild/protobuf';
import { Code, ConnectError } from '@connectrpc/connect';
import { screen, waitFor } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import {
  type DiskImage,
  DiskImageLifecycle,
  DiskImageSchema,
  DiskImagesUpdateResponseSchema,
} from '@osac/types';

import DiskImageActionsMenu from './DiskImageActionsMenu';
import DiskImageLifecycleLabel from './DiskImageLifecycleLabel';
import { renderWithProviders } from '../../test-utils/TestProviders';

const makeDiskImage = (lifecycle: DiskImageLifecycle): DiskImage =>
  create(DiskImageSchema, {
    id: 'di-1',
    metadata: { name: 'fedora-41' },
    spec: { sourceRef: 'quay.io/example/fedora:41', lifecycle },
  });

const renderMenu = (diskImage: DiskImage, options?: Parameters<typeof renderWithProviders>[1]) =>
  renderWithProviders(<DiskImageActionsMenu diskImage={diskImage} />, options);

const openMenu = async (user: ReturnType<typeof renderWithProviders>['user']) => {
  await user.click(screen.getByRole('button', { name: 'Actions for fedora-41' }));
};

describe('DiskImageActionsMenu', () => {
  it('exposes Deprecate and Obsolete, but not Reactivate or Delete, for an AVAILABLE disk image', async () => {
    const { user } = renderMenu(makeDiskImage(DiskImageLifecycle.AVAILABLE));
    await openMenu(user);

    expect(screen.getByRole('menuitem', { name: 'Deprecate' })).toBeInTheDocument();
    expect(screen.getByRole('menuitem', { name: 'Obsolete' })).toBeInTheDocument();
    expect(screen.queryByRole('menuitem', { name: 'Reactivate' })).not.toBeInTheDocument();
    expect(screen.queryByRole('menuitem', { name: 'Delete' })).not.toBeInTheDocument();
  });

  it('exposes Obsolete and Reactivate, but not Deprecate or Delete, for a DEPRECATED disk image', async () => {
    const { user } = renderMenu(makeDiskImage(DiskImageLifecycle.DEPRECATED));
    await openMenu(user);

    expect(screen.getByRole('menuitem', { name: 'Obsolete' })).toBeInTheDocument();
    expect(screen.getByRole('menuitem', { name: 'Reactivate' })).toBeInTheDocument();
    expect(screen.queryByRole('menuitem', { name: 'Deprecate' })).not.toBeInTheDocument();
    expect(screen.queryByRole('menuitem', { name: 'Delete' })).not.toBeInTheDocument();
  });

  it('exposes only Reactivate for an OBSOLETE disk image (Delete not wired this story)', async () => {
    const { user } = renderMenu(makeDiskImage(DiskImageLifecycle.OBSOLETE));
    await openMenu(user);

    expect(screen.getByRole('menuitem', { name: 'Reactivate' })).toBeInTheDocument();
    expect(screen.queryByRole('menuitem', { name: 'Deprecate' })).not.toBeInTheDocument();
    expect(screen.queryByRole('menuitem', { name: 'Obsolete' })).not.toBeInTheDocument();
    expect(screen.queryByRole('menuitem', { name: 'Delete' })).not.toBeInTheDocument();
  });

  it('renders nothing for an unspecified lifecycle (no actions available)', () => {
    renderMenu(makeDiskImage(DiskImageLifecycle.UNSPECIFIED));

    expect(screen.queryByRole('button', { name: 'Actions for fedora-41' })).not.toBeInTheDocument();
  });

  it('sends a spec.lifecycle update targeting DEPRECATED when Deprecate is clicked', async () => {
    let captured: Record<string, unknown> | undefined;
    const { user } = renderMenu(makeDiskImage(DiskImageLifecycle.AVAILABLE), {
      transportOverrides: {
        onDiskImageUpdate: (req) => {
          captured = req;
          return create(DiskImagesUpdateResponseSchema, {
            object: makeDiskImage(DiskImageLifecycle.DEPRECATED),
          });
        },
      },
    });
    await openMenu(user);
    await user.click(screen.getByRole('menuitem', { name: 'Deprecate' }));

    await waitFor(() => expect(captured).toBeDefined());
    const object = captured?.object as { id?: string; spec?: { lifecycle?: DiskImageLifecycle } };
    expect(object.id).toBe('di-1');
    expect(object.spec?.lifecycle).toBe(DiskImageLifecycle.DEPRECATED);
  });

  it('shows a toast with the deprecate failure title and backend message when the update fails, and leaves the lifecycle unchanged', async () => {
    const diskImage = makeDiskImage(DiskImageLifecycle.AVAILABLE);
    const { user } = renderWithProviders(
      <>
        <DiskImageLifecycleLabel lifecycle={diskImage.spec?.lifecycle} />
        <DiskImageActionsMenu diskImage={diskImage} />
      </>,
      {
        transportOverrides: {
          onDiskImageUpdate: () => {
            throw new ConnectError('image is referenced', Code.FailedPrecondition);
          },
        },
      },
    );
    await openMenu(user);
    await user.click(screen.getByRole('menuitem', { name: 'Deprecate' }));

    expect(await screen.findByText('Failed to deprecate disk image')).toBeInTheDocument();
    expect(screen.getByText('image is referenced')).toBeInTheDocument();
    expect(screen.getByText('Available')).toBeInTheDocument();
    expect(screen.queryByText('Deprecated')).not.toBeInTheDocument();
  });

  it('shows a toast with the obsolete failure title when the obsolete update fails', async () => {
    const { user } = renderMenu(makeDiskImage(DiskImageLifecycle.AVAILABLE), {
      transportOverrides: {
        onDiskImageUpdate: () => {
          throw new ConnectError('obsolete rejected', Code.FailedPrecondition);
        },
      },
    });
    await openMenu(user);
    await user.click(screen.getByRole('menuitem', { name: 'Obsolete' }));

    expect(await screen.findByText('Failed to mark disk image as obsolete')).toBeInTheDocument();
  });

  it('shows a toast with the reactivate failure title when the reactivate update fails', async () => {
    const { user } = renderMenu(makeDiskImage(DiskImageLifecycle.DEPRECATED), {
      transportOverrides: {
        onDiskImageUpdate: () => {
          throw new ConnectError('reactivation rejected', Code.FailedPrecondition);
        },
      },
    });
    await openMenu(user);
    await user.click(screen.getByRole('menuitem', { name: 'Reactivate' }));

    expect(await screen.findByText('Failed to reactivate disk image')).toBeInTheDocument();
  });
});
