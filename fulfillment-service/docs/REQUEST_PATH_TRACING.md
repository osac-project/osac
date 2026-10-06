# Request path tracing

Before implementing a feature driven by an API or CLI request, trace its input
from the user-facing entry point to the handler. Identify each layer that
routes, filters, or transforms it, including the REST gateway, gRPC
interceptors, middleware, and server. A layer remains part of the affected path
even when its code does not need to change.

Check that every layer preserves the new input. A handler unit test cannot
detect input silently dropped before the handler; test the relevant boundary
when introducing headers, metadata, or other routed input.

## REST gateway example

```text
HTTP client → REST gateway → gRPC interceptors → server handler
```

The gateway mux in
[start_rest_gateway_cmd.go](../internal/cmd/service/start/restgateway/start_rest_gateway_cmd.go)
maps the dry-run HTTP header to gRPC metadata explicitly, then falls back to
grpc-gateway's `DefaultHeaderMatcher`. The default matcher forwards permanent
HTTP headers and `Grpc-Metadata-*` headers; arbitrary custom headers are not
forwarded by default. Check this matcher and test REST-to-gRPC propagation when
a feature relies on a new header reaching the server.
