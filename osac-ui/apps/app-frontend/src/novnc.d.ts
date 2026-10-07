// @osac/ui-components imports `@novnc/novnc` dynamically, but the
// package ships no types. This mirrors the same ambient declaration ui-components
// keeps for its own compilation in
// libs/ui-components/src/components/Console/novnc.d.ts — TypeScript ambient
// declarations only apply within the program that includes them, and
// app-frontend consumes ui-components as source (no build/types boundary), so
// each package's own tsconfig needs its own copy rather than app-frontend
// reaching into ui-components' internal file layout.
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
