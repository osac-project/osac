/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package tenantbillingassignment

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"time"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/osac-project/osac/fulfillment-service/internal/terminal"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

// assignmentsServer is a fake gRPC server that records the paging bounds of the last List request.
type assignmentsServer struct {
	publicv1.UnimplementedTenantBillingAssignmentsServer
	response  *publicv1.TenantBillingAssignmentsListResponse
	listErr   error
	gotLimit  int32
	gotOffset int32
	sawLimit  bool
	sawOffset bool
}

// List is the implementation of the gRPC method.
func (s *assignmentsServer) List(_ context.Context, r *publicv1.TenantBillingAssignmentsListRequest) (*publicv1.TenantBillingAssignmentsListResponse, error) {
	s.gotLimit = r.GetLimit()
	s.gotOffset = r.GetOffset()
	s.sawLimit = r.HasLimit()
	s.sawOffset = r.HasOffset()
	if s.listErr != nil {
		return nil, s.listErr
	}
	return s.response, nil
}

// assignmentsConnection exposes the fake server over an in-memory connection.
func assignmentsConnection(server *assignmentsServer) *grpc.ClientConn {
	GinkgoHelper()
	listener := bufconn.Listen(1024 * 1024)
	grpcServer := grpc.NewServer()
	publicv1.RegisterTenantBillingAssignmentsServer(grpcServer, server)
	go func() {
		_ = grpcServer.Serve(listener)
	}()
	conn, err := grpc.NewClient("passthrough:///assignments-test", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
		return listener.Dial()
	}))
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() {
		Expect(conn.Close()).To(Succeed())
		grpcServer.Stop()
		Expect(listener.Close()).To(Succeed())
	})
	return conn
}

// failingWriter fails every write with a persistent error, to simulate a broken output stream.
type failingWriter struct {
	err error
}

// Write is the implementation of the io.Writer interface.
func (w *failingWriter) Write(p []byte) (int, error) {
	return 0, w.err
}

