import { create } from '@bufbuild/protobuf';
import { screen, waitFor } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import { type Volume, VolumeSchema, VolumeState } from '@osac/types';

import VolumeActionsMenu from './VolumeActionsMenu';
import { renderWithProviders } from '../../test-utils/TestProviders';

const makeVolume = (state: VolumeState, name = 'test-volume'): Volume =>
  create(VolumeSchema, {
    id: 'vol-1',
    metadata: { name },
    status: { state },
  });

const renderMenu = (volume: Volume) => renderWithProviders(<VolumeActionsMenu volume={volume} />);

const openMenu = async (
  user: ReturnType<typeof renderWithProviders>['user'],
  name = 'test-volume',
) => {
  await user.click(screen.getByRole('button', { name: `Actions for ${name}` }));
};

describe('VolumeActionsMenu', () => {
  it('shows Edit and Delete for an AVAILABLE volume', async () => {
    const { user } = renderMenu(makeVolume(VolumeState.AVAILABLE));
    await openMenu(user);

    expect(screen.getByRole('menuitem', { name: 'Edit' })).toBeInTheDocument();
    expect(screen.getByRole('menuitem', { name: 'Delete' })).toBeInTheDocument();
  });

  it('shows Edit but not Delete for a CREATING volume', async () => {
    const { user } = renderMenu(makeVolume(VolumeState.CREATING));
    await openMenu(user);

    expect(screen.getByRole('menuitem', { name: 'Edit' })).toBeInTheDocument();
    expect(screen.queryByRole('menuitem', { name: 'Delete' })).not.toBeInTheDocument();
  });

  it('shows Edit and Delete for a FAILED volume', async () => {
    const { user } = renderMenu(makeVolume(VolumeState.FAILED));
    await openMenu(user);

    expect(screen.getByRole('menuitem', { name: 'Edit' })).toBeInTheDocument();
    expect(screen.getByRole('menuitem', { name: 'Delete' })).toBeInTheDocument();
  });

  it('renders nothing for a DELETING volume', () => {
    renderMenu(makeVolume(VolumeState.DELETING));

    expect(
      screen.queryByRole('button', { name: 'Actions for test-volume' }),
    ).not.toBeInTheDocument();
  });

  it('renders nothing for a DELETED volume', () => {
    renderMenu(makeVolume(VolumeState.DELETED));

    expect(
      screen.queryByRole('button', { name: 'Actions for test-volume' }),
    ).not.toBeInTheDocument();
  });

  it('renders nothing for an UNSPECIFIED volume', () => {
    renderMenu(makeVolume(VolumeState.UNSPECIFIED));

    expect(
      screen.queryByRole('button', { name: 'Actions for test-volume' }),
    ).not.toBeInTheDocument();
  });

  it('closes the menu after Edit is clicked, confirming navigation was triggered', async () => {
    const { user } = renderMenu(makeVolume(VolumeState.AVAILABLE));
    await openMenu(user);
    await user.click(screen.getByRole('menuitem', { name: 'Edit' }));

    await waitFor(() => {
      expect(screen.queryByRole('menuitem', { name: 'Edit' })).not.toBeInTheDocument();
    });
  });

  it('opens the delete confirmation modal when Delete is clicked', async () => {
    const { user } = renderMenu(makeVolume(VolumeState.AVAILABLE));
    await openMenu(user);
    await user.click(screen.getByRole('menuitem', { name: 'Delete' }));

    expect(
      screen.getByText('This permanently deletes the volume. This action cannot be undone.'),
    ).toBeInTheDocument();
  });
});
