/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package catalogpricinginput

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/ginkgo/v2/dsl/table"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc"

	"github.com/osac-project/osac/fulfillment-service/internal/config"
	"github.com/osac-project/osac/fulfillment-service/internal/logging"
	"github.com/osac-project/osac/fulfillment-service/internal/terminal"
	"github.com/osac-project/osac/fulfillment-service/internal/testing"
	"github.com/osac-project/osac/fulfillment-service/internal/validation"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

// catalogPricingInputsListServer emulates the documented server List contract: an omitted limit returns a page of
// the default size (100), an explicit limit is honored verbatim with zero meaning count-only, and a negative offset
// is clamped to zero.
type catalogPricingInputsListServer struct {
	publicv1.UnimplementedCatalogPricingInputsServer
	requests []*publicv1.CatalogPricingInputsListRequest
	items    []*publicv1.CatalogPricingInput
}

func (s *catalogPricingInputsListServer) List(_ context.Context,
	request *publicv1.CatalogPricingInputsListRequest) (*publicv1.CatalogPricingInputsListResponse, error) {
	s.requests = append(s.requests, request)
	offset := max(request.GetOffset(), 0)
	limit := int32(100)
	if request.HasLimit() {
		limit = request.GetLimit()
	}
	total := int32(len(s.items))
	var page []*publicv1.CatalogPricingInput
	if limit != 0 && offset < total {
		end := total
		if offset+limit < end {
			end = offset + limit
		}
		page = s.items[offset:end]
	}
	return publicv1.CatalogPricingInputsListResponse_builder{
		Size:  int32(len(page)),
		Total: total,
		Items: page,
	}.Build(), nil
}

// inventoryItems builds count rows with recognizable values, more than one server page when count exceeds 100.
func inventoryItems(count int) []*publicv1.CatalogPricingInput {
	items := make([]*publicv1.CatalogPricingInput, 0, count)
	for i := range count {
		items = append(items, publicv1.CatalogPricingInput_builder{
			Service:   "compute_instance",
			Dimension: "instance_type",
			Value:     fmt.Sprintf("type-%03d", i),
		}.Build())
	}
	return items
}