var _ = Describe("render", func() {
	// newConsole builds a console that captures both streams, so no test writes to the real stderr.
	newConsole := func(stdout, stderr io.Writer) *terminal.Console {
		console, err := terminal.NewConsole().SetLogger(logger).SetStdout(stdout).SetStderr(stderr).Build()
		Expect(err).ToNot(HaveOccurred())
		return console
	}

	run := func(format string, offset int32, response *publicv1.TenantBillingAssignmentsListResponse) (string, string) {
		stdout := &bytes.Buffer{}
		stderr := &bytes.Buffer{}
		console := newConsole(stdout, stderr)
		Expect(render(ctx, console, format, offset, response)).To(Succeed())
		return stdout.String(), stderr.String()
	}

	listWith := func(items ...*publicv1.TenantBillingAssignment) *publicv1.TenantBillingAssignmentsListResponse {
		response := &publicv1.TenantBillingAssignmentsListResponse{}
		response.SetSize(int32(len(items)))
		response.SetTotal(int32(len(items)))
		response.SetItems(items)
		return response
	}

	pageWith := func(total int32, items ...*publicv1.TenantBillingAssignment) *publicv1.TenantBillingAssignmentsListResponse {
		return publicv1.TenantBillingAssignmentsListResponse_builder{
			Items: items, Size: int32(len(items)), Total: total,
		}.Build()
	}

	connected := publicv1.TenantBillingAssignment_builder{
		Id: "asg-1",
		Spec: publicv1.TenantBillingAssignmentSpec_builder{
			TenantId: "tenant-a", BillingAccountId: "ba-1",
		}.Build(),
		Status: publicv1.TenantBillingAssignmentStatus_builder{
			ConnectionState: publicv1.TenantBillingAssignmentConnectionState_TENANT_BILLING_ASSIGNMENT_CONNECTION_STATE_CONNECTED,
			EffectiveFrom:   timestamppb.New(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)),
		}.Build(),
	}.Build()

	pending := publicv1.TenantBillingAssignment_builder{
		Id: "asg-2",
		Spec: publicv1.TenantBillingAssignmentSpec_builder{
			TenantId: "tenant-b", BillingAccountId: "ba-2",
		}.Build(),
		Status: publicv1.TenantBillingAssignmentStatus_builder{
			ConnectionState: publicv1.TenantBillingAssignmentConnectionState_TENANT_BILLING_ASSIGNMENT_CONNECTION_STATE_PENDING,
			EffectiveFrom:   timestamppb.New(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)),
			Message: "BILLING_PROVIDER_CORRELATION_WRITE_UNSUPPORTED: set externalId on the account in the " +
				"provider to the correlation_id shown by BillingAccounts.List",
		}.Build(),
	}.Build()

	failed := publicv1.TenantBillingAssignment_builder{
		Id: "asg-3",
		Spec: publicv1.TenantBillingAssignmentSpec_builder{
			TenantId: "tenant-c", BillingAccountId: "ba-3",
		}.Build(),
		Status: publicv1.TenantBillingAssignmentStatus_builder{
			ConnectionState: publicv1.TenantBillingAssignmentConnectionState_TENANT_BILLING_ASSIGNMENT_CONNECTION_STATE_FAILED,
		}.Build(),
	}.Build()

	It("renders a CONNECTED assignment without an enum prefix and a dash for the unset effective_to", func() {
		output, stderr := run("table", 0, listWith(connected))
		Expect(output).To(MatchRegexp(`\bCONNECTED\b`))
		Expect(output).ToNot(ContainSubstring("TENANT_BILLING_ASSIGNMENT_CONNECTION_STATE_"))
		Expect(output).To(ContainSubstring("tenant-a"))
		Expect(output).To(ContainSubstring("ba-1"))
		Expect(output).To(MatchRegexp(`-\s*\n`))
		Expect(stderr).To(BeEmpty())
	})

	It("shows the PENDING remediation message", func() {
		output, stderr := run("table", 0, listWith(pending))
		Expect(output).To(MatchRegexp(`\bPENDING\b`))
		Expect(output).To(ContainSubstring("BILLING_PROVIDER_CORRELATION_WRITE_UNSUPPORTED"))
		Expect(stderr).To(BeEmpty())
	})

	It("renders the FAILED state, though this increment never produces it", func() {
		output, stderr := run("table", 0, listWith(failed))
		Expect(output).To(MatchRegexp(`\bFAILED\b`))
		Expect(stderr).To(BeEmpty())
	})

	It("says so when there are no assignments, in table mode", func() {
		output, stderr := run("table", 0, listWith())
		Expect(output).To(ContainSubstring("No tenant billing assignments found"))
		Expect(stderr).To(BeEmpty())
	})

	It("hints the next page on standard error, leaving the table alone on standard output", func() {
		stdout, stderr := run("table", 0, pageWith(5, connected, pending))
		Expect(stdout).To(ContainSubstring("asg-1"))
		Expect(stdout).ToNot(ContainSubstring("Showing"))
		Expect(stderr).To(Equal("Showing 1-2 of 5 tenant billing assignments. Next page: --offset 2\n"))
	})

	It("keeps standard error empty when the page holds every assignment", func() {
		stdout, stderr := run("table", 100, pageWith(2, connected, pending))
		Expect(stdout).To(ContainSubstring("asg-1"))
		Expect(stderr).To(BeEmpty())
	})

	It("does not claim an empty collection for an empty page at or beyond the end", func() {
		stdout, stderr := run("table", 5, pageWith(5))
		Expect(stdout).To(BeEmpty())
		Expect(stderr).To(Equal("No tenant billing assignments on this page: offset 5 is at or beyond the total 5.\n"))
	})

	It("renders JSON with API field names", func() {
		output, stderr := run("json", 0, listWith(connected))
		Expect(output).To(MatchJSON(`{
			"size": 1,
			"total": 1,
			"items": [{
				"id": "asg-1",
				"spec": {"tenant_id": "tenant-a", "billing_account_id": "ba-1"},
				"status": {
					"connection_state": "TENANT_BILLING_ASSIGNMENT_CONNECTION_STATE_CONNECTED",
					"effective_from": "2026-01-02T03:04:05Z"
				}
			}]
		}`))
		Expect(stderr).To(BeEmpty())
	})

	It("renders YAML with API field names", func() {
		output, stderr := run("yaml", 0, listWith(pending))
		Expect(output).To(ContainSubstring("tenant_id: tenant-b"))
		Expect(output).To(ContainSubstring("BILLING_PROVIDER_CORRELATION_WRITE_UNSUPPORTED"))
		Expect(stderr).To(BeEmpty())
	})
})

