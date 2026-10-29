package bridge

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"google.golang.org/protobuf/reflect/protoregistry"
)

// TestGlobalTypes_resolvesWellKnownWrapperTypes guards against a proto marshaling failure
// seen when a response contains a google.protobuf.Any packing a well-known type — e.g.
// ClusterTemplateParameterDefinition.default, documented (cluster_template_type.proto) to hold
// a packed StringValue, BoolValue, ..., or Value (for "any JSON value"). vanguard's
// reflection-derived per-service type resolver falls back to protoregistry.GlobalTypes
// specifically to resolve Any-packed well-known types, but that registry is empty unless
// something in this binary imports the corresponding package (wrapperspb, structpb) — no
// .proto file "imports" google/protobuf/{wrappers,struct}.proto just by declaring an Any
// field, since Any is opaque, so gRPC reflection never surfaces it. Without those imports in
// connectjson.go, lookups like this fail with "unable to resolve
// type.googleapis.com/google.protobuf.StringValue: not found" (or .Value, .BoolValue, ...).
func TestReflectionTimeoutCoversSlowLinks(t *testing.T) {
	if reflectionTimeout < 2*time.Minute {
		t.Fatalf("reflectionTimeout = %s, want at least 2m0s", reflectionTimeout)
	}
}

func TestGlobalTypes_resolvesWellKnownWrapperTypes(t *testing.T) {
	t.Parallel()

	for _, typeURL := range []string{
		"type.googleapis.com/google.protobuf.StringValue",
		"type.googleapis.com/google.protobuf.BoolValue",
		"type.googleapis.com/google.protobuf.Int32Value",
		"type.googleapis.com/google.protobuf.Int64Value",
		"type.googleapis.com/google.protobuf.DoubleValue",
		"type.googleapis.com/google.protobuf.Value",
		"type.googleapis.com/google.protobuf.Struct",
		"type.googleapis.com/google.protobuf.ListValue",
	} {
		if _, err := protoregistry.GlobalTypes.FindMessageByURL(typeURL); err != nil {
			t.Errorf("expected %s to be resolvable via protoregistry.GlobalTypes, got: %v", typeURL, err)
		}
	}
}

// TestNewGRPCTransport_doesNotMutateSharedTLSConfig guards against OSAC-3081: main.go
// passes one *tls.Config to multiple independent transports (this one, the plain
// FulfillmentHTTPClient transport, and NewConsoleWebSocketProxy's transport).
// ForceAttemptHTTP2 makes net/http mutate TLSClientConfig.NextProtos in place on first
// use — if newGRPCTransport didn't clone, that mutation would leak into the other
// transports sharing the same pointer, making them offer ALPN "h2" while still
// speaking HTTP/1.1 and get their connections reset by an ALPN-aware upstream.
func TestNewGRPCTransport_doesNotMutateSharedTLSConfig(t *testing.T) {
	t.Parallel()

	shared := &tls.Config{MinVersion: tls.VersionTLS13}
	transport := newGRPCTransport(shared)

	httpTransport, ok := transport.(*http.Transport)
	if !ok {
		t.Fatalf("expected *http.Transport, got %T", transport)
	}
	if httpTransport.TLSClientConfig == shared {
		t.Fatal("expected newGRPCTransport to clone tlsConfig, not share the pointer")
	}

	// net/http lazily runs its ForceAttemptHTTP2 configuration (which mutates
	// TLSClientConfig.NextProtos) on the transport's first RoundTrip call, regardless
	// of whether the dial itself succeeds — so a request to an address nothing listens
	// on is enough to trigger the side effect under test.
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "https://127.0.0.1:0/", nil)
	resp, err := httpTransport.RoundTrip(req) //nolint:bodyclose // RoundTrip is expected to fail before returning a body
	if err == nil {
		t.Fatal("expected RoundTrip to fail dialing a port nothing listens on")
	}
	if resp != nil {
		t.Fatal("expected no response on dial failure")
	}

	if shared.NextProtos != nil {
		t.Fatalf("expected the original shared *tls.Config to be untouched, got NextProtos=%v", shared.NextProtos)
	}
}
