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
	"bytes"
	"context"
	"io"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// mockBackend is a test Backend that returns a mockConnection.
type mockBackend struct {
	connectFunc func(ctx context.Context, target Target) (io.ReadWriteCloser, error)
}

func (b *mockBackend) Connect(ctx context.Context, target Target) (io.ReadWriteCloser, error) {
	return b.connectFunc(ctx, target)
}

// mockConnection is a simple in-memory ReadWriteCloser for testing.
type mockConnection struct {
	readBuf  *bytes.Buffer
	writeBuf *bytes.Buffer
	closed   bool
}

func newMockConnection() *mockConnection {
	return &mockConnection{
		readBuf:  bytes.NewBufferString("hello from vm\n"),
		writeBuf: &bytes.Buffer{},
	}
}

func (c *mockConnection) Read(p []byte) (int, error) {
	if c.closed {
		return 0, io.EOF
	}
	return c.readBuf.Read(p)
}

func (c *mockConnection) Write(p []byte) (int, error) {
	if c.closed {
		return 0, io.ErrClosedPipe
	}
	return c.writeBuf.Write(p)
}

func (c *mockConnection) Close() error {
	c.closed = true
	return nil
}

// slowMockConnection blocks on Read until context cancels, simulating a long-lived connection.
type slowMockConnection struct {
	ctx    context.Context
	closed bool
}

func newSlowMockConnection(ctx context.Context) *slowMockConnection {
	return &slowMockConnection{ctx: ctx}
}

func (c *slowMockConnection) Read(p []byte) (int, error) {
	<-c.ctx.Done()
	return 0, c.ctx.Err()
}

func (c *slowMockConnection) Write(p []byte) (int, error) {
	if c.closed {
		return 0, io.ErrClosedPipe
	}
	return len(p), nil
}

func (c *slowMockConnection) Close() error {
	c.closed = true
	return nil
}

// contextAwareBackend creates connections that respect context cancellation.
type contextAwareBackend struct{}

func (b *contextAwareBackend) Connect(ctx context.Context, target Target) (io.ReadWriteCloser, error) {
	return newSlowMockConnection(ctx), nil
}

