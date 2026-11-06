/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package billingaccount

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

// billingAccountsServer is a fake gRPC server that records the paging bounds of the last List request.
type billingAccountsServer struct {
	publicv1.UnimplementedBillingAccountsServer
	response  *publicv1.BillingAccountsListResponse
	listErr   error
	gotLimit  int32
	gotOffset int32
	sawLimit  bool
	sawOffset bool
}

// List is the implementation of the gRPC method.
func (s *billingAccountsServer) List(_ context.Context, r *publicv1.BillingAccountsListRequest) (*publicv1.BillingAccountsListResponse, error) {
	s.gotLimit = r.GetLimit()
	s.gotOffset = r.GetOffset()
	s.sawLimit = r.HasLimit()
	s.sawOffset = r.HasOffset()
	if s.listErr != nil {
		return nil, s.listErr
	}
	return s.response, nil
}

// billingAccountsConnection exposes the fake server over an in-memory connection.
func billingAccountsConnection(server *billingAccountsServer) *grpc.ClientConn {
	GinkgoHelper()
	listener := bufconn.Listen(1024 * 1024)
	grpcServer := grpc.NewServer()
	publicv1.RegisterBillingAccountsServer(grpcServer, server)
	go func() {
		_ = grpcServer.Serve(listener)
	}()
	conn, err := grpc.NewClient("passthrough:///billing-accounts-test", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
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

	run := func(format string, offset int32, response *publicv1.BillingAccountsListResponse) (string, string) {
		stdout := &bytes.Buffer{}
		stderr := &bytes.Buffer{}
		console := newConsole(stdout, stderr)
		Expect(render(ctx, console, format, offset, response)).To(Succeed())
		return stdout.String(), stderr.String()
	}

	listWith := func(items ...*publicv1.BillingAccount) *publicv1.BillingAccountsListResponse {
		response := &publicv1.BillingAccountsListResponse{}
		response.SetSize(int32(len(items)))
		response.SetTotal(int32(len(items)))
		response.SetItems(items)
		return response
	}

	pageWith := func(total int32, items ...*publicv1.BillingAccount) *publicv1.BillingAccountsListResponse {
		return publicv1.BillingAccountsListResponse_builder{
			Items: items, Size: int32(len(items)), Total: total,
		}.Build()
	}

	active := publicv1.BillingAccount_builder{
		Id: "ba-1", ProviderKey: "m360", DisplayName: "Acme production", BaseCurrency: "USD",
		State:            publicv1.BillingAccountState_BILLING_ACCOUNT_STATE_ACTIVE,
		StatusObservedAt: timestamppb.New(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)),
		CorrelationId:    "acct-corr-1001",
	}.Build()

	unknownNoCurrency := publicv1.BillingAccount_builder{
		Id: "ba-2", ProviderKey: "m360", DisplayName: "Acme staging",
		State: publicv1.BillingAccountState_BILLING_ACCOUNT_STATE_UNKNOWN,
	}.Build()

	It("shows an empty currency and an unknown state explicitly, never blank", func() {
		output, stderr := run("table", 0, listWith(unknownNoCurrency))
		Expect(output).To(MatchRegexp(`ba-2\s+m360\s+Acme staging\s+-\s+UNKNOWN\s+-\s+-`))
		Expect(stderr).To(BeEmpty())
	})

	It("renders an active account's fields without an enum prefix", func() {
		output, stderr := run("table", 0, listWith(active))
		Expect(output).To(ContainSubstring("USD"))
		Expect(output).To(MatchRegexp(`\bACTIVE\b`))
		Expect(output).ToNot(ContainSubstring("BILLING_ACCOUNT_STATE_"))
		Expect(output).To(ContainSubstring("acct-corr-1001"))
		Expect(stderr).To(BeEmpty())
	})

	It("strips control characters from provider-supplied cells in table mode", func() {
		dirty := publicv1.BillingAccount_builder{
			Id: "ba-3", ProviderKey: "m360", DisplayName: "Acme\x1b]52;x;wg\x07",
			State:         publicv1.BillingAccountState_BILLING_ACCOUNT_STATE_ACTIVE,
			CorrelationId: "corr\t\n1001",
		}.Build()
		output, stderr := run("table", 0, listWith(dirty))
		Expect(output).ToNot(ContainSubstring("\x1b"), "a terminal escape must not reach the console")
		Expect(output).ToNot(ContainSubstring("\x07"))
		Expect(output).To(ContainSubstring("Acme]52;x;wg"))
		Expect(output).To(ContainSubstring("corr1001"))
		Expect(stderr).To(BeEmpty())
	})

	It("says so when there are no accounts, in table mode", func() {
		output, stderr := run("table", 0, listWith())
		Expect(output).To(ContainSubstring("No billing accounts found"))
		Expect(stderr).To(BeEmpty())
	})

	It("hints the next page on standard error, leaving the table alone on standard output", func() {
		stdout, stderr := run("table", 0, pageWith(5, active, unknownNoCurrency))
		Expect(stdout).To(ContainSubstring("ba-1"))
		Expect(stdout).ToNot(ContainSubstring("Showing"))
		Expect(stderr).To(Equal("Showing 1-2 of 5 billing accounts. Next page: --offset 2\n"))
	})

	It("keeps standard error empty when the page holds every account", func() {
		stdout, stderr := run("table", 100, pageWith(2, active, unknownNoCurrency))
		Expect(stdout).To(ContainSubstring("ba-1"))
		Expect(stderr).To(BeEmpty())
	})

	It("does not claim an empty collection for an empty page at or beyond the end", func() {
		stdout, stderr := run("table", 5, pageWith(5))
		Expect(stdout).To(BeEmpty())
		Expect(stderr).To(Equal("No billing accounts on this page: offset 5 is at or beyond the total 5.\n"))
	})

	It("renders JSON with API field names, omitting empty base_currency and correlation_id", func() {
		output, stderr := run("json", 0, listWith(unknownNoCurrency))
		Expect(output).To(MatchJSON(`{
			"size": 1,
			"total": 1,
			"items": [{
				"id": "ba-2",
				"provider_key": "m360",
				"display_name": "Acme staging",
				"state": "BILLING_ACCOUNT_STATE_UNKNOWN"
			}]
		}`))
		Expect(stderr).To(BeEmpty())
	})

	It("renders YAML with API field names", func() {
		output, stderr := run("yaml", 0, listWith(active))
		Expect(output).To(ContainSubstring("base_currency: USD"))
		Expect(output).To(ContainSubstring("correlation_id: acct-corr-1001"))
		Expect(stderr).To(BeEmpty())
	})
})

