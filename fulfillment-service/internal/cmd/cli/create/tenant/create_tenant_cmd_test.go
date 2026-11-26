/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package tenant

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"time"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"

	"github.com/osac-project/osac/fulfillment-service/internal/config"
	"github.com/osac-project/osac/fulfillment-service/internal/logging"
	"github.com/osac-project/osac/fulfillment-service/internal/terminal"
	"github.com/osac-project/osac/fulfillment-service/internal/testing"
	"google.golang.org/protobuf/types/known/timestamppb"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

type tenantsServer struct {
	privatev1.UnimplementedTenantsServer

	createCalls int
	request     *privatev1.TenantsCreateRequest
	createErr   error
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) {
	return 0, errors.New("stdout closed")
}

func (s *tenantsServer) Create(_ context.Context, request *privatev1.TenantsCreateRequest) (*privatev1.TenantsCreateResponse, error) {
	s.createCalls++
	if s.createErr != nil {
		return nil, s.createErr
	}
	s.request = request
	object := request.GetObject()
	tenant := privatev1.Tenant_builder{
		Id: "tenant-id",
		Metadata: privatev1.Metadata_builder{
			Name:              object.GetMetadata().GetName(),
			CreationTimestamp: timestamppb.New(time.Date(2026, 11, 26, 11, 0, 24, 992124975, time.UTC)),
		}.Build(),
		Spec: privatev1.TenantSpec_builder{
			Domains: object.GetSpec().GetDomains(),
		}.Build(),
	}
	tenant.Status = privatev1.TenantStatus_builder{
		State: privatev1.TenantState_TENANT_STATE_SYNCED,
	}.Build()
	return privatev1.TenantsCreateResponse_builder{Object: tenant.Build()}.Build(), nil
}

func newTestContext(server *testing.Server, stdout io.Writer) context.Context {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	settings, err := config.NewSettings().
		SetLogger(logger).
		SetDir(GinkgoT().TempDir()).
		Build()
	Expect(err).NotTo(HaveOccurred())
	settings.SetAddress(server.Address())
	settings.SetPlaintext(true)
	settings.SetPrivate(true)

	console, err := terminal.NewConsole().
		SetLogger(logger).
		SetStdout(stdout).
		SetStderr(io.Discard).
		SetColorEnabled(false).
		Build()
	Expect(err).NotTo(HaveOccurred())

	ctx := logging.LoggerIntoContext(context.Background(), logger)
	ctx = config.SettingsIntoContext(ctx, settings)
	return terminal.ConsoleIntoContext(ctx, console)
}

func newUnarmedTestContext(stdout io.Writer) context.Context {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	settings, err := config.NewSettings().
		SetLogger(logger).
		SetDir(GinkgoT().TempDir()).
		Build()
	Expect(err).NotTo(HaveOccurred())

	console, err := terminal.NewConsole().
		SetLogger(logger).
		SetStdout(stdout).
		SetStderr(io.Discard).
		SetColorEnabled(false).
		Build()
	Expect(err).NotTo(HaveOccurred())

	ctx := logging.LoggerIntoContext(context.Background(), logger)
	ctx = config.SettingsIntoContext(ctx, settings)
	return terminal.ConsoleIntoContext(ctx, console)
}

