package bridge

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestNewConsoleWebSocketProxy_rewritesToConsoleConnectPath(t *testing.T) {
	t.Parallel()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != consoleConnectPath {
			t.Fatalf("expected path %q, got %q", consoleConnectPath, r.URL.Path)
		}
		if r.Header.Get("Cookie") != "console-ticket=ticket-value" {
			t.Fatalf("expected console-ticket cookie to be forwarded, got %q", r.Header.Get("Cookie"))
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(upstream.Close)

	handler, err := NewConsoleWebSocketProxy(upstream.URL, nil)
	if err != nil {
		t.Fatalf("NewConsoleWebSocketProxy: %v", err)
	}

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/fulfillment/v1/console_sessions/connect", nil)
	req.AddCookie(&http.Cookie{Name: "console-ticket", Value: "ticket-value"})
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, rec.Code)
	}
}

func TestNewConsoleWebSocketProxy_rejectsInvalidURL(t *testing.T) {
	t.Parallel()

	_, err := NewConsoleWebSocketProxy("://bad", nil)
	if err == nil {
		t.Fatal("expected error for invalid URL")
	}
}

func TestNewConsoleWebSocketProxy_requiresHost(t *testing.T) {
	t.Parallel()

	_, err := NewConsoleWebSocketProxy("https:///missing-host", nil)
	if err == nil {
		t.Fatal("expected error for missing host")
	}

	_, err = NewConsoleWebSocketProxy("ftp://example.com", nil)
	if err == nil {
		t.Fatal("expected error for unsupported scheme")
	}
}

func TestConsoleConnectPath_isAbsolute(t *testing.T) {
	t.Parallel()

	parsed, err := url.Parse(consoleConnectPath)
	if err != nil {
		t.Fatalf("parse path: %v", err)
	}
	if parsed.Path != consoleConnectPath {
		t.Fatalf("unexpected parsed path %q", parsed.Path)
	}
}