var _ = Describe("render failures", func() {
	sentinel := errors.New("broken stream")

	renderErr := func(format string, offset int32, response *publicv1.BillingAccountsListResponse,
		stdout, stderr io.Writer) error {
		console, err := terminal.NewConsole().SetLogger(logger).SetStdout(stdout).SetStderr(stderr).Build()
		Expect(err).ToNot(HaveOccurred())
		return render(ctx, console, format, offset, response)
	}

	pageWith := func(total int32, items ...*publicv1.BillingAccount) *publicv1.BillingAccountsListResponse {
		return publicv1.BillingAccountsListResponse_builder{
			Items: items, Size: int32(len(items)), Total: total,
		}.Build()
	}

	active := publicv1.BillingAccount_builder{
		Id: "ba-1", ProviderKey: "m360", DisplayName: "Acme production", BaseCurrency: "USD",
		State: publicv1.BillingAccountState_BILLING_ACCOUNT_STATE_ACTIVE,
	}.Build()

	It("reports a failed empty-collection message write", func() {
		err := renderErr("table", 0, pageWith(0), &failingWriter{err: sentinel}, &bytes.Buffer{})
		Expect(err).To(MatchError(ContainSubstring("failed to write info output")))
		Expect(errors.Is(err, sentinel)).To(BeTrue())
	})

	It("reports a failed table flush wrapping the writer error", func() {
		err := renderErr("table", 0, pageWith(1, active), &failingWriter{err: sentinel}, &bytes.Buffer{})
		Expect(err).To(MatchError(ContainSubstring("failed to flush billing accounts table")))
		Expect(errors.Is(err, sentinel)).To(BeTrue())
	})

	It("reports a failed JSON write", func() {
		err := renderErr("json", 0, pageWith(1, active), &failingWriter{err: sentinel}, &bytes.Buffer{})
		Expect(err).To(MatchError(ContainSubstring("failed to render billing accounts")))
		Expect(errors.Is(err, sentinel)).To(BeTrue())
	})

	It("reports a failed YAML write", func() {
		err := renderErr("yaml", 0, pageWith(1, active), &failingWriter{err: sentinel}, &bytes.Buffer{})
		Expect(err).To(MatchError(ContainSubstring("failed to render billing accounts")))
		Expect(errors.Is(err, sentinel)).To(BeTrue())
	})

	It("reports a failed page notice write", func() {
		err := renderErr("table", 0, pageWith(5, active), &bytes.Buffer{}, &failingWriter{err: sentinel})
		Expect(err).To(MatchError(ContainSubstring("failed to write the billing accounts page notice")))
		Expect(errors.Is(err, sentinel)).To(BeTrue())
	})

	It("does not report a failure when the table and the page notice both succeed", func() {
		err := renderErr("table", 0, pageWith(5, active), &bytes.Buffer{}, &bytes.Buffer{})
		Expect(err).ToNot(HaveOccurred())
	})
})

