import { screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';

import type { ComputeInstance } from '@osac/types';
import { ComputeInstanceState } from '@osac/types';

import VmDetailsActionButtons from './VmDetailsActionButtons';
import { renderWithProviders } from '../../../test-utils/TestProviders';

vi.mock('../useVmPowerAction', () => ({
  useVmPowerAction: () => ({ runPowerAction: vi.fn() }),
}));

const vm = {
  id: 'vm-1',
  metadata: { name: 'test-vm' },
  status: { state: ComputeInstanceState.RUNNING },
} as ComputeInstance;

const stoppedVm = {
  ...vm,
  status: { state: ComputeInstanceState.STOPPED },
} as ComputeInstance;

const failedVm = {
  ...vm,
  status: { state: ComputeInstanceState.FAILED },
} as ComputeInstance;

describe('VmDetailsActionButtons', () => {
  it('renders lifecycle and delete actions without external IP controls', () => {
    renderWithProviders(<VmDetailsActionButtons vm={vm} onOpenConsole={vi.fn()} />);

    expect(screen.getByRole('button', { name: 'Start' })).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Stop' })).toBeEnabled();
    expect(screen.getByRole('button', { name: 'Restart' })).toBeEnabled();
    expect(screen.getByRole('button', { name: 'Delete' })).toBeEnabled();
    expect(screen.queryByRole('button', { name: /external ip/i })).not.toBeInTheDocument();
  });

  it('opens the existing console surface through the supplied callback', async () => {
    const onOpenConsole = vi.fn();
    const user = userEvent.setup();

    renderWithProviders(<VmDetailsActionButtons vm={vm} onOpenConsole={onOpenConsole} />);

    await user.click(screen.getByRole('button', { name: 'Open console' }));

    expect(onOpenConsole).toHaveBeenCalledOnce();
  });

  it('disables the console action when the VM is not running', () => {
    renderWithProviders(<VmDetailsActionButtons vm={stoppedVm} onOpenConsole={vi.fn()} />);

    expect(screen.getByRole('button', { name: 'Open console' })).toBeDisabled();
  });

  it('allows restarting a failed VM', () => {
    renderWithProviders(<VmDetailsActionButtons vm={failedVm} onOpenConsole={vi.fn()} />);

    expect(screen.getByRole('button', { name: 'Restart' })).toBeEnabled();
  });
});
