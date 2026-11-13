/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package providerratecarditem

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

// providerRateCardItemsListServer emulates the documented server List contract: an omitted limit returns a page of
// the default size (100), an explicit limit is honored verbatim with zero meaning count-only, and a negative offset
// is clamped to zero.
type providerRateCardItemsListServer struct {
	publicv1.UnimplementedProviderRateCardItemsServer
	requests []*publicv1.ProviderRateCardItemsListRequest
	items    []*publicv1.ProviderRateCardItem
}

func (s *providerRateCardItemsListServer) List(_ context.Context,
	request *publicv1.ProviderRateCardItemsListRequest) (*publicv1.ProviderRateCardItemsListResponse, error) {
	s.requests = append(s.requests, request)
	offset := max(request.GetOffset(), 0)
	limit := int32(100)
	if request.HasLimit() {
		limit = request.GetLimit()
	}
	total := int32(len(s.items))
	var page []*publicv1.ProviderRateCardItem
	if limit != 0 && offset < total {
		end := total
		if offset+limit < end {
			end = offset + limit
		}
		page = s.items[offset:end]
	}
	return publicv1.ProviderRateCardItemsListResponse_builder{
		Size:  int32(len(page)),
		Total: total,
		Items: page,
	}.Build(), nil
}

// rateCardItems builds count rows with recognizable conditions, more than one server page when count exceeds 100.
func rateCardItems(count int) []*publicv1.ProviderRateCardItem {
	items := make([]*publicv1.ProviderRateCardItem, 0, count)
	for i := range count {
		items = append(items, publicv1.ProviderRateCardItem_builder{
			Service:      "compute_instance",
			CurrencyCode: "USD",
			Dimension:    "compute_rate",
			ResourceConditions: []*publicv1.ProviderRateCardItemResourceCondition{
				publicv1.ProviderRateCardItemResourceCondition_builder{
					Dimension: "instance_type",
					Operator:  publicv1.BillingRateMatchOperator_BILLING_RATE_MATCH_OPERATOR_EQUALS,
					Value:     fmt.Sprintf("type-%03d", i),
				}.Build(),
			},
			Rate: publicv1.ProviderRateCardItemRate_builder{Amount: "0.00125", Unit: "second"}.Build(),
		}.Build())
	}
	return items
}

var _ = Describe("list", func() {
	// runCommand starts a fake server holding the given items, runs the command with the given arguments and
	// returns the fake server, the rendered output and the command error.
	runCommand := func(items []*publicv1.ProviderRateCardItem, args ...string) (*providerRateCardItemsListServer, string, error) {
		server := testing.NewServer()
		DeferCleanup(server.Stop)
		listServer := &providerRateCardItemsListServer{items: items}
		publicv1.RegisterProviderRateCardItemsServer(server.Registrar(), listServer)
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
		listServer, output, err := runCommand(rateCardItems(150), "--currency-code", "USD")
		Expect(err).NotTo(HaveOccurred())
		Expect(listServer.requests).To(HaveLen(2))
		offsets := make([]int32, 0, len(listServer.requests))
		for _, request := range listServer.requests {
			Expect(request.HasLimit()).To(BeFalse(), "no limit must be sent so the server applies its default page size")
			offsets = append(offsets, request.GetOffset())
		}
		Expect(offsets).To(Equal([]int32{0, 100}))
		Expect(output).To(ContainSubstring("type-000"))
		Expect(output).To(ContainSubstring("type-149"))
	})

	It("requires the currency code", func() {
		listServer, _, err := runCommand(rateCardItems(3))
		Expect(err).To(MatchError(ContainSubstring("required flag(s)")))
		Expect(listServer.requests).To(BeEmpty())
	})

	It("rejects a currency code that is not three letters", func() {
		listServer, _, err := runCommand(rateCardItems(3), "--currency-code", "US")
		Expect(err).To(MatchError(ContainSubstring("3-letter ISO-4217")))
		Expect(listServer.requests).To(BeEmpty())
	})

	It("forwards the service, currency, tenant, filter and order", func() {
		listServer, _, err := runCommand(rateCardItems(3),
			"--currency-code", "EUR",
			"--service", "cluster",
			"--tenant-id", "tenant-a",
			"--filter", "this.service == 'cluster'",
			"--order", "service")
		Expect(err).NotTo(HaveOccurred())
		Expect(listServer.requests).To(HaveLen(1))
		request := listServer.requests[0]
		Expect(request.GetCurrencyCode()).To(Equal("EUR"))
		Expect(request.GetService()).To(Equal("cluster"))
		Expect(request.GetTenantId()).To(Equal("tenant-a"))
		Expect(request.GetFilter()).To(Equal("this.service == 'cluster'"))
		Expect(request.GetOrder()).To(Equal("service"))
	})

	It("omits the optional filters instead of sending them empty", func() {
		listServer, _, err := runCommand(rateCardItems(3), "--currency-code", "USD")
		Expect(err).NotTo(HaveOccurred())
		Expect(listServer.requests).To(HaveLen(1))
		request := listServer.requests[0]
		Expect(request.GetCurrencyCode()).To(Equal("USD"))
		Expect(request.GetTenantId()).To(BeEmpty())
		Expect(request.GetService()).To(BeEmpty())
		Expect(request.GetFilter()).To(BeEmpty())
		Expect(request.GetOrder()).To(BeEmpty())
	})

	It("sends an explicit limit and offset once", func() {
		listServer, output, err := runCommand(rateCardItems(150),
			"--currency-code", "USD", "--limit", "5", "--offset", "10")
		Expect(err).NotTo(HaveOccurred())
		Expect(listServer.requests).To(HaveLen(1))
		Expect(listServer.requests[0].GetLimit()).To(Equal(int32(5)))
		Expect(listServer.requests[0].GetOffset()).To(Equal(int32(10)))
		Expect(output).To(ContainSubstring("type-010"))
		Expect(output).To(ContainSubstring("type-014"))
		Expect(output).ToNot(ContainSubstring("type-015"))
	})

	It("preserves the count-only response of an explicit zero limit", func() {
		listServer, output, err := runCommand(rateCardItems(3), "--currency-code", "USD", "--limit", "0")
		Expect(err).NotTo(HaveOccurred())
		Expect(listServer.requests).To(HaveLen(1))
		Expect(listServer.requests[0].GetLimit()).To(Equal(int32(0)))
		Expect(output).ToNot(ContainSubstring("type-"))
	})

	It("keeps the service, currency and tenant filters on every page when paging", func() {
		listServer, _, err := runCommand(rateCardItems(150),
			"--currency-code", "USD", "--service", "compute_instance", "--tenant-id", "tenant-a")
		Expect(err).NotTo(HaveOccurred())
		Expect(listServer.requests).To(HaveLen(2))
		for _, request := range listServer.requests {
			Expect(request.GetCurrencyCode()).To(Equal("USD"))
			Expect(request.GetService()).To(Equal("compute_instance"))
			Expect(request.GetTenantId()).To(Equal("tenant-a"))
		}
	})
})
