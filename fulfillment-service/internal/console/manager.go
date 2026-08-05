/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package console

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"time"
)

// ManagerBuilder builds a Manager.
type ManagerBuilder struct {
	logger         *slog.Logger
	backends       map[string]Backend
	sessionTimeout time.Duration
}

// Manager manages console sessions and dispatches to the correct backend.
type Manager struct {
	logger         *slog.Logger
	backends       map[string]Backend
	sessionTimeout time.Duration
	sessions       map[string]*session
	sessionsLock   sync.Mutex
}

type session struct {
	resourceKey string
	user        string
	clientID    string
	startedAt   time.Time
	ctx         context.Context
	cancel      context.CancelFunc
}

// NewManager creates a new builder for the console manager.
func NewManager() *ManagerBuilder {
	return &ManagerBuilder{
		backends:       make(map[string]Backend),
		sessionTimeout: DefaultSessionTimeout,
	}
}

func (b *ManagerBuilder) SetLogger(value *slog.Logger) *ManagerBuilder {
	b.logger = value
	return b
}

func (b *ManagerBuilder) SetSessionTimeout(value time.Duration) *ManagerBuilder {
	b.sessionTimeout = value
	return b
}

func (b *ManagerBuilder) AddBackend(resourceType string, backend Backend) *ManagerBuilder {
	b.backends[resourceType] = backend
	return b
}

func (b *ManagerBuilder) Build() (*Manager, error) {
	if b.logger == nil {
		return nil, errors.New("logger is mandatory")
	}
	if len(b.backends) == 0 {
		return nil, errors.New("at least one backend is required")
	}
	return &Manager{
		logger:         b.logger,
		backends:       b.backends,
		sessionTimeout: b.sessionTimeout,
		sessions:       make(map[string]*session),
	}, nil
}

// ConnectResult holds the result of a successful Manager.Connect call.
type ConnectResult struct {
	// Conn is the bidirectional connection to the backend console.
	Conn io.ReadWriteCloser

	// SessionCtx is cancelled when the session ends — by eviction, timeout,
	// or parent context cancellation. Callers should pass this to the relay
	// so that session lifecycle events terminate the proxy.
	SessionCtx context.Context
}

// Connect establishes a console connection to the target resource.
// The returned ConnectResult contains both the connection and the session context.
//
// target.BackendURI is the session identity for deduplication — two connections
// with the same BackendURI are considered the same console endpoint.
//
// If clientID is non-empty and matches an existing session from the same user,
// the stale session is evicted and the new connection is admitted. This handles
// reconnection after unclean TCP disconnects.
func (m *Manager) Connect(ctx context.Context, target Target, user, clientID string) (*ConnectResult, error) {
	if target.BackendURI == "" {
		return nil, fmt.Errorf("backend URI is required")
	}

	backend, ok := m.backends[target.ResourceType]
	if !ok {
		return nil, fmt.Errorf("unsupported resource type %q", target.ResourceType)
	}

	sessionKey := target.BackendURI

	var oldCancel context.CancelFunc

	m.sessionsLock.Lock()
	if existing, ok := m.sessions[sessionKey]; ok {
		if clientID != "" && existing.clientID == clientID && existing.user == user {
			m.logger.InfoContext(ctx, "Evicting stale console session",
				slog.String("resource", sessionKey),
				slog.String("user", user),
				slog.String("client_id", clientID),
				slog.Duration("age", time.Since(existing.startedAt)),
			)
			oldCancel = existing.cancel
			delete(m.sessions, sessionKey)
		} else {
			m.sessionsLock.Unlock()
			return nil, &ErrSessionExists{
				Resource: sessionKey,
				User:     existing.user,
				Since:    existing.startedAt.Format(time.RFC3339),
			}
		}
	}

	// Create session with timeout.
	sessionCtx, sessionCancel := context.WithTimeout(ctx, m.sessionTimeout)
	s := &session{
		resourceKey: sessionKey,
		user:        user,
		clientID:    clientID,
		startedAt:   time.Now(),
		ctx:         sessionCtx,
		cancel:      sessionCancel,
	}
	m.sessions[sessionKey] = s
	m.sessionsLock.Unlock()

	if oldCancel != nil {
		oldCancel()
	}

	m.logger.InfoContext(ctx, "Opening console session",
		slog.String("resource", sessionKey),
		slog.String("user", user),
		slog.Duration("timeout", m.sessionTimeout),
	)

	conn, err := backend.Connect(sessionCtx, target)
	if err != nil {
		m.removeSession(sessionKey, s)
		sessionCancel()
		return nil, err
	}

	return &ConnectResult{
		Conn: &managedConnection{
			ReadWriteCloser: conn,
			manager:         m,
			session:         s,
		},
		SessionCtx: sessionCtx,
	}, nil
}

// ActiveSessions returns the number of active console sessions.
func (m *Manager) ActiveSessions() int {
	m.sessionsLock.Lock()
	defer m.sessionsLock.Unlock()
	return len(m.sessions)
}

// CancelSessions cancels all active session contexts, causing their proxy
// goroutines to shut down asynchronously. It does not wait for the goroutines
// to finish — callers should allow a grace period for in-flight operations
// (e.g., sending disconnect status messages) to complete.
func (m *Manager) CancelSessions() {
	m.sessionsLock.Lock()
	for key, s := range m.sessions {
		m.logger.Info("Cancelling console session",
			slog.String("resource", key),
			slog.String("user", s.user),
		)
		s.cancel()
	}
	m.sessionsLock.Unlock()
}

func (m *Manager) removeSession(key string, owner *session) {
	m.sessionsLock.Lock()
	defer m.sessionsLock.Unlock()
	if m.sessions[key] == owner {
		delete(m.sessions, key)
	}
}

// managedConnection wraps an io.ReadWriteCloser and removes the session on close.
type managedConnection struct {
	io.ReadWriteCloser
	manager   *Manager
	session   *session
	closeOnce sync.Once
}

func (c *managedConnection) Close() error {
	var err error
	c.closeOnce.Do(func() {
		attrs := []any{
			slog.String("resource", c.session.resourceKey),
			slog.String("user", c.session.user),
			slog.Duration("duration", time.Since(c.session.startedAt)),
		}
		if ctxErr := c.session.ctx.Err(); ctxErr != nil {
			attrs = append(attrs, slog.String("reason", ctxErr.Error()))
		}
		c.manager.logger.Info("Closing console session", attrs...)
		err = c.ReadWriteCloser.Close()
		c.manager.removeSession(c.session.resourceKey, c.session)
		c.session.cancel()
	})
	return err
}
