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
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/coder/websocket"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("KubeVirt Backend Integration", func() {
	var (
		wsServer *mockWSServer
	)

	BeforeEach(func() {
		var err error
		wsServer, err = newMockWSServer()
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(wsServer.Close)
	})

	It("should connect through the Manager and exchange data", func() {
		backend, err := NewKubeVirtBackend().
			SetLogger(logger).
			Build()
		Expect(err).NotTo(HaveOccurred())

		mgr, err := NewManager().
			SetLogger(logger).
			AddBackend("compute_instance", backend).
			Build()
		Expect(err).NotTo(HaveOccurred())

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		target := Target{
			ResourceType: "compute_instance",
			BackendURI:   "ws://" + wsServer.Addr() + "/apis/console.osac.openshift.io/v1alpha1/namespaces/test-ns/computeinstances/test-vm/console",
		}
		result, err := mgr.Connect(ctx, target, "testuser", "")
		Expect(err).NotTo(HaveOccurred())
		Expect(mgr.ActiveSessions()).To(Equal(1))
		defer result.Conn.Close()

		// Read banner.
		buf := make([]byte, 4096)
		n, err := result.Conn.Read(buf)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(buf[:n])).To(ContainSubstring("Welcome"))

		// Send and receive.
		_, err = result.Conn.Write([]byte("ls\n"))
		Expect(err).NotTo(HaveOccurred())

		n, err = result.Conn.Read(buf)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(buf[:n])).To(Equal("echo: ls\n"))

		// Close and verify session removed.
		err = result.Conn.Close()
		Expect(err).NotTo(HaveOccurred())
		Expect(mgr.ActiveSessions()).To(Equal(0))
	})

	It("should handle server-side connection close gracefully", func() {
		// Create a mock WS server that sends banner then closes the WebSocket.
		closeServer, err := newMockWSServerWithHandler(func(ctx context.Context, conn *websocket.Conn) {
			conn.Write(ctx, websocket.MessageBinary, []byte("goodbye\r\n"))
			// Close the WebSocket connection immediately after banner.
			conn.Close(websocket.StatusNormalClosure, "")
		})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(closeServer.Close)

		backend, err := NewKubeVirtBackend().
			SetLogger(logger).
			Build()
		Expect(err).NotTo(HaveOccurred())

		ctx := context.Background()
		conn, err := backend.Connect(ctx, Target{
			BackendURI: "ws://" + closeServer.Addr() + "/apis/console.osac.openshift.io/v1alpha1/namespaces/test-ns/computeinstances/test-vm/console",
		})
		Expect(err).NotTo(HaveOccurred())

		// Read the banner.
		buf := make([]byte, 4096)
		n, err := conn.Read(buf)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(buf[:n])).To(Equal("goodbye\r\n"))

		// Next read should return EOF since server closed the connection.
		_, err = conn.Read(buf)
		Expect(err).To(HaveOccurred())

		conn.Close()
	})

	It("should reject concurrent sessions to the same resource", func() {
		backend, err := NewKubeVirtBackend().
			SetLogger(logger).
			Build()
		Expect(err).NotTo(HaveOccurred())

		mgr, err := NewManager().
			SetLogger(logger).
			AddBackend("compute_instance", backend).
			Build()
		Expect(err).NotTo(HaveOccurred())

		ctx := context.Background()
		target := Target{
			ResourceType: "compute_instance",
			BackendURI:   "ws://" + wsServer.Addr() + "/apis/console.osac.openshift.io/v1alpha1/namespaces/test-ns/computeinstances/test-vm/console",
		}

		result1, err := mgr.Connect(ctx, target, "user1", "")
		Expect(err).NotTo(HaveOccurred())
		defer result1.Conn.Close()

		// Second connection to same resource should fail.
		_, err = mgr.Connect(ctx, target, "user2", "")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("already has an active console session"))

		// After closing first, second should succeed.
		result1.Conn.Close()
		result2, err := mgr.Connect(ctx, target, "user2", "")
		Expect(err).NotTo(HaveOccurred())
		result2.Conn.Close()
	})

	It("should send bearer token in Authorization header to the server", func() {
		receivedAuth := make(chan string, 1)
		authListener, err := newMockWSServerWithAuthCapture(receivedAuth)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(authListener.Close)

		backend, err := NewKubeVirtBackend().
			SetLogger(logger).
			Build()
		Expect(err).NotTo(HaveOccurred())

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		conn, err := backend.Connect(ctx, Target{
			BackendURI:   "ws://" + authListener.Addr() + "/apis/console.osac.openshift.io/v1alpha1/namespaces/test-ns/computeinstances/test-vm/console",
			BackendToken: "test-token-123",
		})
		Expect(err).NotTo(HaveOccurred())
		defer conn.Close()

		buf := make([]byte, 4096)
		n, err := conn.Read(buf)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(buf[:n])).To(Equal("authenticated\r\n"))
		Expect(receivedAuth).To(Receive(Equal("Bearer test-token-123")))
	})
})

// newMockWSServerWithAuthCapture creates a mock WS server that captures the
// Authorization header from the HTTP upgrade request into a channel.
func newMockWSServerWithAuthCapture(authCh chan<- string) (*mockWSServer, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("failed to listen: %w", err)
	}

	m := &mockWSServer{listener: listener}

	mux := http.NewServeMux()
	mux.HandleFunc("/apis/console.osac.openshift.io/v1alpha1/namespaces/", func(w http.ResponseWriter, r *http.Request) {
		authCh <- r.Header.Get("Authorization")
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
			InsecureSkipVerify: true,
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		conn.SetReadLimit(-1)
		conn.Write(r.Context(), websocket.MessageBinary, []byte("authenticated\r\n"))
		conn.Close(websocket.StatusNormalClosure, "")
	})

	m.server = &http.Server{Handler: mux}
	go m.server.Serve(listener)

	return m, nil
}