var _ = Describe("Manager", func() {
	var (
		backend *mockBackend
		mgr     *Manager
	)

	BeforeEach(func() {
		backend = &mockBackend{
			connectFunc: func(ctx context.Context, target Target) (io.ReadWriteCloser, error) {
				return newMockConnection(), nil
			},
		}
		var err error
		mgr, err = NewManager().
			SetLogger(logger).
			SetSessionTimeout(5*time.Second).
			AddBackend("compute_instance", backend).
			Build()
		Expect(err).NotTo(HaveOccurred())
	})

	Describe("Build", func() {
		It("should fail without logger", func() {
			_, err := NewManager().
				AddBackend("test", backend).
				Build()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("logger"))
		})

		It("should fail without backends", func() {
			_, err := NewManager().
				SetLogger(logger).
				Build()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("backend"))
		})
	})

	Describe("Connect", func() {
		It("should establish a connection", func() {
			ctx := context.Background()
			target := Target{
				ResourceType: "compute_instance",
				BackendURI:   "wss://hub/ns/vm-1/console",
			}

			result, err := mgr.Connect(ctx, target, "testuser", "")
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Conn).NotTo(BeNil())
			Expect(result.SessionCtx).NotTo(BeNil())
			Expect(mgr.ActiveSessions()).To(Equal(1))

			err = result.Conn.Close()
			Expect(err).NotTo(HaveOccurred())
			Expect(mgr.ActiveSessions()).To(Equal(0))
		})

		It("should reject missing backend URI", func() {
			ctx := context.Background()
			target := Target{ResourceType: "compute_instance"}
			_, err := mgr.Connect(ctx, target, "testuser", "")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("backend URI is required"))
		})

		It("should reject unsupported resource type", func() {
			ctx := context.Background()
			target := Target{ResourceType: "unknown", BackendURI: "wss://hub/unknown"}
			_, err := mgr.Connect(ctx, target, "testuser", "")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("unsupported"))
		})

		It("should reject concurrent sessions on same resource", func() {
			ctx := context.Background()
			target := Target{
				ResourceType: "compute_instance",
				BackendURI:   "wss://hub/ns/test-123/console",
			}

			result1, err := mgr.Connect(ctx, target, "user1", "")
			Expect(err).NotTo(HaveOccurred())

			_, err = mgr.Connect(ctx, target, "user2", "")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("already has an active console session"))

			result1.Conn.Close()

			// Should succeed after the first session is closed.
			result2, err := mgr.Connect(ctx, target, "user2", "")
			Expect(err).NotTo(HaveOccurred())
			result2.Conn.Close()
		})

		It("should allow simultaneous serial and VNC sessions to the same resource", func() {
			ctx := context.Background()
			serialTarget := Target{
				ResourceType: "compute_instance",
				BackendURI:   "wss://hub/ns/test-123/console",
			}
			vncTarget := Target{
				ResourceType: "compute_instance",
				BackendURI:   "wss://hub/ns/test-123/vnc",
			}

			result1, err := mgr.Connect(ctx, serialTarget, "user1", "")
			Expect(err).NotTo(HaveOccurred())

			result2, err := mgr.Connect(ctx, vncTarget, "user1", "")
			Expect(err).NotTo(HaveOccurred())

			Expect(mgr.ActiveSessions()).To(Equal(2))

			result1.Conn.Close()
			result2.Conn.Close()
			Expect(mgr.ActiveSessions()).To(Equal(0))
		})

		It("should reject concurrent sessions of the same console type to the same resource", func() {
			ctx := context.Background()
			target := Target{
				ResourceType: "compute_instance",
				BackendURI:   "wss://hub/ns/test-123/vnc",
			}

			result1, err := mgr.Connect(ctx, target, "user1", "")
			Expect(err).NotTo(HaveOccurred())

			_, err = mgr.Connect(ctx, target, "user2", "")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("already has an active console session"))

			result1.Conn.Close()
		})

		It("should allow sessions to different resources", func() {
			ctx := context.Background()
			target1 := Target{ResourceType: "compute_instance", BackendURI: "wss://hub/ns/vm-1/console"}
			target2 := Target{ResourceType: "compute_instance", BackendURI: "wss://hub/ns/vm-2/console"}

			result1, err := mgr.Connect(ctx, target1, "user1", "")
			Expect(err).NotTo(HaveOccurred())

			result2, err := mgr.Connect(ctx, target2, "user1", "")
			Expect(err).NotTo(HaveOccurred())

			Expect(mgr.ActiveSessions()).To(Equal(2))

			result1.Conn.Close()
			result2.Conn.Close()
			Expect(mgr.ActiveSessions()).To(Equal(0))
		})

		It("should handle double close gracefully", func() {
			ctx := context.Background()
			target := Target{ResourceType: "compute_instance", BackendURI: "wss://hub/ns/vm-1/console"}
			result, err := mgr.Connect(ctx, target, "user1", "")
			Expect(err).NotTo(HaveOccurred())

			err = result.Conn.Close()
			Expect(err).NotTo(HaveOccurred())

			err = result.Conn.Close()
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Describe("Session Timeout", func() {
		It("should cancel the session context after timeout", func() {
			timeoutMgr, err := NewManager().
				SetLogger(logger).
				SetSessionTimeout(100*time.Millisecond).
				AddBackend("compute_instance", &contextAwareBackend{}).
				Build()
			Expect(err).NotTo(HaveOccurred())

			ctx := context.Background()
			target := Target{
				ResourceType: "compute_instance",
				BackendURI:   "wss://hub/ns/timeout-test/console",
			}

			result, err := timeoutMgr.Connect(ctx, target, "testuser", "")
			Expect(err).NotTo(HaveOccurred())
			Expect(timeoutMgr.ActiveSessions()).To(Equal(1))

			// Read should block until timeout fires, then return context.DeadlineExceeded.
			buf := make([]byte, 64)
			_, readErr := result.Conn.Read(buf)
			Expect(readErr).To(HaveOccurred())
			Expect(readErr.Error()).To(ContainSubstring("deadline exceeded"))

			result.Conn.Close()
			Expect(timeoutMgr.ActiveSessions()).To(Equal(0))
		})

		It("should allow new session after timeout-expired session is closed", func() {
			timeoutMgr, err := NewManager().
				SetLogger(logger).
				SetSessionTimeout(50*time.Millisecond).
				AddBackend("compute_instance", &contextAwareBackend{}).
				Build()
			Expect(err).NotTo(HaveOccurred())

			ctx := context.Background()
			target := Target{
				ResourceType: "compute_instance",
				BackendURI:   "wss://hub/ns/timeout-reuse/console",
			}

			result1, err := timeoutMgr.Connect(ctx, target, "user1", "")
			Expect(err).NotTo(HaveOccurred())

			// Wait for timeout.
			time.Sleep(100 * time.Millisecond)
			result1.Conn.Close()

			// Should be able to open a new session.
			result2, err := timeoutMgr.Connect(ctx, target, "user2", "")
			Expect(err).NotTo(HaveOccurred())
			result2.Conn.Close()
		})
	})

	Describe("CancelSessions", func() {
		It("should cancel all active sessions", func() {
			ctx := context.Background()
			target1 := Target{ResourceType: "compute_instance", BackendURI: "wss://hub/ns/vm-1/console"}
			target2 := Target{ResourceType: "compute_instance", BackendURI: "wss://hub/ns/vm-2/console"}

			result1, err := mgr.Connect(ctx, target1, "user1", "")
			Expect(err).NotTo(HaveOccurred())
			result2, err := mgr.Connect(ctx, target2, "user2", "")
			Expect(err).NotTo(HaveOccurred())

			Expect(mgr.ActiveSessions()).To(Equal(2))

			mgr.CancelSessions()

			result1.Conn.Close()
			result2.Conn.Close()
		})
	})

	Describe("Session Eviction", func() {
		It("should evict stale session with same client_id and user", func() {
			ctx := context.Background()
			target := Target{ResourceType: "compute_instance", BackendURI: "wss://hub/ns/test-123/console"}

			result1, err := mgr.Connect(ctx, target, "user1", "client-abc")
			Expect(err).NotTo(HaveOccurred())
			Expect(mgr.ActiveSessions()).To(Equal(1))

			result2, err := mgr.Connect(ctx, target, "user1", "client-abc")
			Expect(err).NotTo(HaveOccurred())
			Expect(mgr.ActiveSessions()).To(Equal(1))

			// Old connection Close should be a no-op for the session map.
			result1.Conn.Close()
			Expect(mgr.ActiveSessions()).To(Equal(1))

			result2.Conn.Close()
			Expect(mgr.ActiveSessions()).To(Equal(0))
		})

		It("should cancel evicted session context", func() {
			ctx := context.Background()
			target := Target{ResourceType: "compute_instance", BackendURI: "wss://hub/ns/test-123/console"}

			result1, err := mgr.Connect(ctx, target, "user1", "client-abc")
			Expect(err).NotTo(HaveOccurred())
			Expect(result1.SessionCtx.Err()).To(Succeed())

			result2, err := mgr.Connect(ctx, target, "user1", "client-abc")
			Expect(err).NotTo(HaveOccurred())

			// Eviction should have cancelled the old session context.
			Expect(result1.SessionCtx.Err()).NotTo(Succeed())
			// New session context should still be active.
			Expect(result2.SessionCtx.Err()).To(Succeed())

			result1.Conn.Close()
			result2.Conn.Close()
		})

		It("should reject when same user has different client_id", func() {
			ctx := context.Background()
			target := Target{ResourceType: "compute_instance", BackendURI: "wss://hub/ns/test-123/console"}

			result1, err := mgr.Connect(ctx, target, "user1", "client-abc")
			Expect(err).NotTo(HaveOccurred())

			_, err = mgr.Connect(ctx, target, "user1", "client-xyz")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("already has an active console session"))

			result1.Conn.Close()
		})

		It("should reject when different user has same client_id", func() {
			ctx := context.Background()
			target := Target{ResourceType: "compute_instance", BackendURI: "wss://hub/ns/test-123/console"}

			result1, err := mgr.Connect(ctx, target, "user1", "client-abc")
			Expect(err).NotTo(HaveOccurred())

			_, err = mgr.Connect(ctx, target, "user2", "client-abc")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("already has an active console session"))

			result1.Conn.Close()
		})

		It("should not evict when client_id is empty", func() {
			ctx := context.Background()
			target := Target{ResourceType: "compute_instance", BackendURI: "wss://hub/ns/test-123/console"}

			result1, err := mgr.Connect(ctx, target, "user1", "")
			Expect(err).NotTo(HaveOccurred())

			_, err = mgr.Connect(ctx, target, "user1", "")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("already has an active console session"))

			result1.Conn.Close()
		})

		It("should not evict when existing session has no client_id", func() {
			ctx := context.Background()
			target := Target{ResourceType: "compute_instance", BackendURI: "wss://hub/ns/test-123/console"}

			result1, err := mgr.Connect(ctx, target, "user1", "")
			Expect(err).NotTo(HaveOccurred())

			_, err = mgr.Connect(ctx, target, "user1", "client-abc")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("already has an active console session"))

			result1.Conn.Close()
		})

		It("should not corrupt new session when old connection closes after eviction", func() {
			mgr, err := NewManager().
				SetLogger(logger).
				SetSessionTimeout(5*time.Second).
				AddBackend("compute_instance", &contextAwareBackend{}).
				Build()
			Expect(err).NotTo(HaveOccurred())

			ctx := context.Background()
			target := Target{ResourceType: "compute_instance", BackendURI: "wss://hub/ns/test-123/console"}

			result1, err := mgr.Connect(ctx, target, "user1", "client-abc")
			Expect(err).NotTo(HaveOccurred())

			result2, err := mgr.Connect(ctx, target, "user1", "client-abc")
			Expect(err).NotTo(HaveOccurred())

			// Old connection closes after eviction — must not remove new session.
			result1.Conn.Close()
			Expect(mgr.ActiveSessions()).To(Equal(1))

			result2.Conn.Close()
			Expect(mgr.ActiveSessions()).To(Equal(0))
		})

		It("should allow new session after eviction and close", func() {
			ctx := context.Background()
			target := Target{ResourceType: "compute_instance", BackendURI: "wss://hub/ns/test-123/console"}

			result1, err := mgr.Connect(ctx, target, "user1", "client-abc")
			Expect(err).NotTo(HaveOccurred())

			result2, err := mgr.Connect(ctx, target, "user1", "client-abc")
			Expect(err).NotTo(HaveOccurred())

			result1.Conn.Close()
			result2.Conn.Close()
			Expect(mgr.ActiveSessions()).To(Equal(0))

			// Should be able to open a fresh session.
			result3, err := mgr.Connect(ctx, target, "user1", "client-abc")
			Expect(err).NotTo(HaveOccurred())
			result3.Conn.Close()
		})
	})
})
