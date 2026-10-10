declare module '@novnc/novnc' {
  export default class RFB {
    constructor(target: HTMLElement, urlOrChannel: string | WebSocket);

    scaleViewport: boolean;

    background: string;

    disconnect: () => void;

    focus: () => void;

    sendKey: (keysym: number, code: string, down?: boolean) => void;

    addEventListener: (type: string, listener: () => void) => void;

    removeEventListener: (type: string, listener: () => void) => void;
  }
}
