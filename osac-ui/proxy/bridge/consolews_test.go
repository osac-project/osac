package bridge

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

// upstreamObservation is what a test upstream server reports back to the test
// goroutine. Values are sent over a channel rather than shared, so the handler
// goroutine and the assertions have explicit synchronization.
type upstreamObservation struct {
	requestURI string
	header     http.Header
	host       string
}

func newObservingUpstream(t *testing.T) (*httptest.Server, <-chan upstreamObservation) {
	t.Helper()

	observations := make(chan upstreamObservation, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		observations <- upstreamObservation{
			requestURI: r.RequestURI,
			header:     r.Header.Clone(),
			host:       r.Host,
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)
	return server, observations
}

func TestNewConsoleWebSocketProxy_rewritesToConsoleConnectPath(t *testing.T) {
	t.Parallel()

	upstream, observations := newObservingUpstream(t)

	// A base path on the target and a different inbound path both make the
	// explicit path override observable: without it the upstream would see
	// "/base/console_sessions/other".
	handler, err := NewConsoleWebSocketProxy(upstream.URL+"/base", nil)
	if err != nil {
		t.Fatalf("NewConsoleWebSocketProxy: %v", err)
	}

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/fulfillment/v1/console_sessions/other?session=s1", nil)
	req.AddCookie(&http.Cookie{Name: "console-ticket", Value: "ticket-value"})
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, rec.Code)
	}
	observed := <-observations

	if want := consoleConnectPath + "?session=s1"; observed.requestURI != want {
		t.Errorf("expected request URI %q, got %q", want, observed.requestURI)
	}
	if observed.header.Get("Cookie") != "console-ticket=ticket-value" {
		t.Errorf("expected console-ticket cookie to be forwarded, got %q", observed.header.Get("Cookie"))
	}
}

func TestNewConsoleWebSocketProxy_stripsInboundForwardingHeaders(t *testing.T) {
	t.Parallel()

	upstream, observations := newObservingUpstream(t)

	handler, err := NewConsoleWebSocketProxy(upstream.URL, nil)
	if err != nil {
		t.Fatalf("NewConsoleWebSocketProxy: %v", err)
	}

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, consoleConnectPath, nil)
	// A Host that differs from the upstream's loopback address makes the
	// outbound Host assertion meaningful.
	req.Host = "attacker.example"
	forwarded := []string{"Forwarded", "X-Forwarded-For", "X-Forwarded-Host", "X-Forwarded-Proto"}
	for _, name := range forwarded {
		req.Header.Set(name, "attacker-controlled")
	}
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, rec.Code)
	}
	observed := <-observations

	for _, name := range forwarded {
		if value := observed.header.Get(name); value != "" {
			t.Errorf("expected inbound %s to be stripped, got %q", name, value)
		}
	}

	upstreamURL, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatalf("parse upstream URL: %v", err)
	}
	if observed.host != upstreamURL.Host {
		t.Errorf("expected outbound Host %q, got %q", upstreamURL.Host, observed.host)
	}
}

