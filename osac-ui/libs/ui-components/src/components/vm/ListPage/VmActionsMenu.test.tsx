import { screen } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import type { ComputeInstance } from '@osac/types';
import { ComputeInstanceState } from '@osac/types';

import VmActionsMenu from './VmActionsMenu';
import { renderWithProviders } from '../../../test-utils/TestProviders';

const powerActionMocks = vi.hoisted(() => ({
  runPowerAction: vi.fn(),
}));

vi.mock('../useVmPowerAction', () => ({
  useVmPowerAction: () => ({ runPowerAction: powerActionMocks.runPowerAction }),
}));

const VM_ID = 'vm-1';
const VM_NAME = 'test-vm';

const makeVm = (
  state: ComputeInstanceState,
  { name = VM_NAME, omitName = false }: { name?: string; omitName?: boolean } = {},
): ComputeInstance =>
  ({
    id: VM_ID,
    metadata: omitName ? {} : { name },
    status: { state },
  }) as ComputeInstance;

const renderActions = (instance: ComputeInstance) =>
  renderWithProviders(<VmActionsMenu vm={instance} />);

const openMenu = async (
  user: ReturnType<typeof renderWithProviders>['user'],
  actionsLabel = `Actions for ${VM_NAME}`,
) => {
  await user.click(screen.getByRole('button', { name: actionsLabel }));
};

const menuItem = (name: string) => screen.getByRole('menuitem', { name });

const openConsoleMenuItem = () => menuItem('Open console');

const openConsoleListItem = () => {
  const listItem = openConsoleMenuItem().closest('li');
  expect(listItem).not.toBeNull();
  return listItem as HTMLLIElement;
};

const expectOpenConsoleEnabled = () => {
  const item = openConsoleMenuItem();
  expect(item).toHaveAttribute('href', `/vms/${encodeURIComponent(VM_ID)}/console`);
  expect(openConsoleListItem().className).not.toMatch(/disabled/);
};

const expectOpenConsoleUnavailable = () => {
  expect(openConsoleListItem().className).toMatch(/disabled/);
};

describe('VmActionsMenu', () => {
  beforeEach(() => {
    powerActionMocks.runPowerAction.mockClear();
  });

  describe('lifecycle menu availability', () => {
    it('reflects running-state power actions', async () => {
      const { user } = renderActions(makeVm(ComputeInstanceState.RUNNING));
      await openMenu(user);

      expect(menuItem('View details')).toBeInTheDocument();
      expectOpenConsoleEnabled();
      expect(menuItem('Start')).toBeDisabled();
      expect(menuItem('Stop')).not.toBeDisabled();
      expect(menuItem('Restart')).not.toBeDisabled();
      expect(menuItem('Delete')).not.toHaveAttribute('aria-disabled', 'true');
    });

    it('reflects stopped-state power actions', async () => {
      const { user } = renderActions(makeVm(ComputeInstanceState.STOPPED));
      await openMenu(user);

      expectOpenConsoleUnavailable();
      expect(menuItem('Start')).not.toBeDisabled();
      expect(menuItem('Stop')).toBeDisabled();
      expect(menuItem('Restart')).toBeDisabled();
    });

    it('reflects paused-state power actions', async () => {
      const { user } = renderActions(makeVm(ComputeInstanceState.PAUSED));
      await openMenu(user);

      expectOpenConsoleUnavailable();
      expect(menuItem('Start')).toBeDisabled();
      expect(menuItem('Stop')).not.toBeDisabled();
      expect(menuItem('Restart')).not.toBeDisabled();
    });

    it('allows restarting a failed VM', async () => {
      const { user } = renderActions(makeVm(ComputeInstanceState.FAILED));
      await openMenu(user);

      expect(menuItem('Start')).toBeDisabled();
      expect(menuItem('Stop')).toBeDisabled();
      expect(menuItem('Restart')).not.toBeDisabled();
    });

    it('disables delete while the VM is deleting', async () => {
      const { user } = renderActions(makeVm(ComputeInstanceState.DELETING));
      await openMenu(user);

      const deleteAction = menuItem('Delete');
      expect(deleteAction).toHaveAttribute('aria-disabled', 'true');
      expect(deleteAction).not.toBeDisabled();
    });
  });

  describe('navigation', () => {
    it('links View details to the VM detail route', async () => {
      const { user } = renderActions(makeVm(ComputeInstanceState.RUNNING));
      await openMenu(user);

      expect(menuItem('View details')).toHaveAttribute('href', `/vms/${VM_ID}`);
    });

    it('labels the menu toggle with the VM id when metadata name is missing', () => {
      renderActions(makeVm(ComputeInstanceState.RUNNING, { omitName: true }));

      expect(screen.getByRole('button', { name: `Actions for ${VM_ID}` })).toBeInTheDocument();
    });
  });

  describe('power actions', () => {
    it('starts a stopped VM', async () => {
      const { user } = renderActions(makeVm(ComputeInstanceState.STOPPED));
      await openMenu(user);
      await user.click(menuItem('Start'));

      expect(powerActionMocks.runPowerAction).toHaveBeenCalledOnce();
      expect(powerActionMocks.runPowerAction).toHaveBeenCalledWith(VM_ID, VM_NAME, 'start');
    });

    it('stops a running VM', async () => {
      const { user } = renderActions(makeVm(ComputeInstanceState.RUNNING));
      await openMenu(user);
      await user.click(menuItem('Stop'));

      expect(powerActionMocks.runPowerAction).toHaveBeenCalledOnce();
      expect(powerActionMocks.runPowerAction).toHaveBeenCalledWith(VM_ID, VM_NAME, 'stop');
    });

    it('restarts a running VM', async () => {
      const { user } = renderActions(makeVm(ComputeInstanceState.RUNNING));
      await openMenu(user);
      await user.click(menuItem('Restart'));

      expect(powerActionMocks.runPowerAction).toHaveBeenCalledOnce();
      expect(powerActionMocks.runPowerAction).toHaveBeenCalledWith(VM_ID, VM_NAME, 'restart');
    });

    it('uses the VM id as the display name when metadata name is missing', async () => {
      const { user } = renderActions(makeVm(ComputeInstanceState.STOPPED, { omitName: true }));
      await openMenu(user, `Actions for ${VM_ID}`);
      await user.click(menuItem('Start'));

      expect(powerActionMocks.runPowerAction).toHaveBeenCalledWith(VM_ID, VM_ID, 'start');
    });
  });

  describe('console', () => {
    it('links to the console route in a new tab when the VM is running', async () => {
      const { user } = renderActions(makeVm(ComputeInstanceState.RUNNING));
      await openMenu(user);

      const consoleItem = openConsoleMenuItem();
      expect(consoleItem).toHaveAttribute('href', `/vms/${encodeURIComponent(VM_ID)}/console`);
      expect(consoleItem).toHaveAttribute('target', '_blank');
      expect(consoleItem).toHaveAttribute('rel', 'noopener noreferrer');
    });
  });

  describe('delete', () => {
    it('opens the delete confirmation modal', async () => {
      const { user } = renderActions(makeVm(ComputeInstanceState.RUNNING));
      await openMenu(user);
      await user.click(menuItem('Delete'));

      expect(screen.getByRole('dialog')).toBeInTheDocument();
      expect(screen.getByText(/This permanently deletes the compute instance/)).toBeInTheDocument();
    });

    it('does not open the delete modal when delete is unavailable', async () => {
      const { user } = renderActions(makeVm(ComputeInstanceState.DELETING));
      await openMenu(user);
      await user.click(menuItem('Delete'));

      expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    });
  });
});
