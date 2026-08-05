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
	"net/http"
	"net/http/httptest"
	"time"

	"github.com/coder/websocket"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/osac-project/fulfillment-service/internal/console"
)

// rejectingOpener is a ticketOpener stub that always returns an error.
// Used by origin-enforcement tests so the handler reaches a deterministic 401
// instead of panicking on a nil TicketOpener.
type rejectingOpener struct{}

func (r *rejectingOpener) Open(_ context.Context, _ string) (*console.Ticket, error) {
	return nil, errors.New("stub: rejected")
}

var _ = Describe("extractTicket", func() {
	It("should extract from Authorization Bearer header", func() {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Authorization", "Bearer my-jwt-token")

		ticket, err := extractTicket(req)
		Expect(err).NotTo(HaveOccurred())
		Expect(ticket).To(Equal("my-jwt-token"))
	})

	It("should reject non-Bearer Authorization header", func() {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Authorization", "Basic dXNlcjpwYXNz")

		_, err := extractTicket(req)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("Bearer"))
	})

	It("should extract from console-ticket cookie", func() {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.AddCookie(&http.Cookie{Name: "console-ticket", Value: "cookie-jwt"})

		ticket, err := extractTicket(req)
		Expect(err).NotTo(HaveOccurred())
		Expect(ticket).To(Equal("cookie-jwt"))
	})

	It("should prefer Authorization header over cookie", func() {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Authorization", "Bearer header-jwt")
		req.AddCookie(&http.Cookie{Name: "console-ticket", Value: "cookie-jwt"})

		ticket, err := extractTicket(req)
		Expect(err).NotTo(HaveOccurred())
		Expect(ticket).To(Equal("header-jwt"))
	})

	It("should return error when neither header nor cookie present", func() {
		req := httptest.NewRequest(http.MethodGet, "/", nil)

		_, err := extractTicket(req)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("missing ticket"))
	})

	It("should ignore empty cookie value", func() {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.AddCookie(&http.Cookie{Name: "console-ticket", Value: ""})

		_, err := extractTicket(req)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("missing ticket"))
	})
})

// succeedingOpener is a ticketOpener stub that returns a fixed ticket.
type succeedingOpener struct {
	ticket *console.Ticket
}

func (s *succeedingOpener) Open(_ context.Context, _ string) (*console.Ticket, error) {
	return s.ticket, nil
}

var _ = Describe("ServeHTTP ConnectBackend error handling", func() {
	It("should return 409 when backend reports an active session", func() {
		backend := &mockBackendForServer{conn: newMockConn("")}
		manager, err := console.NewManager().
			SetLogger(logger).
			AddBackend("compute_instance", backend).
			Build()
		Expect(err).NotTo(HaveOccurred())

		ticket := &console.Ticket{
			User:        "user-a",
			ClientID:    "client-1",
			ConsoleType: "vnc",
			TargetURI:   "wss://hub:6443/test/vnc",
			TargetToken: "tok",
		}

		// Occupy the session so the next connect gets ErrSessionExists.
		target := console.Target{
			ResourceType: console.ResourceTypeComputeInstance,
			BackendURI:   ticket.TargetURI,
			BackendToken: ticket.TargetToken,
		}
		result, err := manager.Connect(context.Background(), target, "user-a", "client-1")
		Expect(err).NotTo(HaveOccurred())
		defer result.Conn.Close()

		core, err := NewConsoleProxyCore().
			SetLogger(logger).
			SetOpener(console.NewTicketOpener(nil)).
			SetManager(manager).
			Build()
		Expect(err).NotTo(HaveOccurred())
		core.opener = &succeedingOpener{ticket: ticket}

		handler := &ConsoleProxyWSHandler{
			core:           core,
			allowedOrigins: []string{"*"},
		}

		// Second connect with different clientID triggers ErrSessionExists.
		secondTicket := &console.Ticket{
			User:        "user-b",
			ClientID:    "client-2",
			ConsoleType: "vnc",
			TargetURI:   "wss://hub:6443/test/vnc",
			TargetToken: "tok",
		}
		core.opener = &succeedingOpener{ticket: secondTicket}

		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Authorization", "Bearer some-ticket")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		Expect(w.Code).To(Equal(http.StatusConflict))
		Expect(w.Body.String()).To(ContainSubstring("console session already active"))
	})

	It("should return 502 for generic backend errors", func() {
		backend := &mockBackendForServer{
			connErr: errors.New("dial backend failed"),
		}
		manager, err := console.NewManager().
			SetLogger(logger).
			AddBackend("compute_instance", backend).
			Build()
		Expect(err).NotTo(HaveOccurred())

		ticket := &console.Ticket{
			User:        "user-a",
			ClientID:    "client-1",
			ConsoleType: "vnc",
			TargetURI:   "wss://hub:6443/test/vnc",
			TargetToken: "tok",
		}

		core, err := NewConsoleProxyCore().
			SetLogger(logger).
			SetOpener(console.NewTicketOpener(nil)).
			SetManager(manager).
			Build()
		Expect(err).NotTo(HaveOccurred())
		core.opener = &succeedingOpener{ticket: ticket}

		handler := &ConsoleProxyWSHandler{
			core:           core,
			allowedOrigins: []string{"*"},
		}

		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Authorization", "Bearer some-ticket")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		Expect(w.Code).To(Equal(http.StatusBadGateway))
		Expect(w.Body.String()).To(ContainSubstring("failed to connect to console backend"))
	})
})

