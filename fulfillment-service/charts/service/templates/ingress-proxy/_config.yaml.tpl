#
# Copyright (c) 2025 Red Hat Inc.
#
# Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with
# the License. You may obtain a copy of the License at
#
#   http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
# "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
# specific language governing permissions and limitations under the License.
#

{{ define "ingress-proxy.config.yaml" }}

admin:
  address:
    socket_address:
      address: 127.0.0.1
      port_value: 9901

static_resources:

  listeners:

  # This listener exposes Prometheus metrics and health check endpoints. Metrics requests are proxied to the admin
  # interface, which is bound to localhost for security reasons. Health check requests are handled directly by the
  # health check filter.
  - name: metrics
    address:
      socket_address:
        address: 0.0.0.0
        port_value: 8002
    filter_chains:
    - filters:
      - name: envoy.filters.network.http_connection_manager
        typed_config:
          "@type": type.googleapis.com/envoy.extensions.filters.network.http_connection_manager.v3.HttpConnectionManager
          stat_prefix: metrics
          route_config:
            name: metrics
            virtual_hosts:
            - name: metrics
              domains:
              - "*"
              routes:
              - name: metrics
                match:
                  prefix: /metrics
                route:
                  cluster: admin
                  prefix_rewrite: /stats/prometheus
              - name: healthz
                match:
                  prefix: /healthz
                route:
                  cluster: admin
          http_filters:
          - name: envoy.filters.http.health_check
            typed_config:
              "@type": type.googleapis.com/envoy.extensions.filters.http.health_check.v3.HealthCheck
              pass_through_mode: false
              headers:
              - name: ":path"
                string_match:
                  exact: /healthz
          - name: envoy.filters.http.router
            typed_config:
              "@type": type.googleapis.com/envoy.extensions.filters.http.router.v3.Router

   # This listener receives external traffic for the public API, so it needs to be secured with CORS and TLS.
   # Authentication and authorization are handled by the service itself.
  - name: external-api
    address:
      socket_address:
        address: 0.0.0.0
        port_value: 8000
    filter_chains:
    - filters:
      - name: envoy.filters.network.http_connection_manager
        typed_config:
          "@type": type.googleapis.com/envoy.extensions.filters.network.http_connection_manager.v3.HttpConnectionManager
          access_log:
          - name: envoy.access_loggers.file
            typed_config:
              "@type": type.googleapis.com/envoy.extensions.access_loggers.file.v3.FileAccessLog
              path: /dev/stdout
          stat_prefix: external-api
          http2_protocol_options:
            connection_keepalive:
              interval: 15s
              timeout: 10s
          upgrade_configs:
          - upgrade_type: websocket
          route_config:
            name: backend
            virtual_hosts:
            - name: all
              domains:
              - "*"
              typed_per_filter_config:
                envoy.filters.http.cors:
                  "@type": type.googleapis.com/envoy.extensions.filters.http.cors.v3.CorsPolicy
                  allow_origin_string_match:
                  - safe_regex:
                      regex: ".*"
                  allow_methods: "GET,POST,PUT,PATCH,DELETE,OPTIONS"
                  allow_headers: "Authorization,Content-Type,X-User-Agent,X-Grpc-Web, Accept"
                  expose_headers: "Grpc-Status,Grpc-Message"
                  allow_credentials: true
                  max_age: "86400"
              routes:

              # This route is for the WebSocket console proxy. Console sessions are long-lived
              # and require HTTP/1.1 for the WebSocket upgrade. This route must be matched
              # before the rest-gateway route which catches all /api/... paths. The ticket
              # travels in the Authorization header or console-ticket cookie.
              - name: console-ws
                match:
                  path: /api/fulfillment/v1/console_sessions/connect
                route:
                  cluster: console-proxy-ws
                  timeout: 0s
                  idle_timeout: 1800s

              # JWKS endpoint for token verification. Public, unauthenticated.
              - name: jwks
                match:
                  path: /.well-known/jwks.json
                route:
                  cluster: rest-gateway
                  timeout: 10s

              # This route is for the REST gateway. The public API endpoints use path prefixes
              # like /api/fulfillment/ and /api/events/.
              - name: rest-gateway
                match:
                  safe_regex:
                    regex: /api/(fulfillment|events)(/.*)?
                route:
                  cluster: rest-gateway
                  timeout: 300s

              # This route is for the gRPC streaming requests used to watch events. Those streams can last very
              # long, so we don't want to set a timeout, as that would cause the connection to be closed and
              # events to be potentially lost.
              - name: events
                match:
                  safe_regex:
                    regex: /osac\.public\..*/Watch
                route:
                  cluster: grpc-server
                  timeout: 0s
                  idle_timeout: 0s

              # This route is for the ConsoleProxy.Connect bidirectional stream. Like Watch
              # streams, console sessions are long-lived and must not be subject to the
              # default request timeout. The backend enforces a 30-minute session timeout
              # via context deadline (see manager.go).
              - name: console-proxy-grpc
                match:
                  safe_regex:
                    regex: /osac\.public\..*ConsoleProxy/Connect
                route:
                  cluster: console-proxy-grpc
                  timeout: 0s
                  idle_timeout: 1800s

              # This route is for public API and gRPC reflection unary requests.
              - name: grpc-server
                match:
                  safe_regex:
                    regex: /(osac\.public\.|grpc\.(reflection|health)\.).*
                route:
                  cluster: grpc-server
                  timeout: 300s

          http_filters:
          - name: envoy.filters.http.cors
            typed_config:
              "@type": type.googleapis.com/envoy.extensions.filters.http.cors.v3.Cors
          - name: envoy.filters.http.router
            typed_config:
              "@type": type.googleapis.com/envoy.extensions.filters.http.router.v3.Router

      transport_socket:
        name: envoy.transport_sockets.tls
        typed_config:
          "@type": type.googleapis.com/envoy.extensions.transport_sockets.tls.v3.DownstreamTlsContext
          common_tls_context:
            alpn_protocols:
            - http1.1
            - h2
            tls_certificates:
            - certificate_chain:
                filename: /etc/envoy/tls/tls.crt
              private_key:
                filename: /etc/envoy/tls/tls.key

   # This listener receives internal traffic for both the public and private APIs, so it needs to be secured with
   # CORS and TLS. Authentication and authorization are handled by the service itself.
  - name: internal-api
    address:
      socket_address:
        address: 0.0.0.0
        port_value: 8001
    filter_chains:
    - filters:
      - name: envoy.filters.network.http_connection_manager
        typed_config:
          "@type": type.googleapis.com/envoy.extensions.filters.network.http_connection_manager.v3.HttpConnectionManager
          access_log:
          - name: envoy.access_loggers.file
            typed_config:
              "@type": type.googleapis.com/envoy.extensions.access_loggers.file.v3.FileAccessLog
              path: /dev/stdout
          stat_prefix: internal-api
          http2_protocol_options:
            connection_keepalive:
              interval: 15s
              timeout: 10s
          route_config:
            name: backend
            virtual_hosts:
            - name: all
              domains:
              - "*"
              typed_per_filter_config:
                envoy.filters.http.cors:
                  "@type": type.googleapis.com/envoy.extensions.filters.http.cors.v3.CorsPolicy
                  allow_origin_string_match:
                  - safe_regex:
                      regex: ".*"
                  allow_methods: "GET,POST,PUT,PATCH,DELETE,OPTIONS"
                  allow_headers: "Authorization,Content-Type,X-User-Agent,X-Grpc-Web, Accept"
                  expose_headers: "Grpc-Status,Grpc-Message"
                  allow_credentials: true
                  max_age: "86400"
              routes:

              # JWKS endpoint for token verification. Public, unauthenticated.
              - name: jwks
                match:
                  path: /.well-known/jwks.json
                route:
                  cluster: rest-gateway
                  timeout: 10s

              # This route is for the REST gateway.
              - name: rest-gateway
                match:
                  prefix: /api
                route:
                  cluster: rest-gateway
                  timeout: 300s

              # This route is for the gRPC streaming requests used to watch events. Those streams can last very
              # long, so we don't want to set a timeout, as that would cause the connection to be closed and
              # events to be potentially lost.
              - name: events
                match:
                  safe_regex:
                    regex: ^.*/Watch$
                route:
                  cluster: grpc-server
                  timeout: 0s
                  idle_timeout: 0s

              # This route is for the ConsoleProxy.Connect bidirectional stream.
              - name: console-proxy-grpc
                match:
                  safe_regex:
                    regex: /osac\.public\..*ConsoleProxy/Connect
                route:
                  cluster: console-proxy-grpc
                  timeout: 0s
                  idle_timeout: 1800s

              # This route is for gRPC unary requests.
              - name: grpc-server
                match:
                  prefix: /
                route:
                  cluster: grpc-server
                  timeout: 300s

          http_filters:
          - name: envoy.filters.http.cors
            typed_config:
              "@type": type.googleapis.com/envoy.extensions.filters.http.cors.v3.Cors
          - name: envoy.filters.http.router
            typed_config:
              "@type": type.googleapis.com/envoy.extensions.filters.http.router.v3.Router

      transport_socket:
        name: envoy.transport_sockets.tls
        typed_config:
          "@type": type.googleapis.com/envoy.extensions.transport_sockets.tls.v3.DownstreamTlsContext
          common_tls_context:
            alpn_protocols:
            - http1.1
            - h2
            tls_certificates:
            - certificate_chain:
                filename: /etc/envoy/tls/tls.crt
              private_key:
                filename: /etc/envoy/tls/tls.key

  clusters:

  - name: admin
    connect_timeout: 1s
    type: STATIC
    lb_policy: ROUND_ROBIN
    load_assignment:
      cluster_name: admin
      endpoints:
      - lb_endpoints:
        - endpoint:
            address:
              socket_address:
                address: 127.0.0.1
                port_value: 9901

  - name: grpc-server
    connect_timeout: 1s
    type: STRICT_DNS
    lb_policy: ROUND_ROBIN
    typed_extension_protocol_options:
      envoy.extensions.upstreams.http.v3.HttpProtocolOptions:
        "@type": type.googleapis.com/envoy.extensions.upstreams.http.v3.HttpProtocolOptions
        explicit_http_config:
          http2_protocol_options: {}
    load_assignment:
      cluster_name: grpc-server
      endpoints:
      - lb_endpoints:
        - endpoint:
            address:
              socket_address:
                address: fulfillment-grpc-server
                port_value: 8000
    transport_socket:
      name: envoy.transport_sockets.tls
      typed_config:
        "@type": type.googleapis.com/envoy.extensions.transport_sockets.tls.v3.UpstreamTlsContext
        common_tls_context:
          validation_context:
            trusted_ca:
              filename: /etc/envoy/tls/ca.crt

  - name: rest-gateway
    connect_timeout: 1s
    type: STRICT_DNS
    lb_policy: ROUND_ROBIN
    typed_extension_protocol_options:
      envoy.extensions.upstreams.http.v3.HttpProtocolOptions:
        "@type": type.googleapis.com/envoy.extensions.upstreams.http.v3.HttpProtocolOptions
        explicit_http_config:
          http2_protocol_options: {}
    load_assignment:
      cluster_name: rest-gateway
      endpoints:
      - lb_endpoints:
        - endpoint:
            address:
              socket_address:
                address: fulfillment-rest-gateway
                port_value: 8000
    transport_socket:
      name: envoy.transport_sockets.tls
      typed_config:
        "@type": type.googleapis.com/envoy.extensions.transport_sockets.tls.v3.UpstreamTlsContext
        common_tls_context:
          validation_context:
            trusted_ca:
              filename: /etc/envoy/tls/ca.crt

  # This cluster connects to the console proxy WebSocket listener. It must use
  # HTTP/1.1 (no http2_protocol_options) because WebSocket requires an HTTP/1.1
  # upgrade handshake.
  - name: console-proxy-ws
    connect_timeout: 1s
    type: STRICT_DNS
    lb_policy: ROUND_ROBIN
    load_assignment:
      cluster_name: console-proxy-ws
      endpoints:
      - lb_endpoints:
        - endpoint:
            address:
              socket_address:
                address: fulfillment-console-proxy
                port_value: 8090
    transport_socket:
      name: envoy.transport_sockets.tls
      typed_config:
        "@type": type.googleapis.com/envoy.extensions.transport_sockets.tls.v3.UpstreamTlsContext
        common_tls_context:
          validation_context:
            trusted_ca:
              filename: /etc/envoy/tls/ca.crt

  # This cluster connects to the console proxy gRPC listener for the
  # ConsoleProxy.Connect bidirectional stream.
  - name: console-proxy-grpc
    connect_timeout: 1s
    type: STRICT_DNS
    lb_policy: ROUND_ROBIN
    typed_extension_protocol_options:
      envoy.extensions.upstreams.http.v3.HttpProtocolOptions:
        "@type": type.googleapis.com/envoy.extensions.upstreams.http.v3.HttpProtocolOptions
        explicit_http_config:
          http2_protocol_options: {}
    load_assignment:
      cluster_name: console-proxy-grpc
      endpoints:
      - lb_endpoints:
        - endpoint:
            address:
              socket_address:
                address: fulfillment-console-proxy
                port_value: 8000
    transport_socket:
      name: envoy.transport_sockets.tls
      typed_config:
        "@type": type.googleapis.com/envoy.extensions.transport_sockets.tls.v3.UpstreamTlsContext
        common_tls_context:
          validation_context:
            trusted_ca:
              filename: /etc/envoy/tls/ca.crt

{{ end }}