var _ = Describe("Create tenant command", func() {
	Describe("command structure", func() {
		It("uses a positional name", func() {
			cmd := Cmd()
			Expect(cmd.Use).To(Equal("tenant [FLAG...] NAME"))
			Expect(cmd.Args).NotTo(BeNil())
			Expect(cmd.Args(cmd, []string{"example"})).To(Succeed())
			Expect(cmd.Args(cmd, []string{})).NotTo(Succeed())
			Expect(cmd.Args(cmd, []string{"example", "extra"})).NotTo(Succeed())
		})

		It("has the private protobuf alias", func() {
			cmd := Cmd()
			Expect(cmd.Aliases).To(ContainElement("osac.private.v1.Tenant"))
		})

		It("registers the domain and output flags", func() {
			cmd := Cmd()
			domain := cmd.Flags().Lookup("domain")
			Expect(domain).NotTo(BeNil())
			Expect(domain.Usage).To(ContainSubstring("DOMAIN"))
			output := cmd.Flags().Lookup("output")
			Expect(output).NotTo(BeNil())
			Expect(output.Shorthand).To(Equal("o"))
			Expect(output.DefValue).To(Equal("text"))
		})

		It("documents the command and flags", func() {
			cmd := Cmd()
			Expect(cmd.Short).To(Equal("Create a tenant"))
			Expect(cmd.Long).To(ContainSubstring("create tenant example"))
			Expect(cmd.Long).To(ContainSubstring("--domain example.com"))
			Expect(cmd.Long).To(ContainSubstring("--output json"))
		})
	})

	Describe("buildTenant", func() {
		It("sets only the requested name and domains", func() {
			tenant := buildTenant("example", []string{"example.com", "corp.example.com"})
			Expect(tenant.GetMetadata().GetName()).To(Equal("example"))
			Expect(tenant.GetMetadata().GetTenant()).To(BeEmpty())
			Expect(tenant.GetSpec().GetDomains()).To(Equal([]string{"example.com", "corp.example.com"}))
		})
	})

	Describe("execution", func() {
		var (
			server  *testing.Server
			tenants *tenantsServer
			stdout  *bytes.Buffer
		)

		BeforeEach(func() {
			server = testing.NewServer()
			DeferCleanup(server.Stop)
			tenants = &tenantsServer{}
			stdout = &bytes.Buffer{}
		})

		runCommandWithStdout := func(stdout io.Writer, args ...string) error {
			privatev1.RegisterTenantsServer(server.Registrar(), tenants)
			server.Start()
			cmd := Cmd()
			cmd.SetContext(newTestContext(server, stdout))
			cmd.SetArgs(args)
			return cmd.Execute()
		}

		runCommand := func(args ...string) error {
			return runCommandWithStdout(stdout, args...)
		}

		It("sends the requested tenant and renders human-readable output by default", func() {
			err := runCommand("example", "--domain", "example.com", "--domain", "corp.example.com")
			Expect(err).NotTo(HaveOccurred())

			Expect(tenants.request.GetObject().GetMetadata().GetName()).To(Equal("example"))
			Expect(tenants.request.GetObject().GetMetadata().GetTenant()).To(BeEmpty())
			Expect(tenants.request.GetObject().GetSpec().GetDomains()).To(Equal([]string{"example.com", "corp.example.com"}))
			Expect(stdout.String()).To(Equal("Created tenant 'example' (ID: tenant-id).\n"))
		})

		It("returns JSON output write failures without retrying creation", func() {
			err := runCommandWithStdout(failingWriter{}, "example", "--domain", "example.com", "--output", "json")
			Expect(err).To(MatchError(ContainSubstring("tenant created, but failed to write JSON output")))
			Expect(err).To(MatchError(ContainSubstring("stdout closed")))
			Expect(tenants.createCalls).To(Equal(1))
		})

		It("renders the created tenant as protobuf JSON", func() {
			err := runCommand("example", "--domain", "example.com", "--output", "json")
			Expect(err).NotTo(HaveOccurred())

			var decoded struct {
				ID       string `json:"id"`
				Metadata struct {
					Name              string `json:"name"`
					CreationTimestamp string `json:"creation_timestamp"`
				} `json:"metadata"`
				Spec struct {
					Domains []string `json:"domains"`
				} `json:"spec"`
				Status struct {
					State string `json:"state"`
				} `json:"status"`
			}
			Expect(json.Unmarshal(stdout.Bytes(), &decoded)).To(Succeed())
			Expect(decoded.ID).To(Equal("tenant-id"))
			Expect(decoded.Metadata.Name).To(Equal("example"))
			Expect(decoded.Metadata.CreationTimestamp).To(Equal("2026-11-26T11:00:24.992124975Z"))
			Expect(decoded.Spec.Domains).To(Equal([]string{"example.com"}))
			Expect(decoded.Status.State).To(Equal("TENANT_STATE_SYNCED"))
		})

		It("requires an authenticated configuration", func() {
			cmd := Cmd()
			cmd.SetContext(newUnarmedTestContext(stdout))
			cmd.SetArgs([]string{"example", "--domain", "example.com"})
			err := cmd.Execute()
			Expect(err).To(MatchError("there is no configuration, run the 'login' command"))
			Expect(stdout.String()).To(BeEmpty())
		})

		It("rejects an empty tenant name", func() {
			cmd := Cmd()
			cmd.SetContext(newTestContext(server, stdout))
			cmd.SetArgs([]string{""})
			err := cmd.Execute()
			Expect(err).To(MatchError("name is required"))
			Expect(stdout.String()).To(BeEmpty())
		})

		It("rejects an unsupported output format", func() {
			err := runCommand("example", "--output", "yaml")
			Expect(err).To(MatchError(ContainSubstring("unknown output format 'yaml'")))
			Expect(stdout.String()).To(BeEmpty())
		})
	})

	Describe("API errors", func() {
		It("surfaces create failures", func() {
			server := testing.NewServer()
			DeferCleanup(server.Stop)
			privatev1.RegisterTenantsServer(server.Registrar(), &tenantsServer{
				createErr: errors.New("domain already claimed"),
			})
			server.Start()
			stdout := &bytes.Buffer{}

			cmd := Cmd()
			cmd.SetContext(newTestContext(server, stdout))
			cmd.SetArgs([]string{"example", "--domain", "example.com"})
			err := cmd.Execute()
			Expect(err).To(MatchError(ContainSubstring("failed to create tenant")))
			Expect(err).To(MatchError(ContainSubstring("domain already claimed")))
			Expect(stdout.String()).To(BeEmpty())
		})
	})
})