var _ = Describe("ServeHTTP Origin enforcement", func() {
	It("should return 403 for cookie auth without Origin header", func() {
		handler := &ConsoleProxyWSHandler{
			core:           &ConsoleProxyCore{logger: logger, opener: &rejectingOpener{}},
			allowedOrigins: []string{"https://good.com"},
		}
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.AddCookie(&http.Cookie{Name: "console-ticket", Value: "some-jwt"})
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		Expect(w.Code).To(Equal(http.StatusForbidden))
		Expect(w.Body.String()).To(ContainSubstring("origin not allowed"))
	})

	It("should pass cookie auth with Origin to ticket verification", func() {
		handler := &ConsoleProxyWSHandler{
			core:           &ConsoleProxyCore{logger: logger, opener: &rejectingOpener{}},
			allowedOrigins: []string{"https://good.com"},
		}
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.AddCookie(&http.Cookie{Name: "console-ticket", Value: "some-jwt"})
		req.Header.Set("Origin", "https://good.com")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		// Origin is present so the pre-check passes; the stub opener
		// rejects the token, so we expect 401 (not a 403 origin error).
		Expect(w.Code).To(Equal(http.StatusUnauthorized))
	})

	It("should reject cookie auth with disallowed Origin during upgrade", func() {
		// This test exercises the library's OriginPatterns rejection inside
		// websocket.Accept. It requires a real HTTP server (not httptest.NewRecorder)
		// because Accept needs a hijackable connection.
		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Simulate the handler's origin pre-check: Origin is present, so
			// the empty-Origin guard passes. Accept should reject the mismatch.
			// Accept writes the HTTP error response itself, so we just return.
			_, _ = websocket.Accept(w, r, &websocket.AcceptOptions{
				OriginPatterns: []string{"https://good.com"},
			})
		})
		srv := httptest.NewServer(handler)
		defer srv.Close()

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _, err := websocket.Dial(ctx, "ws"+srv.URL[len("http"):], &websocket.DialOptions{
			HTTPHeader: http.Header{"Origin": []string{"https://evil.com"}},
		})
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("403"))
	})

	It("should skip empty-Origin check for Bearer auth", func() {
		handler := &ConsoleProxyWSHandler{
			core:           &ConsoleProxyCore{logger: logger, opener: &rejectingOpener{}},
			allowedOrigins: []string{"https://good.com"},
		}
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Authorization", "Bearer some-token")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		// Bearer auth with no Origin -- the pre-check only fires for cookie
		// auth, so this proceeds to ticket verification. The stub opener
		// rejects the token, yielding 401 (not a 403 origin error).
		Expect(w.Code).To(Equal(http.StatusUnauthorized))
	})
})
