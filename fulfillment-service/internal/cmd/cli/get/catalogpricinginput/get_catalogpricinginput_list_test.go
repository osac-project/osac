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
	. "github.com/onsi/gomega"

	"github.com/osac-project/osac/fulfillment-service/internal/config"
	"github.com/osac-project/osac/fulfillment-service/internal/logging"
	"github.com/osac-project/osac/fulfillment-service/internal/terminal"
	"github.com/osac-project/osac/fulfillment-service/internal/testing"
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
		server := testing.NewServer()
		DeferCleanup(server.Stop)
		listServer := &catalogPricingInputsListServer{items: items}
		publicv1.RegisterCatalogPricingInputsServer(server.Registrar(), listServer)
		server.Start()
		commandLogger := slog.New(slog.NewTextHandler(io.Discard, nil))
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

	It("preserves the count-only response of an explicit zero limit", func() {
		listServer, output, err := runCommand(inventoryItems(3), "--limit", "0")
		Expect(err).NotTo(HaveOccurred())
		Expect(listServer.requests).To(HaveLen(1))
		Expect(listServer.requests[0].GetLimit()).To(Equal(int32(0)))
		Expect(output).ToNot(ContainSubstring("type-"))
	})

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