var _ = Describe("listBillingAccounts", func() {
	var (
		server *billingAccountsServer
		conn   *grpc.ClientConn
	)

	BeforeEach(func() {
		server = &billingAccountsServer{
			response: publicv1.BillingAccountsListResponse_builder{}.Build(),
		}
		conn = billingAccountsConnection(server)
	})

	It("sends the exact bounds to the server", func() {
		_, err := listBillingAccounts(ctx, publicv1.NewBillingAccountsClient(conn), 7, 42)
		Expect(err).NotTo(HaveOccurred())
		Expect(server.gotLimit).To(Equal(int32(7)))
		Expect(server.gotOffset).To(Equal(int32(42)))
		Expect(server.sawLimit).To(BeTrue())
		Expect(server.sawOffset).To(BeTrue())
	})

	It("sends the bounds as explicit fields even when they are zero", func() {
		_, err := listBillingAccounts(ctx, publicv1.NewBillingAccountsClient(conn), 0, 0)
		Expect(err).NotTo(HaveOccurred())
		Expect(server.gotLimit).To(Equal(int32(0)))
		Expect(server.gotOffset).To(Equal(int32(0)))
		Expect(server.sawLimit).To(BeTrue())
		Expect(server.sawOffset).To(BeTrue())
	})

	It("wraps RPC errors", func() {
		server.listErr = status.Error(codes.PermissionDenied, "list blocked")
		_, err := listBillingAccounts(ctx, publicv1.NewBillingAccountsClient(conn), 0, 0)
		Expect(err).To(MatchError(ContainSubstring("failed to list billing accounts")))
		Expect(err).To(MatchError(ContainSubstring("list blocked")))
	})

	It("wraps a canceled context", func() {
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		_, err := listBillingAccounts(canceled, publicv1.NewBillingAccountsClient(conn), 0, 0)
		Expect(err).To(MatchError(ContainSubstring("failed to list billing accounts")))
		Expect(status.Code(err)).To(Equal(codes.Canceled))
	})
})

var _ = Describe("billingaccount flags", func() {
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
