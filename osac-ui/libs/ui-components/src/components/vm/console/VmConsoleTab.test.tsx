import { forwardRef, useImperativeHandle } from 'react';
import { act, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import type { ComputeInstance } from '@osac/types';
import { ComputeInstanceState, ConsoleType } from '@osac/types';

import VmConsoleTab from './VmConsoleTab';
import { renderWithProviders } from '../../../test-utils/TestProviders';

vi.mock('../../Console/useConsoleSession', () => ({
  useConsoleSession: vi.fn(),
}));

vi.mock('../../Console/novnc-rfb', () => ({
  loadVncRfbConstructor: vi.fn().mockResolvedValue(undefined),
}));

vi.mock('../../Console/xterm-loader', () => ({
  loadXtermConstructors: vi.fn().mockResolvedValue({}),
}));

vi.mock('../../Console/SerialConsoleViewer', () => {
  const MockSerialConsoleViewer = forwardRef<
    { focus: () => void; pasteFromClipboard: () => Promise<void> },
    unknown
  >((_props, ref) => {
    useImperativeHandle(ref, () => ({
      focus: vi.fn(),
      pasteFromClipboard: () => Promise.resolve(),
    }));
    return <div>Serial viewer</div>;
  });
  MockSerialConsoleViewer.displayName = 'MockSerialConsoleViewer';
  return { default: MockSerialConsoleViewer };
});

// VncConsoleViewer is a static import in VmConsoleTab.tsx (no longer lazy), so vi.mock's
// hoisting runs this factory before any of the test file's own top-level consts — anything
// it needs (the vi.fn() and the mutable ref for the captured onConnected prop) must come
// from vi.hoisted() rather than a plain const declared below.
const { focusViewer, latestOnConnectedRef } = vi.hoisted(() => ({
  focusViewer: vi.fn(),
  latestOnConnectedRef: { current: undefined as (() => void) | undefined },
}));

vi.mock('../../Console/VncConsoleViewer', () => {
  const MockVncConsoleViewer = forwardRef<
    { focus: () => void; pasteFromClipboard: () => Promise<void> },
    { onConnected?: () => void }
  >((props, ref) => {
    latestOnConnectedRef.current = props.onConnected;
    useImperativeHandle(ref, () => ({
      focus: focusViewer,
      pasteFromClipboard: () => Promise.resolve(),
    }));
    return <div>VNC viewer</div>;
  });
  MockVncConsoleViewer.displayName = 'MockVncConsoleViewer';
  return { default: MockVncConsoleViewer };
});

const { useConsoleSession } = await import('../../Console/useConsoleSession');
const { loadVncRfbConstructor } = await import('../../Console/novnc-rfb');

const runningVm = {
  id: 'vm-1',
  status: { state: ComputeInstanceState.RUNNING },
} as ComputeInstance;

const stoppedVm = {
  id: 'vm-1',
  status: { state: ComputeInstanceState.STOPPED },
} as ComputeInstance;

const renderTab = (vm: ComputeInstance) => renderWithProviders(<VmConsoleTab vm={vm} />);

describe('VmConsoleTab', () => {
  beforeEach(() => {
    latestOnConnectedRef.current = undefined;
    focusViewer.mockClear();
    vi.mocked(loadVncRfbConstructor).mockClear();
    vi.mocked(useConsoleSession).mockReturnValue({
      connectionState: 'idle',
      connect: vi.fn(),
      errorMessage: null,
      errorKind: 'generic',
      reportViewerError: vi.fn(),
      takeOver: vi.fn(),
      webSocket: null,
    });
  });

  it('shows an empty state when the VM is not running', () => {
    vi.mocked(useConsoleSession).mockReturnValue({
      connectionState: 'idle',
      connect: vi.fn(),
      errorMessage: null,
      errorKind: 'generic',
      reportViewerError: vi.fn(),
      takeOver: vi.fn(),
      webSocket: null,
    });

    renderTab(stoppedVm);

    expect(screen.getByRole('heading', { name: 'Console unavailable' })).toBeInTheDocument();
    expect(
      screen.getByText('The console is available when the virtual machine is running.'),
    ).toBeInTheDocument();
    expect(screen.queryByText('Connecting')).not.toBeInTheDocument();
  });

  it('loads the noVNC viewer code before calling connect', async () => {
    // Regression guard: the socket must not be created until the viewer code is ready to
    // attach to it (see novnc-rfb.ts's loadVncRfbConstructor for the race this prevents).
    const connect = vi.fn();
    vi.mocked(useConsoleSession).mockReturnValue({
      connectionState: 'idle',
      connect,
      errorMessage: null,
      errorKind: 'generic',
      reportViewerError: vi.fn(),
      takeOver: vi.fn(),
      webSocket: null,
    });

    renderTab(runningVm);

    expect(loadVncRfbConstructor).toHaveBeenCalled();
    expect(connect).not.toHaveBeenCalled();

    await waitFor(() => expect(connect).toHaveBeenCalledTimes(1));
  });

  it('does not load the noVNC code or connect when the VM is not running', () => {
    vi.mocked(useConsoleSession).mockReturnValue({
      connectionState: 'idle',
      connect: vi.fn(),
      errorMessage: null,
      errorKind: 'generic',
      reportViewerError: vi.fn(),
      takeOver: vi.fn(),
      webSocket: null,
    });

    renderTab(stoppedVm);

    expect(loadVncRfbConstructor).not.toHaveBeenCalled();
  });

  it('reports a viewer error instead of hanging when the noVNC code fails to load', async () => {
    // Regression guard: without a .catch() here, connect() is never called, webSocket
    // stays null, and the UI is stuck on the "Connecting" spinner forever — the same
    // failure mode this effect exists to prevent, just triggered a step earlier.
    vi.mocked(loadVncRfbConstructor).mockRejectedValueOnce(new Error('chunk load failed'));
    const connect = vi.fn();
    const reportViewerError = vi.fn();
    vi.mocked(useConsoleSession).mockReturnValue({
      connectionState: 'idle',
      connect,
      errorMessage: null,
      errorKind: 'generic',
      reportViewerError,
      takeOver: vi.fn(),
      webSocket: null,
    });

    renderTab(runningVm);

    await waitFor(() => expect(reportViewerError).toHaveBeenCalledWith('chunk load failed'));
    expect(connect).not.toHaveBeenCalled();
  });

  it('shows connection error details with no action for an unrelated failure', () => {
    // A dropped connection or ticket-creation failure gives no reason to suspect another
    // session, and there is no generic "retry" — nothing actionable to offer.
    vi.mocked(useConsoleSession).mockReturnValue({
      connectionState: 'error',
      connect: vi.fn(),
      errorMessage: 'WebSocket closed before the console connected (code 1006)',
      errorKind: 'generic',
      reportViewerError: vi.fn(),
      takeOver: vi.fn(),
      webSocket: null,
    });

    renderTab(runningVm);

    expect(screen.getByText('Console connection failed')).toBeInTheDocument();
    expect(
      screen.getByText('WebSocket closed before the console connected (code 1006)'),
    ).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Retry' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Take over' })).not.toBeInTheDocument();
  });

  it('offers no action when a handshake failure might be a conflict', () => {
    // takeOver would resend the identical request an automatic connect already sent, so
    // it cannot do anything a retry wouldn't — see useConsoleSession.ts's ConsoleErrorKind
    // doc comment for why only 'siblingTabConflict' offers takeOver.
    vi.mocked(useConsoleSession).mockReturnValue({
      connectionState: 'error',
      connect: vi.fn(),
      errorMessage: 'This might be because the console is already open in another session.',
      errorKind: 'possibleConflict',
      reportViewerError: vi.fn(),
      takeOver: vi.fn(),
      webSocket: null,
    });

    renderTab(runningVm);

    expect(screen.queryByRole('button', { name: 'Take over' })).not.toBeInTheDocument();
  });

  it('offers Take over when a sibling tab already holds the console lock', () => {
    vi.mocked(useConsoleSession).mockReturnValue({
      connectionState: 'error',
      connect: vi.fn(),
      errorMessage: 'This console is already open in another tab in this browser.',
      errorKind: 'siblingTabConflict',
      reportViewerError: vi.fn(),
      takeOver: vi.fn(),
      webSocket: null,
    });

    renderTab(runningVm);

    expect(screen.getByRole('button', { name: 'Take over' })).toBeInTheDocument();
  });

  it('offers no action for viewer errors (the session itself is fine)', () => {
    vi.mocked(useConsoleSession).mockReturnValue({
      connectionState: 'error',
      connect: vi.fn(),
      errorMessage: 'Timed out waiting for the graphical console to finish connecting',
      errorKind: 'viewer',
      reportViewerError: vi.fn(),
      takeOver: vi.fn(),
      webSocket: null,
    });

    renderTab(runningVm);

    expect(screen.queryByRole('button', { name: 'Retry' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Take over' })).not.toBeInTheDocument();
  });

  it('renders the console controls and viewer in separate page sections', () => {
    vi.mocked(useConsoleSession).mockReturnValue({
      connectionState: 'connected',
      connect: vi.fn(),
      errorMessage: null,
      errorKind: 'generic',
      reportViewerError: vi.fn(),
      takeOver: vi.fn(),
      webSocket: {} as WebSocket,
    });

    renderTab(runningVm);

    expect(document.querySelectorAll('.pf-v6-c-page__main-section')).toHaveLength(2);
    expect(
      screen.getByRole('button', { name: 'Select console type' }).closest('section'),
    ).toHaveClass('vm-console-toolbar-section');
    expect(screen.getByText('VNC viewer').closest('section')).toHaveClass(
      'vm-console-view-section',
    );
  });

  it('keeps showing the connecting empty state until the viewer reports it has connected', () => {
    vi.mocked(useConsoleSession).mockReturnValue({
      connectionState: 'connected',
      connect: vi.fn(),
      errorMessage: null,
      errorKind: 'generic',
      reportViewerError: vi.fn(),
      takeOver: vi.fn(),
      webSocket: {} as WebSocket,
    });

    renderTab(runningVm);

    expect(screen.getByText('Connecting')).toBeInTheDocument();
    expect(screen.getByText('Connecting').closest('section')).toHaveClass('pf-m-fill');

    act(() => {
      latestOnConnectedRef.current?.();
    });

    expect(screen.queryByText('Connecting')).not.toBeInTheDocument();
    expect(focusViewer).toHaveBeenCalled();
  });

  it('shows the connecting empty state while disconnected (no blank viewport)', () => {
    vi.mocked(useConsoleSession).mockReturnValue({
      connectionState: 'disconnected',
      connect: vi.fn(),
      errorMessage: null,
      errorKind: 'generic',
      reportViewerError: vi.fn(),
      takeOver: vi.fn(),
      webSocket: null,
    });

    renderTab(runningVm);

    expect(screen.getByRole('heading', { name: 'Connecting' })).toBeInTheDocument();
    expect(screen.queryByText('VNC viewer')).not.toBeInTheDocument();
  });

  it('mounts the VNC viewer as soon as a webSocket exists, before connectionState is connected', () => {
    // Regression guard: noVNC must attach to the WebSocket while it's still CONNECTING,
    // not after it opens — otherwise its onopen/onmessage handlers are wired up too late
    // and the handshake silently stalls (see novnc-rfb.ts's loadVncRfbConstructor).
    // Gating the viewer's mount on connectionState === 'connected' reintroduces that race.
    vi.mocked(useConsoleSession).mockReturnValue({
      connectionState: 'connecting',
      connect: vi.fn(),
      errorMessage: null,
      errorKind: 'generic',
      reportViewerError: vi.fn(),
      takeOver: vi.fn(),
      webSocket: {} as WebSocket,
    });

    renderTab(runningVm);

    expect(screen.getByText('VNC viewer')).toBeInTheDocument();
  });

  it('passes a stable onConnected callback to the viewer across parent re-renders', () => {
    vi.mocked(useConsoleSession).mockReturnValue({
      connectionState: 'connected',
      connect: vi.fn(),
      errorMessage: null,
      errorKind: 'generic',
      reportViewerError: vi.fn(),
      takeOver: vi.fn(),
      webSocket: {} as WebSocket,
    });

    const { rerender } = renderTab(runningVm);

    const firstOnConnected = latestOnConnectedRef.current;

    // Simulates an unrelated parent re-render (e.g. VM details polling) with a new vm reference.
    rerender(<VmConsoleTab vm={{ ...runningVm }} />);

    expect(latestOnConnectedRef.current).toBe(firstOnConnected);
  });

  const lastConsoleType = () => vi.mocked(useConsoleSession).mock.calls.at(-1)?.[0].consoleType;

  it('defaults to the VNC transport and mints a VNC ticket', () => {
    vi.mocked(useConsoleSession).mockReturnValue({
      connectionState: 'connecting',
      connect: vi.fn(),
      errorMessage: null,
      errorKind: 'generic',
      reportViewerError: vi.fn(),
      takeOver: vi.fn(),
      webSocket: {} as WebSocket,
    });

    renderTab(runningVm);

    expect(screen.getByText('VNC viewer')).toBeInTheDocument();
    expect(screen.queryByText('Serial viewer')).not.toBeInTheDocument();
    expect(lastConsoleType()).toBe(ConsoleType.VNC);
  });

  it('switches to the serial viewer and mints a SERIAL ticket when Serial is selected', async () => {
    const user = userEvent.setup();
    vi.mocked(useConsoleSession).mockReturnValue({
      connectionState: 'connecting',
      connect: vi.fn(),
      errorMessage: null,
      errorKind: 'generic',
      reportViewerError: vi.fn(),
      takeOver: vi.fn(),
      webSocket: {} as WebSocket,
    });

    renderTab(runningVm);
    expect(screen.getByText('VNC viewer')).toBeInTheDocument();

    await user.click(screen.getByRole('button', { name: 'Select console type' }));
    await user.click(screen.getByRole('option', { name: 'Serial console' }));

    // Remounts on the new transport: serial viewer replaces VNC and the ticket type flips.
    expect(screen.getByText('Serial viewer')).toBeInTheDocument();
    expect(screen.queryByText('VNC viewer')).not.toBeInTheDocument();
    expect(lastConsoleType()).toBe(ConsoleType.SERIAL);
  });
});
