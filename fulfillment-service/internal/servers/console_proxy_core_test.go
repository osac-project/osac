/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package servers

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/osac-project/fulfillment-service/internal/console"
)

var _ = Describe("ConsoleProxyCore", func() {
	Describe("Relay", func() {
		var core *ConsoleProxyCore

		BeforeEach(func() {
			// Relay tests only use the logger, but the builder requires all fields.
			// The opener wraps nil (never called) and the manager uses a dummy backend.
			dummyBackend := &mockBackendForServer{conn: newMockConn("")}
			manager, err := console.NewManager().
				SetLogger(logger).
				AddBackend("compute_instance", dummyBackend).
				Build()
			Expect(err).NotTo(HaveOccurred())
			core, err = NewConsoleProxyCore().
				SetLogger(logger).
				SetOpener(console.NewTicketOpener(nil)).
				SetManager(manager).
				Build()
			Expect(err).NotTo(HaveOccurred())
		})

		It("should copy data bidirectionally", func() {
			clientSide, clientRemote := net.Pipe()
			backendSide, backendRemote := net.Pipe()

			done := make(chan error, 1)
			go func() {
				done <- core.Relay(context.Background(), clientSide, backendSide)
			}()

			// Write from "client app" side, read from "backend app" side.
			_, err := clientRemote.Write([]byte("hello"))
			Expect(err).NotTo(HaveOccurred())

			buf := make([]byte, 64)
			backendRemote.SetReadDeadline(time.Now().Add(2 * time.Second))
			n, err := backendRemote.Read(buf)
			Expect(err).NotTo(HaveOccurred())
			Expect(string(buf[:n])).To(Equal("hello"))

			// Write from "backend app" side, read from "client app" side.
			_, err = backendRemote.Write([]byte("world"))
			Expect(err).NotTo(HaveOccurred())

			clientRemote.SetReadDeadline(time.Now().Add(2 * time.Second))
			n, err = clientRemote.Read(buf)
			Expect(err).NotTo(HaveOccurred())
			Expect(string(buf[:n])).To(Equal("world"))

			// Close one side to terminate the relay.
			clientRemote.Close()
			backendRemote.Close()

			Eventually(done).WithTimeout(2 * time.Second).Should(Receive())
		})

		It("should terminate when context is cancelled", func() {
			clientSide, clientRemote := net.Pipe()
			backendSide, backendRemote := net.Pipe()
			defer clientRemote.Close()
			defer backendRemote.Close()

			ctx, cancel := context.WithCancel(context.Background())

			done := make(chan error, 1)
			go func() {
				done <- core.Relay(ctx, clientSide, backendSide)
			}()

			// Cancel context to trigger shutdown.
			cancel()

			Eventually(done).WithTimeout(2 * time.Second).Should(Receive())
		})

		It("should terminate when backend closes and client Read blocks", func() {
			// Simulates the gRPC relay scenario: client.Close() signals a done
			// channel to unblock Read(), but doesn't close an underlying connection.
			// Without this, the relay hangs indefinitely.
			client := &failingRWC{
				Reader: &blockingReader{ch: make(chan struct{})},
				Writer: io.Discard,
			}
			backendSide, backendRemote := net.Pipe()

			done := make(chan error, 1)
			go func() {
				done <- core.Relay(context.Background(), client, backendSide)
			}()

			// Close the backend to trigger the first copy to exit.
			backendRemote.Close()

			Eventually(done).WithTimeout(2 * time.Second).Should(Receive())
		})

		It("should suppress errors when context is cancelled", func() {
			clientSide, clientRemote := net.Pipe()
			backendSide, backendRemote := net.Pipe()
			defer clientRemote.Close()
			defer backendRemote.Close()

			ctx, cancel := context.WithCancel(context.Background())

			done := make(chan error, 1)
			go func() {
				done <- core.Relay(ctx, clientSide, backendSide)
			}()

			cancel()

			var relayErr error
			Eventually(done).WithTimeout(2 * time.Second).Should(Receive(&relayErr))
			Expect(relayErr).NotTo(HaveOccurred())
		})

		It("should propagate backend write errors", func() {
			// Client side: readable pipe that the relay reads from, backed by
			// a writer we control.
			clientPR, clientPW := io.Pipe()
			client := &failingRWC{
				Reader: clientPR,
				Writer: io.Discard,
			}
			backend := &failingRWC{
				Reader: &blockingReader{ch: make(chan struct{})},
				Writer: &failWriter{err: errors.New("backend write failed")},
			}

			done := make(chan error, 1)
			go func() {
				done <- core.Relay(context.Background(), client, backend)
			}()

			// Feed data into the client's read side. The relay reads from
			// the client and tries to write to the backend, which fails.
			_, _ = clientPW.Write([]byte("data"))

			var relayErr error
			Eventually(done).WithTimeout(2 * time.Second).Should(Receive(&relayErr))
			Expect(relayErr).To(HaveOccurred())
		})
	})

	Describe("ConnectBackend", func() {
		It("should pass ticket target to backend and open connection", func() {
			mockBackend := &mockBackendForServer{
				conn: newMockConn("backend-data"),
			}
			manager, err := console.NewManager().
				SetLogger(logger).
				AddBackend("compute_instance", mockBackend).
				Build()
			Expect(err).NotTo(HaveOccurred())

			core, err := NewConsoleProxyCore().
				SetLogger(logger).
				SetOpener(console.NewTicketOpener(nil)).
				SetManager(manager).
				Build()
			Expect(err).NotTo(HaveOccurred())

			ticket := &console.Ticket{
				User:        "test-user",
				ClientID:    "client-1",
				TargetURI:   "wss://hub:6443/apis/console.osac.openshift.io/v1alpha1/namespaces/ns-1/computeinstances/ci-test/vnc",
				TargetToken: "test-token",
			}

			conn, sessionCtx, err := core.ConnectBackend(context.Background(), ticket)
			Expect(err).NotTo(HaveOccurred())
			Expect(conn).NotTo(BeNil())
			Expect(sessionCtx).NotTo(BeNil())
			Expect(sessionCtx.Err()).To(Succeed())
			conn.Close()

			target := mockBackend.getLastTarget()
			Expect(target.BackendURI).To(Equal(ticket.TargetURI))
			Expect(target.BackendToken).To(Equal("test-token"))
		})

		It("should propagate backend connection errors", func() {
			mockBackend := &mockBackendForServer{
				connErr: errors.New("dial backend failed"),
			}
			manager, err := console.NewManager().
				SetLogger(logger).
				AddBackend("compute_instance", mockBackend).
				Build()
			Expect(err).NotTo(HaveOccurred())

			core, err := NewConsoleProxyCore().
				SetLogger(logger).
				SetOpener(console.NewTicketOpener(nil)).
				SetManager(manager).
				Build()
			Expect(err).NotTo(HaveOccurred())

			ticket := &console.Ticket{
				User:        "test-user",
				ClientID:    "client-1",
				TargetURI:   "wss://hub:6443/test/vnc",
				TargetToken: "test-token",
			}

			conn, sessionCtx, err := core.ConnectBackend(context.Background(), ticket)
			Expect(err).To(HaveOccurred())
			Expect(conn).To(BeNil())
			Expect(sessionCtx).To(BeNil())
			Expect(err.Error()).To(ContainSubstring("dial backend failed"))
		})
	})

	Describe("ExtractBearerToken", func() {
		It("should extract a valid token", func() {
			token, err := ExtractBearerToken("Bearer validtoken")
			Expect(err).NotTo(HaveOccurred())
			Expect(token).To(Equal("validtoken"))
		})

		It("should be case-insensitive on the scheme", func() {
			token, err := ExtractBearerToken("bearer validtoken")
			Expect(err).NotTo(HaveOccurred())
			Expect(token).To(Equal("validtoken"))
		})

		It("should reject an empty token after the prefix", func() {
			_, err := ExtractBearerToken("Bearer ")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("Bearer"))
		})

		It("should reject a whitespace-only token after the prefix", func() {
			_, err := ExtractBearerToken("Bearer    ")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("Bearer"))
		})

		It("should reject a non-Bearer scheme", func() {
			_, err := ExtractBearerToken("Basic xyz")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("Bearer"))
		})

		It("should reject an empty string", func() {
			_, err := ExtractBearerToken("")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("Bearer"))
		})
	})
})

// failingRWC is an io.ReadWriteCloser with injectable reader and writer.
type failingRWC struct {
	io.Reader
	io.Writer
	closeOnce sync.Once
}

func (f *failingRWC) Close() error {
	f.closeOnce.Do(func() {
		if c, ok := f.Reader.(io.Closer); ok {
			c.Close()
		}
	})
	return nil
}

// failWriter always returns an error on Write.
type failWriter struct {
	err error
}

func (w *failWriter) Write(p []byte) (int, error) {
	return 0, w.err
}

// blockingReader blocks on Read until it is closed.
type blockingReader struct {
	ch   chan struct{}
	once sync.Once
}

func (r *blockingReader) Read(p []byte) (int, error) {
	<-r.ch
	return 0, io.EOF
}

func (r *blockingReader) Close() error {
	r.once.Do(func() {
		close(r.ch)
	})
	return nil
}

// Verify interface compliance for test helpers.
var _ io.ReadWriteCloser = (*failingRWC)(nil)
