export interface VncRfbInstance {
  scaleViewport: boolean;
  background: string;
  disconnect: () => void;
  focus: () => void;
  sendKey: (keysym: number, code: string, down?: boolean) => void;
  addEventListener: (type: string, listener: () => void) => void;
  removeEventListener: (type: string, listener: () => void) => void;
}

export type VncRfbConstructor = new (
  target: HTMLElement,
  urlOrChannel: string | WebSocket,
) => VncRfbInstance;

/**
 * noVNC ships as CommonJS. Vite 8 may wrap exports so the RFB class is nested
 * under `.default` or `.default.default` instead of being the default import.
 *
 * Callers should await this *before* creating the console WebSocket (see
 * VmConsoleTab), not after: noVNC's Websock.attach() sets the raw WebSocket's
 * onopen/onmessage handlers via property assignment, so if the socket has already
 * reached OPEN (or already delivered its first message) before attach() runs, those
 * events are gone for good and the handshake stalls forever. The dynamic import here
 * is cached by the module loader, so calling this again later (once VncConsoleViewer
 * mounts) resolves instantly instead of re-fetching.
 */
export const loadVncRfbConstructor = async (): Promise<VncRfbConstructor> => {
  const module = (await import('@novnc/novnc')) as {
    default?: VncRfbConstructor | { default?: VncRfbConstructor };
  };
  const moduleDefault = module.default;
  const RFB =
    moduleDefault &&
    typeof moduleDefault === 'object' &&
    'default' in moduleDefault &&
    typeof moduleDefault.default === 'function'
      ? moduleDefault.default
      : typeof moduleDefault === 'function'
        ? moduleDefault
        : undefined;

  if (typeof RFB !== 'function') {
    throw new Error('Failed to load noVNC RFB constructor');
  }

  return RFB;
};
