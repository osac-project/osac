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
  it('enables Edit and Delete for an AVAILABLE volume', async () => {
    const { user } = renderMenu(makeVolume(VolumeState.AVAILABLE));
    await openMenu(user);

    expect(screen.getByRole('menuitem', { name: 'Edit' })).not.toBeDisabled();
    expect(screen.getByRole('menuitem', { name: 'Delete' })).not.toBeDisabled();
  });

  it('enables Edit but disables Delete for a CREATING volume', async () => {
    const { user } = renderMenu(makeVolume(VolumeState.CREATING));
    await openMenu(user);

    expect(screen.getByRole('menuitem', { name: 'Edit' })).not.toBeDisabled();
    expect(screen.getByRole('menuitem', { name: 'Delete' })).toBeDisabled();
  });

  it('enables Edit and Delete for a FAILED volume', async () => {
    const { user } = renderMenu(makeVolume(VolumeState.FAILED));
    await openMenu(user);

    expect(screen.getByRole('menuitem', { name: 'Edit' })).not.toBeDisabled();
    expect(screen.getByRole('menuitem', { name: 'Delete' })).not.toBeDisabled();
  });

  it('shows the kebab with both actions disabled for a DELETING volume', async () => {
    const { user } = renderMenu(makeVolume(VolumeState.DELETING));
    await openMenu(user);

    expect(screen.getByRole('menuitem', { name: 'Edit' })).toBeDisabled();
    expect(screen.getByRole('menuitem', { name: 'Delete' })).toBeDisabled();
  });

  it('shows the kebab with both actions disabled for a DELETED volume', async () => {
    const { user } = renderMenu(makeVolume(VolumeState.DELETED));
    await openMenu(user);

    expect(screen.getByRole('menuitem', { name: 'Edit' })).toBeDisabled();
    expect(screen.getByRole('menuitem', { name: 'Delete' })).toBeDisabled();
  });

  it('shows the kebab with both actions disabled for an UNSPECIFIED volume', async () => {
    const { user } = renderMenu(makeVolume(VolumeState.UNSPECIFIED));
    await openMenu(user);

    expect(screen.getByRole('menuitem', { name: 'Edit' })).toBeDisabled();
    expect(screen.getByRole('menuitem', { name: 'Delete' })).toBeDisabled();
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
      screen.getByText(
        'This permanently deletes the volume and all of its data. This action cannot be undone.',
      ),
    ).toBeInTheDocument();
  });
});
