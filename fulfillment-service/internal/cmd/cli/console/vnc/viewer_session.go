/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package vnc

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os/exec"
	"time"
)

// viewerSession manages the lifecycle of a single VNC viewer process and its
// TCP connection. In proxy-only mode (v is nil), it only manages the TCP
// connection accepted from an external VNC client.
type viewerSession struct {
	v        *viewer
	listener net.Listener

	proc    *exec.Cmd
	done    chan struct{}
	exitErr error
	conn    net.Conn
}

// newViewerSession creates a session that will use the given viewer binary
// (nil for proxy-only) and TCP listener.
func newViewerSession(v *viewer, listener net.Listener) *viewerSession {
	return &viewerSession{v: v, listener: listener}
}

// start launches the viewer process (unless proxy-only) and accepts its TCP
// connection. If the viewer exits before connecting, start returns nil and
// Conn() will return nil.
func (s *viewerSession) start(ctx context.Context) error {
	if s.v != nil {
		addr := s.listener.Addr().(*net.TCPAddr)
		viewerAddr := s.v.addrFunc(addr.IP.String(), addr.Port)
		var err error
		s.proc, err = launchViewer(s.v, viewerAddr)
		if err != nil {
			return err
		}
		s.done = make(chan struct{})
		go func() {
			s.exitErr = s.proc.Wait()
			close(s.done)
		}()
	}

	// Accept TCP, cancelling if the viewer exits before connecting.
	acceptCtx, acceptCancel := context.WithCancel(ctx)
	defer acceptCancel()
	if s.done != nil {
		go func() {
			select {
			case <-s.done:
				acceptCancel()
			case <-acceptCtx.Done():
			}
		}()
	}

	conn, err := acceptWithContext(acceptCtx, s.listener)
	if err != nil {
		if s.done != nil {
			select {
			case <-s.done:
				return nil // viewer exited before connecting
			default:
			}
		}
		return fmt.Errorf("failed to accept VNC connection: %w", err)
	}
	s.conn = conn
	return nil
}

// close tears down the TCP connection and viewer process. It returns true if
// the viewer had already exited on its own before close was called (i.e., the
// user closed the window).
func (s *viewerSession) close() (userClosed bool) {
	if s.conn != nil {
		s.conn.Close()
		s.conn = nil
	}
	if s.proc == nil {
		return false
	}
	// Check before killing: did the viewer exit on its own?
	select {
	case <-s.done:
		userClosed = true
	default:
	}
	_ = s.proc.Process.Kill()
	<-s.done
	s.proc = nil
	s.done = nil
	return userClosed
}

// Conn returns the accepted TCP connection, or nil if the viewer exited
// before connecting.
func (s *viewerSession) Conn() net.Conn {
	return s.conn
}

// ExitErr returns the viewer process exit error, if the process has exited.
// It returns nil when there is no viewer (proxy-only), the viewer has not yet
// exited, or the viewer exited successfully (exit code 0).
func (s *viewerSession) ExitErr() error {
	if s.done == nil {
		return s.exitErr
	}
	select {
	case <-s.done:
		return s.exitErr
	default:
		return nil
	}
}

// acceptWithContext wraps listener.Accept with context cancellation support.
// It polls with short deadlines so the accept unblocks when ctx is cancelled.
func acceptWithContext(ctx context.Context, ln net.Listener) (net.Conn, error) {
	tcpLn, ok := ln.(*net.TCPListener)
	if !ok {
		// Should never happen
		return nil, fmt.Errorf("expected *net.TCPListener, got %T", ln)
	}
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := tcpLn.SetDeadline(time.Now().Add(time.Second)); err != nil {
			return nil, fmt.Errorf("failed to set accept deadline: %w", err)
		}
		conn, err := ln.Accept() // Block for 1 sec
		if err != nil {
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				continue
			}
			return nil, err
		}
		return conn, nil
	}
}
