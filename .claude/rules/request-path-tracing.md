# Request Path Tracing

When designing or implementing features triggered by external requests (API endpoints, CLI commands), trace the request path from the user-facing entry point to the handler before writing code. List each layer the request passes through (REST gateway, gRPC interceptors, middleware, server). Any layer that transforms, filters, or routes the request is an affected component - even if its code doesn't change.

A layer that silently drops or ignores the feature's input is a bug that unit tests at the handler level won't catch.

## OSAC Example

The fulfillment-service REST gateway uses grpc-gateway with `DefaultHeaderMatcher`, which only forwards permanent HTTP headers and `Grpc-Metadata-*` prefixed headers to gRPC metadata. Custom HTTP headers are silently dropped. When a feature relies on a custom header reaching the gRPC server, the gateway's header matcher configuration must be checked and updated.

Request path for REST API calls:

```text
HTTP client → REST gateway (grpc-gateway mux) → gRPC interceptors → server handler
```

Check `fulfillment-service/internal/cmd/service/start/restgateway/start_rest_gateway_cmd.go` for gateway configuration when adding new headers.
