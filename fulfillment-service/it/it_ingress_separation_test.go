/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package it

import (
	"context"
	"io"
	"net/http"
	"time"

	"github.com/coder/websocket"
	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	privatev1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/private/v1"
	publicv1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/public/v1"
)

var _ = Describe("Ingress separation", func() {
	var ctx context.Context

	BeforeEach(func() {
		ctx = context.Background()
	})

	It("Should be able to use the public gRPC public api via the external ingress", func() {
		client := publicv1.NewClusterTemplatesClient(tool.ExternalView().UserConn())
		response, err := client.List(ctx, nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(response).ToNot(BeNil())
	})

	It("Should be able to use the public gRPC API via the internal ingress", func() {
		client := publicv1.NewClusterTemplatesClient(tool.InternalView().AdminConn())
		response, err := client.List(ctx, nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(response).ToNot(BeNil())
	})

	It("Should not be able to use the private gRPC API via the external ingress", func() {
		client := privatev1.NewClusterTemplatesClient(tool.ExternalView().AdminConn())
		response, err := client.List(ctx, nil)
		Expect(err).To(HaveOccurred())
		Expect(response).To(BeNil())
		status, ok := grpcstatus.FromError(err)
		Expect(ok).To(BeTrue())
		Expect(status.Code()).To(Equal(grpccodes.Unimplemented))
	})

	It("Should be able to use the public REST API via the external ingress", func() {
		request, err := http.NewRequestWithContext(
			ctx,
			http.MethodGet,
			"/api/fulfillment/v1/cluster_templates",
			nil,
		)
		Expect(err).ToNot(HaveOccurred())
		response, err := tool.ExternalView().UserClient().Do(request)
		Expect(err).ToNot(HaveOccurred())
		defer response.Body.Close()
		Expect(response.StatusCode).To(Equal(http.StatusOK))
		_, err = io.Copy(io.Discard, response.Body)
		Expect(err).ToNot(HaveOccurred())
	})

	It("Should be able to use the private REST API via the internal ingress", func() {
		request, err := http.NewRequestWithContext(
			ctx,
			http.MethodGet,
			"/api/private/v1/cluster_templates",
			nil,
		)
		Expect(err).ToNot(HaveOccurred())
		response, err := tool.InternalView().AdminClient().Do(request)
		Expect(err).ToNot(HaveOccurred())
		Expect(response).ToNot(BeNil())
		defer response.Body.Close()
		Expect(response.StatusCode).To(Equal(http.StatusOK))
	})

	It("Should not be able to use the private REST API via the external ingress", func() {
		request, err := http.NewRequestWithContext(
			ctx,
			http.MethodGet,
			"/api/private/v1/cluster_templates",
			nil,
		)
		Expect(err).ToNot(HaveOccurred())
		response, err := tool.ExternalView().AdminClient().Do(request)
		Expect(err).ToNot(HaveOccurred())
		Expect(response).ToNot(BeNil())
		defer response.Body.Close()
		Expect(response.StatusCode).To(Equal(http.StatusNotFound))
	})

	// Console proxy routing tests
	//
	// These tests verify that the Envoy ingress proxy routes console endpoints
	// to the console-proxy service (not to the rest-gateway or grpc-server).
	//
	// The tests send requests without a valid console ticket. The console proxy
	// validates tickets via JWE+JWS (signed with the token-encryption-keypair
	// cert and verified via the gRPC server's JWKS endpoint). The Keycloak JWT
	// that the admin client carries is not a console ticket, so OpenTicket
	// rejects it. This is sufficient to prove routing: the error codes below
	// are specific to the console proxy and would NOT be returned by the
	// rest-gateway or grpc-server backends.
	//
	// Exercising the full ticket path (codes.Unavailable / WS close 1014) would
	// require a running ComputeInstance with a KubeVirt backend on the hub
	// cluster, which the basic Kind IT environment does not provide.

	It("Should route the gRPC console endpoint via the internal ingress", func() {
		// ConsoleProxy.Connect is routed to the console-proxy-grpc cluster by the
		// Envoy config. The admin Keycloak token is not a valid console ticket, so
		// the console proxy returns Unauthenticated after ticket verification fails.
		// If routing were wrong (e.g. falling through to grpc-server), we would get
		// Unimplemented instead.
		grpcCtx, grpcCancel := context.WithTimeout(ctx, 30*time.Second)
		defer grpcCancel()
		client := publicv1.NewConsoleProxyClient(tool.InternalView().AdminConn())
		stream, err := client.Connect(grpcCtx)
		if err != nil {
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.Unauthenticated))
			return
		}
		_, err = stream.Recv()
		Expect(err).To(HaveOccurred())
		status, ok := grpcstatus.FromError(err)
		Expect(ok).To(BeTrue())
		Expect(status.Code()).To(Equal(grpccodes.Unauthenticated))
	})

	It("Should route the gRPC console endpoint via the external ingress", func() {
		grpcCtx, grpcCancel := context.WithTimeout(ctx, 30*time.Second)
		defer grpcCancel()
		client := publicv1.NewConsoleProxyClient(tool.ExternalView().AdminConn())
		stream, err := client.Connect(grpcCtx)
		if err != nil {
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.Unauthenticated))
			return
		}
		_, err = stream.Recv()
		Expect(err).To(HaveOccurred())
		status, ok := grpcstatus.FromError(err)
		Expect(ok).To(BeTrue())
		Expect(status.Code()).To(Equal(grpccodes.Unauthenticated))
	})

	It("Should route the WebSocket console endpoint via the internal ingress", func() {
		// The console-ws route sends /api/fulfillment/v1/console_sessions/connect
		// to the console-proxy-ws cluster (HTTP/1.1, port 8090). The admin client
		// carries a Bearer token so the handler passes the origin pre-check and
		// accepts the WebSocket upgrade. OpenTicket rejects the Keycloak JWT,
		// so the proxy closes the connection with WS status 3000 (unauthorized).
		// If routing were wrong (falling through to rest-gateway), the WebSocket
		// upgrade would fail entirely with an HTTP error.
		dialCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()

		ws, _, err := websocket.Dial(dialCtx,
			"wss://"+internalServiceAddr+"/api/fulfillment/v1/console_sessions/connect",
			&websocket.DialOptions{
				HTTPClient: tool.InternalView().AdminClient(),
			},
		)
		if err != nil {
			Expect(websocket.CloseStatus(err)).To(
				Equal(websocket.StatusCode(3000)),
				"expected WS close 3000 (unauthorized) from console proxy, got different error: %v", err,
			)
			return
		}
		defer ws.CloseNow() // best-effort cleanup; connection is already closed by server

		_, _, err = ws.Read(dialCtx)
		Expect(err).To(HaveOccurred())
		Expect(websocket.CloseStatus(err)).To(
			Equal(websocket.StatusCode(3000)),
			"expected WS close 3000 (unauthorized) from console proxy",
		)
	})

	It("Should route the WebSocket console endpoint via the external ingress", func() {
		dialCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()

		ws, _, err := websocket.Dial(dialCtx,
			"wss://"+externalServiceAddr+"/api/fulfillment/v1/console_sessions/connect",
			&websocket.DialOptions{
				HTTPClient: tool.ExternalView().AdminClient(),
			},
		)
		if err != nil {
			Expect(websocket.CloseStatus(err)).To(
				Equal(websocket.StatusCode(3000)),
				"expected WS close 3000 (unauthorized) from console proxy, got different error: %v", err,
			)
			return
		}
		defer ws.CloseNow() // best-effort cleanup; connection is already closed by server

		_, _, err = ws.Read(dialCtx)
		Expect(err).To(HaveOccurred())
		Expect(websocket.CloseStatus(err)).To(
			Equal(websocket.StatusCode(3000)),
			"expected WS close 3000 (unauthorized) from console proxy",
		)
	})
})