func TestNewConsoleWebSocketProxy_upgradesAndEchoesOverTLS(t *testing.T) {
	t.Parallel()

	const (
		// Use the example handshake key and accept value from RFC 6455.
		key     = "dGhlIHNhbXBsZSBub25jZQ==" //gitleaks:allow -- RFC 6455 example key, not a secret.
		accept  = "s3pPLMBiTxaQ9kYGzzhZRbK+xOo="
		payload = "console echo"
	)
	// Generous enough for a loaded CI worker while still failing with a
	// phase-specific I/O error long before `go test -timeout` would fire.
	deadline := time.Now().Add(30 * time.Second)
	ctx, cancel := context.WithDeadline(t.Context(), deadline)
	t.Cleanup(cancel)

	serveEcho := func(w http.ResponseWriter, r *http.Request) error {
		if r.URL.Path != consoleConnectPath {
			return fmt.Errorf("upstream path = %q, want %q", r.URL.Path, consoleConnectPath)
		}
		for name, want := range map[string]string{
			"Connection":            "Upgrade",
			"Upgrade":               "websocket",
			"Sec-WebSocket-Key":     key,
			"Sec-WebSocket-Version": "13",
		} {
			if got := r.Header.Get(name); got != want {
				return fmt.Errorf("upstream %s = %q, want %q", name, got, want)
			}
		}

		conn, rw, err := http.NewResponseController(w).Hijack()
		if err != nil {
			return fmt.Errorf("hijack upstream: %w", err)
		}
		defer conn.Close()
		if err := conn.SetDeadline(deadline); err != nil {
			return fmt.Errorf("set upstream deadline: %w", err)
		}
		if _, err := rw.WriteString("HTTP/1.1 101 Switching Protocols\r\n" +
			"Connection: Upgrade\r\nUpgrade: websocket\r\nSec-WebSocket-Accept: " + accept + "\r\n\r\n"); err != nil {
			return fmt.Errorf("write upstream handshake: %w", err)
		}
		if err := rw.Flush(); err != nil {
			return fmt.Errorf("flush upstream handshake: %w", err)
		}
		if _, err := io.CopyN(conn, rw.Reader, int64(len(payload))); err != nil {
			return fmt.Errorf("echo upstream bytes: %w", err)
		}
		return nil
	}
	upstreamResult := make(chan error, 1)
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamResult <- serveEcho(w, r)
	}))
	t.Cleanup(upstream.Close)

	roots := x509.NewCertPool()
	roots.AddCert(upstream.Certificate())
	handler, err := NewConsoleWebSocketProxy(upstream.URL, &tls.Config{RootCAs: roots})
	if err != nil {
		t.Fatalf("NewConsoleWebSocketProxy: %v", err)
	}
	proxy := httptest.NewServer(handler)
	t.Cleanup(proxy.Close)
	for _, server := range []*httptest.Server{upstream, proxy} {
		ip := server.Listener.Addr().(*net.TCPAddr).IP
		if !ip.IsLoopback() || ip.To4() == nil {
			t.Fatalf("expected an IPv4 loopback listener, got %s", ip)
		}
	}

	addr := proxy.Listener.Addr().String()
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp4", addr)
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if err := conn.SetDeadline(deadline); err != nil {
		t.Fatalf("set client deadline: %v", err)
	}
	if _, err := fmt.Fprintf(conn, "GET /before-rewrite HTTP/1.1\r\nHost: %s\r\n"+
		"Connection: Upgrade\r\nUpgrade: websocket\r\nSec-WebSocket-Version: 13\r\n"+
		"Sec-WebSocket-Key: %s\r\n\r\n", addr, key); err != nil {
		t.Fatalf("write client handshake: %v", err)
	}

	reader := bufio.NewReader(conn)
	resp, err := http.ReadResponse(reader, nil)
	if err != nil {
		t.Fatalf("read upgrade response: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusSwitchingProtocols {
		select {
		case err := <-upstreamResult:
			t.Fatalf("expected status 101, got %s (upstream result: %v)", resp.Status, err)
		default:
			t.Fatalf("expected status 101, got %s", resp.Status)
		}
	}
	if resp.Header.Get("Connection") != "Upgrade" || resp.Header.Get("Upgrade") != "websocket" ||
		resp.Header.Get("Sec-WebSocket-Accept") != accept {
		t.Fatalf("invalid upgrade response headers: %v", resp.Header)
	}

	// Verify the tunnel relays bytes; WebSocket framing is the endpoints' job.
	if _, err := io.WriteString(conn, payload); err != nil {
		t.Fatalf("write tunnel payload: %v", err)
	}
	echo := make([]byte, len(payload))
	if _, err := io.ReadFull(reader, echo); err != nil {
		t.Fatalf("read tunnel echo: %v", err)
	}
	if string(echo) != payload {
		t.Fatalf("tunnel echo = %q, want %q", echo, payload)
	}
	select {
	case err := <-upstreamResult:
		if err != nil {
			t.Fatalf("upstream: %v", err)
		}
	case <-ctx.Done():
		t.Fatalf("waiting for upstream: %v", ctx.Err())
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