var _ = Describe("list", func() {
	// runCommand starts a fake server holding the given items, runs the command with the given arguments and
	// returns the fake server, the rendered output and the command error.
	runCommand := func(items []*publicv1.CatalogPricingInput, args ...string) (*catalogPricingInputsListServer, string, error) {
		commandLogger := slog.New(slog.NewTextHandler(io.Discard, nil))
		interceptor, err := validation.NewProtovalidateInterceptor().SetLogger(commandLogger).Build()
		Expect(err).NotTo(HaveOccurred())
		server := testing.NewServer(grpc.UnaryInterceptor(interceptor.UnaryServer))
		DeferCleanup(server.Stop)
		listServer := &catalogPricingInputsListServer{items: items}
		publicv1.RegisterCatalogPricingInputsServer(server.Registrar(), listServer)
		server.Start()
		settings, err := config.NewSettings().SetLogger(commandLogger).SetDir(GinkgoT().TempDir()).Build()
		Expect(err).NotTo(HaveOccurred())
		settings.SetAddress(server.Address())
		settings.SetPlaintext(true)
		stdout := &bytes.Buffer{}
		console, err := terminal.NewConsole().SetLogger(commandLogger).SetStdout(stdout).SetStderr(io.Discard).Build()
		Expect(err).NotTo(HaveOccurred())
		commandCtx := logging.LoggerIntoContext(context.Background(), commandLogger)
		commandCtx = config.SettingsIntoContext(commandCtx, settings)
		commandCtx = terminal.ConsoleIntoContext(commandCtx, console)
		cmd := Cmd()
		cmd.SetContext(commandCtx)
		cmd.SetOut(io.Discard)
		cmd.SetErr(io.Discard)
		// With no CLI flags the variadic args are nil and cobra then falls back to os.Args, which under the
		// ginkgo runner carries --test.* flags the command rejects; pass an explicit empty argument list instead.
		if args == nil {
			args = []string{}
		}
		cmd.SetArgs(args)
		err = cmd.Execute()
		return listServer, stdout.String(), err
	}

	It("fetches every page when no limit is given so more than one page is never truncated", func() {
		listServer, output, err := runCommand(inventoryItems(250))
		Expect(err).NotTo(HaveOccurred())
		Expect(listServer.requests).To(HaveLen(3))
		offsets := make([]int32, 0, len(listServer.requests))
		for _, request := range listServer.requests {
			Expect(request.HasLimit()).To(BeFalse(), "no limit must be sent so the server applies its default page size")
			offsets = append(offsets, request.GetOffset())
		}
		Expect(offsets).To(Equal([]int32{0, 100, 200}))
		Expect(output).To(ContainSubstring("type-000"))
		Expect(output).To(ContainSubstring("type-249"))
	})

	It("sends an explicit limit and offset once", func() {
		listServer, output, err := runCommand(inventoryItems(250), "--limit", "5", "--offset", "10")
		Expect(err).NotTo(HaveOccurred())
		Expect(listServer.requests).To(HaveLen(1))
		Expect(listServer.requests[0].GetLimit()).To(Equal(int32(5)))
		Expect(listServer.requests[0].GetOffset()).To(Equal(int32(10)))
		Expect(output).To(ContainSubstring("type-010"))
		Expect(output).To(ContainSubstring("type-014"))
		Expect(output).ToNot(ContainSubstring("type-015"))
	})

	DescribeTable("prints only the API total for an explicit zero limit",
		func(format string, total int, expected string) {
			listServer, output, err := runCommand(inventoryItems(total), "--limit", "0", "--output", format)
			Expect(err).NotTo(HaveOccurred())
			Expect(listServer.requests).To(HaveLen(1))
			Expect(listServer.requests[0].HasLimit()).To(BeTrue())
			Expect(listServer.requests[0].GetLimit()).To(Equal(int32(0)))
			// Exact output also rules out item rows, item arrays and ordinary list headers.
			Expect(output).To(Equal(expected))
		},
		Entry("table", "table", 42, "TOTAL\n42\n"),
		Entry("JSON", "json", 42, "{\n  \"total\": 42\n}\n"),
		Entry("YAML", "yaml", 42, "total: 42\n"),
		Entry("table, zero total", "table", 0, "TOTAL\n0\n"),
		Entry("JSON, zero total", "json", 0, "{\n  \"total\": 0\n}\n"),
		Entry("YAML, zero total", "yaml", 0, "total: 0\n"),
	)

	DescribeTable("keeps ordinary list output byte-compatible with the item renderer",
		func(format string) {
			for _, count := range []int{0, 3} {
				for _, explicitLimit := range []bool{false, true} {
					items := inventoryItems(count)
					args := []string{"--output", format}
					if explicitLimit {
						args = append(args, "--limit", "1")
					}
					_, output, err := runCommand(items, args...)
					Expect(err).NotTo(HaveOccurred())
					if explicitLimit && count > 0 {
						items = items[:1]
					}
					expected := &bytes.Buffer{}
					console, err := terminal.NewConsole().SetLogger(logger).SetStdout(expected).Build()
					Expect(err).NotTo(HaveOccurred())
					Expect(render(ctx, console, format, items)).To(Succeed())
					Expect(output).To(Equal(expected.String()))
				}
			}
		},
		Entry("table", "table"),
		Entry("JSON", "json"),
		Entry("YAML", "yaml"),
	)

	It("forwards the filter and order", func() {
		listServer, _, err := runCommand(inventoryItems(3), "--filter", "this.service == 'cluster'", "--order", "service")
		Expect(err).NotTo(HaveOccurred())
		Expect(listServer.requests).To(HaveLen(1))
		Expect(listServer.requests[0].GetFilter()).To(Equal("this.service == 'cluster'"))
		Expect(listServer.requests[0].GetOrder()).To(Equal("service"))
	})

	It("keeps the filter on every page when paging", func() {
		listServer, _, err := runCommand(inventoryItems(150), "--filter", "this.service == 'compute_instance'")
		Expect(err).NotTo(HaveOccurred())
		Expect(listServer.requests).To(HaveLen(2))
		for _, request := range listServer.requests {
			Expect(request.GetFilter()).To(Equal("this.service == 'compute_instance'"))
		}
	})
})
