import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';

import type { ConsoleTransport, ConsoleUiConnectionState } from './console.types';
import ConsoleToolbar from './ConsoleToolbar';

const renderToolbar = (overrides: Partial<Parameters<typeof ConsoleToolbar>[0]> = {}) => {
  const onConsoleTransportChange = vi.fn<(transport: ConsoleTransport) => void>();
  const onPaste = vi.fn();
  const props = {
    connectionState: 'connected' as ConsoleUiConnectionState,
    onPaste,
    consoleTransport: 'vnc' as ConsoleTransport,
    onConsoleTransportChange,
    ...overrides,
  };
  render(<ConsoleToolbar {...props} />);
  return { onConsoleTransportChange, onPaste };
};

describe('ConsoleToolbar', () => {
  it('shows the current transport on the selector toggle', () => {
    renderToolbar({ consoleTransport: 'vnc' });

    expect(screen.getByRole('button', { name: 'Select console type' })).toHaveTextContent(
      'VNC console',
    );
  });

  it('offers both transports and marks the current one selected', async () => {
    const user = userEvent.setup();
    renderToolbar({ consoleTransport: 'serial' });

    await user.click(screen.getByRole('button', { name: 'Select console type' }));

    expect(
      screen.getByRole('option', { name: 'VNC console', selected: false }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole('option', { name: 'Serial console', selected: true }),
    ).toBeInTheDocument();
  });

  it('reports the chosen transport when a different option is selected', async () => {
    const user = userEvent.setup();
    const { onConsoleTransportChange } = renderToolbar({ consoleTransport: 'vnc' });

    await user.click(screen.getByRole('button', { name: 'Select console type' }));
    await user.click(screen.getByRole('option', { name: 'Serial console' }));

    expect(onConsoleTransportChange).toHaveBeenCalledWith('serial');
  });

  it('enables Paste while connected and forwards its click', async () => {
    const user = userEvent.setup();
    const { onPaste } = renderToolbar({ connectionState: 'connected' });

    await user.click(screen.getByRole('button', { name: 'Paste from clipboard' }));

    expect(onPaste).toHaveBeenCalled();
  });

  it('disables Paste while not connected', () => {
    renderToolbar({ connectionState: 'connecting' });

    expect(screen.getByRole('button', { name: 'Paste from clipboard' })).toBeDisabled();
    // The transport selector stays usable regardless of connection state.
    expect(screen.getByRole('button', { name: 'Select console type' })).toBeEnabled();
  });
});
