/** UI connection lifecycle — distinct from protobuf ConsoleConnectionState. */
export type ConsoleUiConnectionState =
  'idle' | 'connecting' | 'connected' | 'disconnected' | 'error';

/**
 * UI-facing console transport choice, decoupled from the generated numeric
 * protobuf ConsoleType enum (which callers map to at the API boundary). Drives
 * the console tab's transport selector.
 */
export type ConsoleTransport = 'vnc' | 'serial';
