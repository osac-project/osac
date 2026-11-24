/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package servers

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"net"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gbytes"
	"google.golang.org/grpc"
	grpccodes "google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	"github.com/osac-project/osac/fulfillment-service/internal/database"
	"github.com/osac-project/osac/fulfillment-service/internal/logging"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

// These tests use real public/private handlers, gRPC interceptors, and PostgreSQL.
// The Secret store, tenancy, and attribution use the existing suite doubles.
var _ = Describe("Public Windows user-data Secret validation", func() {
	It("rejects malformed XML without persisting or exposing it in body logs", func(specCtx SpecContext) {
		// Commit the fixture tenant so request transactions can see it.
		Expect(suiteTx.End(ctx)).To(Succeed())
		suiteTx = nil

		output := gbytes.NewBuffer()
		DeferCleanup(output.Close)
		debugLogger := slog.New(slog.NewJSONHandler(output, &slog.HandlerOptions{Level: slog.LevelDebug}))
		store := newMemorySecretStore()
		publicServer, err := NewSecretsServer().
			SetLogger(debugLogger).
			SetAttributionLogic(attribution).
			SetTenancyLogic(tenancy).
			SetSecretStore(store).
			Build()
		Expect(err).ToNot(HaveOccurred())
		logInterceptor, err := logging.NewInterceptor().SetLogger(debugLogger).SetBodies(true).Build()
		Expect(err).ToNot(HaveOccurred())
		txInterceptor, err := database.NewTxInterceptor().SetLogger(debugLogger).SetManager(tm).Build()
		Expect(err).ToNot(HaveOccurred())

		listener := bufconn.Listen(1024 * 1024)
		DeferCleanup(listener.Close)
		grpcServer := grpc.NewServer(grpc.ChainUnaryInterceptor(logInterceptor.UnaryServer, txInterceptor.UnaryServer))
		publicv1.RegisterSecretsServer(grpcServer, publicServer)
		go func() {
			defer GinkgoRecover()
			Expect(grpcServer.Serve(listener)).To(Or(Succeed(), MatchError(grpc.ErrServerStopped)))
		}()
		DeferCleanup(grpcServer.Stop)
		connection, err := grpc.NewClient("passthrough:///bufconn",
			grpc.WithContextDialer(func(dialCtx context.Context, _ string) (net.Conn, error) {
				return listener.DialContext(dialCtx)
			}),
			grpc.WithTransportCredentials(insecure.NewCredentials()),
		)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(connection.Close)
		client := publicv1.NewSecretsClient(connection)

		const sentinel = "OSAC-7712-PUBLIC-SECRET-SENTINEL"
		payload := []byte("<root>" + sentinel)
		callCtx, cancel := context.WithTimeout(specCtx, 10*time.Second)
		DeferCleanup(cancel)
		_, createErr := client.Create(callCtx, publicv1.SecretsCreateRequest_builder{
			Object: publicv1.Secret_builder{
				Metadata: publicv1.Metadata_builder{Name: "windows-secret"}.Build(),
				Type:     publicv1.SecretType_SECRET_TYPE_WINDOWS_USER_DATA,
				Data:     map[string][]byte{"userdata": payload},
			}.Build(),
		}.Build())
		Expect(grpcstatus.Code(createErr)).To(Equal(grpccodes.InvalidArgument))
		Expect(grpcstatus.Convert(createErr).Message()).To(Equal(
			`secret type SECRET_TYPE_WINDOWS_USER_DATA data["userdata"] is not well-formed XML`))

		var count int
		Expect(testPool.QueryRow(callCtx, "select count(*) from secrets").Scan(&count)).To(Succeed())
		Expect(count).To(BeZero())
		storedData, err := store.Fetch(callCtx, testTenant, "", "windows-secret")
		Expect(err).ToNot(HaveOccurred())
		Expect(storedData).To(BeEmpty())

		logOutput := output.Contents()
		var requests, responses []map[string]any
		for _, line := range bytes.Split(bytes.TrimSpace(logOutput), []byte("\n")) {
			var record map[string]any
			Expect(json.Unmarshal(line, &record)).To(Succeed())
			if record["method"] != publicv1.Secrets_Create_FullMethodName {
				continue
			}
			switch record["msg"] {
			case "Received unary request":
				requests = append(requests, record)
			case "Sent unary response":
				responses = append(responses, record)
			}
		}
		Expect(requests).To(HaveLen(1))
		Expect(responses).To(HaveLen(1))
		Expect(requests[0]).To(HaveKeyWithValue("request", HaveKeyWithValue("object", And(
			HaveKeyWithValue("metadata", HaveKeyWithValue("name", "windows-secret")),
			HaveKeyWithValue("data", HaveKeyWithValue("userdata", base64.StdEncoding.EncodeToString([]byte("***")))),
		))))
		Expect(responses[0]).To(HaveKeyWithValue("code", "InvalidArgument"))
		Expect(responses[0]).To(HaveKeyWithValue("error", createErr.Error()))
		for _, sensitive := range []string{
			sentinel, string(payload), base64.StdEncoding.EncodeToString(payload),
			base64.StdEncoding.EncodeToString([]byte(sentinel)),
		} {
			Expect(string(logOutput)).NotTo(ContainSubstring(sensitive))
			Expect(createErr.Error()).NotTo(ContainSubstring(sensitive))
		}
	})
})
