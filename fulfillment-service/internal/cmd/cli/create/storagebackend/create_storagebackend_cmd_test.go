/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package storagebackend

import (
	"context"
	"io"
	"log/slog"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/osac-project/osac/fulfillment-service/internal/config"
	"github.com/osac-project/osac/fulfillment-service/internal/logging"
	"github.com/osac-project/osac/fulfillment-service/internal/terminal"
	"github.com/osac-project/osac/fulfillment-service/internal/testing"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

type storageBackendsServer struct {
	privatev1.UnimplementedStorageBackendsServer
	request *privatev1.StorageBackendsCreateRequest
}

func (s *storageBackendsServer) Create(_ context.Context, request *privatev1.StorageBackendsCreateRequest) (*privatev1.StorageBackendsCreateResponse, error) {
	s.request = request
	return privatev1.StorageBackendsCreateResponse_builder{Object: request.GetObject()}.Build(), nil
}

var _ = Describe("Create storage backend", func() {
	DescribeTable("sends the requested ownership and only supplied credentials",
		func(flags []string, expectedError string, cspManaged, credentials bool) {
			server := testing.NewServer()
			DeferCleanup(server.Stop)
			backends := &storageBackendsServer{}
			privatev1.RegisterStorageBackendsServer(server.Registrar(), backends)
			server.Start()
			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			settings, err := config.NewSettings().SetLogger(logger).SetDir(GinkgoT().TempDir()).Build()
			Expect(err).NotTo(HaveOccurred())
			settings.SetAddress(server.Address())
			settings.SetPlaintext(true)
			console, err := terminal.NewConsole().SetLogger(logger).SetStdout(io.Discard).SetStderr(io.Discard).Build()
			Expect(err).NotTo(HaveOccurred())
			ctx := logging.LoggerIntoContext(context.Background(), logger)
			ctx = config.SettingsIntoContext(ctx, settings)
			ctx = terminal.ConsoleIntoContext(ctx, console)
			cmd := Cmd()
			cmd.SetContext(ctx)
			cmd.SetOut(io.Discard)
			cmd.SetErr(io.Discard)
			cmd.SetArgs(append([]string{"--name", "customer-storage", "--provider", "netapp"}, flags...))
			err = cmd.Execute()
			if expectedError != "" {
				Expect(err).To(MatchError(ContainSubstring(expectedError)))
				Expect(backends.request).To(BeNil())
				return
			}
			Expect(err).NotTo(HaveOccurred())
			Expect(backends.request).NotTo(BeNil())
			spec := backends.request.GetObject().GetSpec()
			Expect(spec.GetCspManaged()).To(Equal(cspManaged))
			if credentials {
				Expect(spec.GetCredentials().GetUsername()).To(Equal("admin"))
				Expect(spec.GetCredentials().GetPassword()).To(Equal("fixture-password"))
			} else {
				Expect(spec.GetCredentials()).To(BeNil())
			}
		},
		Entry("CSP-managed without connection details", []string{"--csp-managed"}, "", true, false),
		Entry("CSP-managed with endpoint only", []string{"--csp-managed", "--endpoint", "https://storage.example.com"}, "", true, false),
		Entry("CSP-managed supplied credentials", []string{"--csp-managed", "--username", "admin", "--password", "fixture-password"}, "", true, true),
		Entry("CSP-managed incomplete username", []string{"--csp-managed", "--password", "fixture-password"}, "username is required", true, false),
		Entry("CSP-managed incomplete password", []string{"--csp-managed", "--username", "admin"}, "password is required", true, false),
		Entry("default ownership requires endpoint", []string{}, "endpoint is required", false, false),
		Entry("default ownership requires username", []string{"--endpoint", "https://storage.example.com"}, "username is required", false, false),
		Entry("default ownership requires password", []string{"--endpoint", "https://storage.example.com", "--username", "admin"}, "password is required", false, false),
		Entry("default ownership sends valid credentials", []string{"--endpoint", "https://storage.example.com", "--username", "admin", "--password", "fixture-password"}, "", false, true),
	)
})