var _ = Describe("render failures", func() {
	sentinel := errors.New("broken stream")

	renderErr := func(format string, offset int32, response *publicv1.TenantBillingAssignmentsListResponse,
		stdout, stderr io.Writer) error {
		console, err := terminal.NewConsole().SetLogger(logger).SetStdout(stdout).SetStderr(stderr).Build()
		Expect(err).ToNot(HaveOccurred())
		return render(ctx, console, format, offset, response)
	}

	pageWith := func(total int32, items ...*publicv1.TenantBillingAssignment) *publicv1.TenantBillingAssignmentsListResponse {
		return publicv1.TenantBillingAssignmentsListResponse_builder{
			Items: items, Size: int32(len(items)), Total: total,
		}.Build()
	}

	connected := publicv1.TenantBillingAssignment_builder{
		Id: "asg-1",
		Spec: publicv1.TenantBillingAssignmentSpec_builder{
			TenantId: "tenant-a", BillingAccountId: "ba-1",
		}.Build(),
		Status: publicv1.TenantBillingAssignmentStatus_builder{
			ConnectionState: publicv1.TenantBillingAssignmentConnectionState_TENANT_BILLING_ASSIGNMENT_CONNECTION_STATE_CONNECTED,
		}.Build(),
	}.Build()

	It("reports a failed empty-collection message write", func() {
		err := renderErr("table", 0, pageWith(0), &failingWriter{err: sentinel}, &bytes.Buffer{})
		Expect(err).To(MatchError(ContainSubstring("failed to write info output")))
		Expect(errors.Is(err, sentinel)).To(BeTrue())
	})

	It("reports a failed table flush wrapping the writer error", func() {
		err := renderErr("table", 0, pageWith(1, connected), &failingWriter{err: sentinel}, &bytes.Buffer{})
		Expect(err).To(MatchError(ContainSubstring("failed to flush tenant billing assignments table")))
		Expect(errors.Is(err, sentinel)).To(BeTrue())
	})

	It("reports a failed JSON write", func() {
		err := renderErr("json", 0, pageWith(1, connected), &failingWriter{err: sentinel}, &bytes.Buffer{})
		Expect(err).To(MatchError(ContainSubstring("failed to render tenant billing assignments")))
		Expect(errors.Is(err, sentinel)).To(BeTrue())
	})

	It("reports a failed YAML write", func() {
		err := renderErr("yaml", 0, pageWith(1, connected), &failingWriter{err: sentinel}, &bytes.Buffer{})
		Expect(err).To(MatchError(ContainSubstring("failed to render tenant billing assignments")))
		Expect(errors.Is(err, sentinel)).To(BeTrue())
	})

	It("reports a failed page notice write", func() {
		err := renderErr("table", 0, pageWith(5, connected), &bytes.Buffer{}, &failingWriter{err: sentinel})
		Expect(err).To(MatchError(ContainSubstring("failed to write the tenant billing assignments page notice")))
		Expect(errors.Is(err, sentinel)).To(BeTrue())
	})

	It("does not report a failure when the table and the page notice both succeed", func() {
		err := renderErr("table", 0, pageWith(5, connected), &bytes.Buffer{}, &bytes.Buffer{})
		Expect(err).ToNot(HaveOccurred())
	})
})

var _ = Describe("listTenantBillingAssignments", func() {
	var (
		server *assignmentsServer
		conn   *grpc.ClientConn
	)

	BeforeEach(func() {
		server = &assignmentsServer{
			response: publicv1.TenantBillingAssignmentsListResponse_builder{}.Build(),
		}
		conn = assignmentsConnection(server)
	})

	It("sends the exact bounds to the server", func() {
		_, err := listTenantBillingAssignments(ctx, publicv1.NewTenantBillingAssignmentsClient(conn), 7, 42)
		Expect(err).NotTo(HaveOccurred())
		Expect(server.gotLimit).To(Equal(int32(7)))
		Expect(server.gotOffset).To(Equal(int32(42)))
		Expect(server.sawLimit).To(BeTrue())
		Expect(server.sawOffset).To(BeTrue())
	})

	It("sends the bounds as explicit fields even when they are zero", func() {
		_, err := listTenantBillingAssignments(ctx, publicv1.NewTenantBillingAssignmentsClient(conn), 0, 0)
		Expect(err).NotTo(HaveOccurred())
		Expect(server.gotLimit).To(Equal(int32(0)))
		Expect(server.gotOffset).To(Equal(int32(0)))
		Expect(server.sawLimit).To(BeTrue())
		Expect(server.sawOffset).To(BeTrue())
	})

	It("wraps RPC errors", func() {
		server.listErr = status.Error(codes.PermissionDenied, "list blocked")
		_, err := listTenantBillingAssignments(ctx, publicv1.NewTenantBillingAssignmentsClient(conn), 0, 0)
		Expect(err).To(MatchError(ContainSubstring("failed to list tenant billing assignments")))
		Expect(err).To(MatchError(ContainSubstring("list blocked")))
	})

	It("wraps a canceled context", func() {
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		_, err := listTenantBillingAssignments(canceled, publicv1.NewTenantBillingAssignmentsClient(conn), 0, 0)
		Expect(err).To(MatchError(ContainSubstring("failed to list tenant billing assignments")))
		Expect(status.Code(err)).To(Equal(codes.Canceled))
	})
})

var _ = Describe("tenantbillingassignment flags", func() {
	execute := func(args ...string) error {
		cmd := Cmd()
		cmd.SetOut(&bytes.Buffer{})
		cmd.SetErr(&bytes.Buffer{})
		cmd.SetArgs(args)
		return cmd.Execute()
	}

	It("rejects a negative --limit before any configuration or connection", func() {
		err := execute("--limit", "-1")
		Expect(err).To(MatchError(ContainSubstring("--limit must be >= 0, got -1")))
	})

	It("rejects a negative --offset before any configuration or connection", func() {
		err := execute("--offset", "-3")
		Expect(err).To(MatchError(ContainSubstring("--offset must be >= 0, got -3")))
	})

	It("rejects a --limit beyond the int32 range while parsing", func() {
		err := execute("--limit", "2147483648")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("invalid argument"))
	})

	It("rejects an --offset beyond the int32 range while parsing", func() {
		err := execute("--offset", "2147483648")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("invalid argument"))
	})
})
