// @refresh reload — depends on useConsoleSession hook signature
import { type ReactNode, useCallback, useEffect, useRef, useState } from 'react';
import { Bullseye, EmptyState, EmptyStateBody, PageSection, Spinner } from '@patternfly/react-core';

import { ConsoleResourceType, ConsoleType } from '@osac/types';

import { useTranslation } from '../../../hooks/useTranslation';
import {
  CONSOLE_CONNECTING_OVERLAY_CLASS_NAME,
  CONSOLE_STACK_CLASS_NAME,
  CONSOLE_TOOLBAR_SECTION_CLASS_NAME,
  CONSOLE_VIEWPORT_HIDDEN_CLASS_NAME,
  CONSOLE_VIEW_SECTION_CLASS_NAME,
} from '../../Console/console-viewport';
import type { ConsoleTransport } from '../../Console/console.types';
import ConsoleToolbar from '../../Console/ConsoleToolbar';
import { loadVncRfbConstructor } from '../../Console/novnc-rfb';
import SerialConsoleViewer from '../../Console/SerialConsoleViewer';
import { useConsoleSession } from '../../Console/useConsoleSession';
import VncConsoleViewer, { type VncConsoleViewerHandle } from '../../Console/VncConsoleViewer';
import { loadXtermConstructors } from '../../Console/xterm-loader';
import QueryErrorState from '../../Resource/QueryErrorState';

// SerialConsoleViewerHandle is structurally identical, so one ref type drives either viewer.
type ConsoleViewerHandle = VncConsoleViewerHandle;

const CONSOLE_TYPE_BY_TRANSPORT: Record<ConsoleTransport, ConsoleType> = {
  vnc: ConsoleType.VNC,
  serial: ConsoleType.SERIAL,
};

interface Props {
  resourceId: string;
  transport: ConsoleTransport;
  onTransportChange: (transport: ConsoleTransport) => void;
}

/**
 * Owns the console session, viewer, and toolbar for a single transport. The parent
 * mounts this keyed by transport, so switching transport remounts the whole subtree —
 * reusing useConsoleSession's unmount cleanup (close socket, clear ticket cookie, release
 * lock) instead of teaching the hook to reconnect with a different type. Only rendered
 * while the VM is running, so the session always starts (isRunning is a constant true).
 */
const VmConsoleView = ({ resourceId, transport, onTransportChange }: Props) => {
  const { t } = useTranslation();
  const isSerial = transport === 'serial';
  const {
    connectionState,
    connect,
    errorMessage,
    errorKind,
    reportViewerError,
    takeOver,
    webSocket,
  } = useConsoleSession({
    resourceType: ConsoleResourceType.COMPUTE_INSTANCE,
    resourceId,
    isRunning: true,
    consoleType: CONSOLE_TYPE_BY_TRANSPORT[transport],
  });
  const viewerRef = useRef<ConsoleViewerHandle>(null);
  // Computed here (where the i18next-cli extractor's static scan can see the translation
  // calls) and read via ref inside the effect below instead of listing t itself as a
  // dependency: t's identity can change mid-connection while i18next-http-backend is still
  // loading translations, and that must not re-trigger loadThenConnect() (which would call
  // connect() a second time, leaking the first WebSocket/session).
  const failedToLoadViewerMessage = isSerial
    ? t('Failed to load serial console viewer')
    : t('Failed to load graphical console viewer');
  const failedToLoadViewerMessageRef = useRef(failedToLoadViewerMessage);
  failedToLoadViewerMessageRef.current = failedToLoadViewerMessage;
  // Compare by socket identity: while mounted the session hook can replace webSocket (e.g.
  // VM stop→start) without remounting — keep "Connecting" until the viewer reports ready
  // for that new socket.
  const [viewerReadySocket, setViewerReadySocket] = useState<WebSocket>();
  const isViewerConnected = viewerReadySocket === webSocket;

  const handleViewerConnected = useCallback(() => {
    if (webSocket) {
      setViewerReadySocket(webSocket);
    }
  }, [webSocket]);

  // Loads the viewer code (noVNC or xterm) before ever creating the session/socket, so that
  // by the time the socket exists and the viewer mounts it can attach its listeners
  // immediately — before the backend's first bytes (sent the instant the socket opens) can
  // arrive with nothing listening and be dropped. transport is fixed for this mount (parent
  // remounts on change), so this effect binds one viewer loader for the session's lifetime.
  useEffect(() => {
    let cancelled = false;
    const loadThenConnect = async () => {
      try {
        await (isSerial ? loadXtermConstructors() : loadVncRfbConstructor());
        if (!cancelled) {
          connect();
        }
      } catch (error) {
        if (!cancelled) {
          reportViewerError(
            error instanceof Error ? error.message : failedToLoadViewerMessageRef.current,
          );
        }
      }
    };
    void loadThenConnect();

    return () => {
      cancelled = true;
    };
  }, [connect, isSerial, reportViewerError]);

  // Restore keyboard focus after the viewer connects so typing reaches the guest.
  useEffect(() => {
    if (!isViewerConnected) {
      return;
    }
    viewerRef.current?.focus();
  }, [isViewerConnected]);

  const connecting = (
    <Bullseye className={CONSOLE_CONNECTING_OVERLAY_CLASS_NAME}>
      <EmptyState titleText={t('Connecting')} icon={Spinner} headingLevel="h3">
        <EmptyStateBody>{t('Establishing console connection...')}</EmptyStateBody>
      </EmptyState>
    </Bullseye>
  );

  let viewport: ReactNode;
  if (connectionState === 'error') {
    // Take over only ever sends this browser's persisted client id — exactly what a plain
    // reconnect already sends — so it can only change the outcome when the conflicting
    // session was created with that same id, i.e. by this same browser. That is only
    // confirmed for 'siblingTabConflict'; every other error (including a merely *possible*
    // conflict) offers no action, since there is nothing takeOver could do differently.
    const canTakeOver = errorKind === 'siblingTabConflict';
    viewport = (
      <QueryErrorState
        error={errorMessage}
        title={t('Console connection failed')}
        secondaryAction={canTakeOver ? { label: t('Take over'), onClick: takeOver } : undefined}
      />
    );
  } else if (!webSocket) {
    viewport = connecting;
  } else {
    const viewerClassName = isViewerConnected ? undefined : CONSOLE_VIEWPORT_HIDDEN_CLASS_NAME;
    viewport = (
      <>
        {isSerial ? (
          <SerialConsoleViewer
            ref={viewerRef}
            className={viewerClassName}
            onConnected={handleViewerConnected}
            onError={reportViewerError}
            webSocket={webSocket}
          />
        ) : (
          <VncConsoleViewer
            ref={viewerRef}
            className={viewerClassName}
            onConnected={handleViewerConnected}
            onError={reportViewerError}
            webSocket={webSocket}
          />
        )}
        {!isViewerConnected && connecting}
      </>
    );
  }

  return (
    <>
      <PageSection hasBodyWrapper={false} className={CONSOLE_TOOLBAR_SECTION_CLASS_NAME}>
        <ConsoleToolbar
          connectionState={connectionState}
          onPaste={() => void viewerRef.current?.pasteFromClipboard()}
          consoleTransport={transport}
          onConsoleTransportChange={onTransportChange}
        />
      </PageSection>
      <PageSection hasBodyWrapper={false} isFilled className={CONSOLE_VIEW_SECTION_CLASS_NAME}>
        <div className={CONSOLE_STACK_CLASS_NAME}>{viewport}</div>
      </PageSection>
    </>
  );
};

export default VmConsoleView;
