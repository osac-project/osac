import { describe, expect, it, vi } from 'vitest';

describe('loadVncRfbConstructor', () => {
  it('unwraps a double-nested default export from the CJS module', async () => {
    const RFBClass = vi.fn();
    vi.doMock('@novnc/novnc', () => ({
      default: { default: RFBClass },
    }));

    const { loadVncRfbConstructor } = await import('./novnc-rfb');
    const RFB = await loadVncRfbConstructor();

    expect(RFB).toBe(RFBClass);
    vi.doUnmock('@novnc/novnc');
  });

  it('unwraps a single nested default export from the CJS module', async () => {
    const RFBClass = vi.fn();
    vi.doMock('@novnc/novnc', () => ({
      default: RFBClass,
    }));

    const { loadVncRfbConstructor } = await import('./novnc-rfb');
    const RFB = await loadVncRfbConstructor();

    expect(RFB).toBe(RFBClass);
    vi.doUnmock('@novnc/novnc');
  });
});
