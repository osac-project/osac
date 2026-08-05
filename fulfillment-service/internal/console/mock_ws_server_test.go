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

	"github.com/coder/websocket"
)

// mockWSServer simulates the console subresource endpoint.
// It accepts WebSocket connections and echoes data back with a configurable prefix.
type mockWSServer struct {
	listener   net.Listener
	server     *http.Server
	echoPrefix string
	banner     string
}

// newMockWSServer creates and starts a mock WebSocket server.
// It listens on a random local port.
func newMockWSServer() (*mockWSServer, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("failed to listen: %w", err)
	}

	m := &mockWSServer{
		listener:   listener,
		echoPrefix: "echo: ",
		banner:     "Welcome to mock console\r\n",
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/apis/console.osac.openshift.io/v1alpha1/namespaces/", m.handleConsole)

	m.server = &http.Server{Handler: mux}
	go m.server.Serve(listener)

	return m, nil
}

// mockWSHandler is a function that handles a WebSocket connection in mock servers.
type mockWSHandler func(ctx context.Context, conn *websocket.Conn)

// newMockWSServerWithHandler creates a mock WebSocket server with a custom handler.
func newMockWSServerWithHandler(handler mockWSHandler) (*mockWSServer, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("failed to listen: %w", err)
	}

	m := &mockWSServer{listener: listener}

	mux := http.NewServeMux()
	mux.HandleFunc("/apis/console.osac.openshift.io/v1alpha1/namespaces/", func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
			InsecureSkipVerify: true,
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		conn.SetReadLimit(-1)
		handler(r.Context(), conn)
	})

	m.server = &http.Server{Handler: mux}
	go m.server.Serve(listener)

	return m, nil
}

// Addr returns the server's listener address.
func (m *mockWSServer) Addr() string {
	return m.listener.Addr().String()
}

// Close shuts down the mock server.
func (m *mockWSServer) Close() error {
	return m.server.Close()
}

// handleConsole handles WebSocket connections to the console subresource.
// It sends a banner, then echoes all received data back with a prefix.
func (m *mockWSServer) handleConsole(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer conn.CloseNow()
	conn.SetReadLimit(-1)

	ctx := r.Context()

	// Send banner.
	if m.banner != "" {
		if err := conn.Write(ctx, websocket.MessageBinary, []byte(m.banner)); err != nil {
			return
		}
	}

	// Echo loop.
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return
		}
		if len(data) > 0 {
			response := m.echoPrefix + string(data)
			if err := conn.Write(ctx, websocket.MessageBinary, []byte(response)); err != nil {
				return
			}
		}
	}
}
