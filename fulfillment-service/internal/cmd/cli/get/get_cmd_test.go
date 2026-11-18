/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package get

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/ginkgo/v2/dsl/table"
	. "github.com/onsi/gomega"
	"github.com/spf13/cobra"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/dynamicpb"
	"gopkg.in/yaml.v3"

	"github.com/osac-project/osac/fulfillment-service/internal/config"
	"github.com/osac-project/osac/fulfillment-service/internal/logging"
	"github.com/osac-project/osac/fulfillment-service/internal/packages"
	"github.com/osac-project/osac/fulfillment-service/internal/reflection"
	"github.com/osac-project/osac/fulfillment-service/internal/terminal"
	"github.com/osac-project/osac/fulfillment-service/internal/testing"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
	"google.golang.org/protobuf/encoding/protojson"
)

// Exercises the real reflection/Cobra-facing fetch path with a
// recording client interceptor. No server, generated mock, or provider is needed.
func newInterceptedGetRunner(invoke func(context.Context, string, proto.Message, proto.Message) error) *runnerContext {
	connection, err := grpc.NewClient("passthrough:///p2-unit.invalid",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithUnaryInterceptor(func(ctx context.Context, method string, request, response any,
			_ *grpc.ClientConn, _ grpc.UnaryInvoker, _ ...grpc.CallOption) error {
			return invoke(ctx, method, request.(proto.Message), response.(proto.Message))
		}))
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(connection.Close)
	helper, err := reflection.NewHelper().SetLogger(logger).SetConnection(connection).
		AddPackage(packages.PublicV1, 0).SetTenantFunc(config.TenantFromContext).
		SetProjectFunc(config.ProjectFromContext).UseGetForStructuredOutput(&publicv1.Secret{}).Build()
	Expect(err).NotTo(HaveOccurred())
	return &runnerContext{globalHelper: helper, objectHelper: helper.Lookup("cluster")}
}

func clusterPageItems(start, count int) []*publicv1.Cluster {
	items := make([]*publicv1.Cluster, count)
	for i := range items {
		items[i] = &publicv1.Cluster{Id: fmt.Sprintf("p2-cluster-%04d", start+i)}
	}
	return items
}

// Run the actual Cmd/runner.run/console path against the native local gRPC test
// server. Dynamic wire messages avoid generated server doubles for singleton
// services. Removing child registrations selects generic dispatch only;
// unrelated child commands remain registered in production.
func executeGenericGet(private bool, invoke func(string, proto.Message, proto.Message) error, args ...string) (string, error) {
	server := testing.NewServer(grpc.UnknownServiceHandler(func(_ any, stream grpc.ServerStream) error {
		defer GinkgoRecover()
		path, ok := grpc.MethodFromServerStream(stream)
		Expect(ok).To(BeTrue())
		name := strings.ReplaceAll(strings.TrimPrefix(path, "/"), "/", ".")
		descriptor, err := protoregistry.GlobalFiles.FindDescriptorByName(protoreflect.FullName(name))
		Expect(err).NotTo(HaveOccurred())
		method := descriptor.(protoreflect.MethodDescriptor)
		request, response := dynamicpb.NewMessage(method.Input()), dynamicpb.NewMessage(method.Output())
		if err := stream.RecvMsg(request); err != nil {
			return err
		}
		if err := invoke(path, request, response); err != nil {
			return err
		}
		return stream.SendMsg(response)
	}))
	DeferCleanup(server.Stop)
	server.Start()
	var stdout bytes.Buffer
	console, err := terminal.NewConsole().SetLogger(logger).SetStdout(&stdout).SetStderr(&bytes.Buffer{}).Build()
	Expect(err).NotTo(HaveOccurred())
	settings, err := config.NewSettings().SetLogger(logger).SetDir("/tmp/opencode").Build()
	Expect(err).NotTo(HaveOccurred())
	settings.SetAddress(server.Address())
	settings.SetPlaintext(true)
	settings.SetPrivate(private)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	ctx = config.ProjectIntoContext(config.TenantIntoContext(ctx, "p2-tenant"), "p2-project")
	ctx = logging.LoggerIntoContext(terminal.ConsoleIntoContext(config.SettingsIntoContext(ctx, settings), console), logger)
	command := Cmd()
	command.RemoveCommand(command.Commands()...)
	root := &cobra.Command{Use: "osac", SilenceErrors: true, SilenceUsage: true}
	root.AddCommand(command)
	root.SetOut(&stdout)
	root.SetErr(&bytes.Buffer{})
	root.SetArgs(append([]string{"get"}, args...))
	err = root.ExecuteContext(ctx)
	return stdout.String(), err
}

// Synthetic provider text is deliberately not normalized by generic structured
// output. Terminal table sanitization has separate renderer coverage.
const structuredAttributionToken = "  p6-token\t\n\x1b\"quoted\"  "

func genericBillingItem(private bool, kind string, index int) map[string]any {
	metadata := map[string]any{
		"name": fmt.Sprintf("billing-name-%d", index), "tenant": "p2-tenant",
		"labels": map[string]any{"env": "test"},
	}
	if private {
		metadata["finalizers"] = []any{"p6-test-finalizer"}
	}
	item := map[string]any{"id": fmt.Sprintf("billing-id-%d", index), "metadata": metadata}
	if kind == "billingaccount" {
		metadata["tenant"] = "system"
		item["spec"] = map[string]any{}
		status := map[string]any{
			"provider_key": "m360", "display_name": "Provider display label", "base_currency": "USD",
			"state": "BILLING_ACCOUNT_STATE_ACTIVE", "state_observation_timestamp": "2026-01-01T00:00:00Z",
			"external_billing_account_id": structuredAttributionToken,
		}
		if private {
			status["provider_account_reference"] = "provider-native-reference"
		}
		item["status"] = status
	} else {
		item["spec"] = map[string]any{
			"tenant":          map[string]any{"id": "p2-tenant", "name": "customer"},
			"billing_account": map[string]any{"id": "account-id", "name": "ba-stable-name"},
		}
		status := map[string]any{
			"connection_state":         "TENANT_BILLING_ASSIGNMENT_CONNECTION_STATE_PENDING",
			"effective_from_timestamp": "2026-01-01T00:00:00Z",
			"message":                  "BILLING_PROVIDER_CORRELATION_WRITE_UNSUPPORTED: manual remediation required",
		}
		if index > 0 {
			status["effective_to_timestamp"] = "2026-02-01T00:00:00Z"
		}
		item["status"] = status
	}
	return item
}

func populateGenericBillingResponse(response proto.Message, private bool, kind string, start, count, total int) error {
	items := make([]any, count)
	for i := range items {
		items[i] = genericBillingItem(private, kind, start+i)
	}
	data, err := json.Marshal(map[string]any{"items": items, "size": count, "total": total})
	if err != nil {
		return err
	}
	return protojson.Unmarshal(data, response)
}

var _ = Describe("Get command", func() {
	var (
		ctx        context.Context
		ctrl       *gomock.Controller
		mockHelper *reflection.MockObjectHelper
	)

	BeforeEach(func() {
		ctx = context.Background()
		ctrl = gomock.NewController(GinkgoT())
		DeferCleanup(ctrl.Finish)
		mockHelper = reflection.NewMockObjectHelper(ctrl)
		mockHelper.EXPECT().IsSingleton().Return(false).AnyTimes()
	})

	Describe("generic billing command", func() {
		for _, private := range []bool{false, true} {
			pkg := packages.PublicV1
			if private {
				pkg = packages.PrivateV1
			}
			for _, kind := range []string{"billingaccount", "tenantbillingassignment"} {
				service, objectName := "BillingAccounts", "BillingAccount"
				if kind == "tenantbillingassignment" {
					service, objectName = "TenantBillingAssignments", "TenantBillingAssignment"
				}
				for _, format := range []string{outputFormatJson, outputFormatYaml} {
					DescribeTable(pkg+" "+kind+" "+format+" uses List-only Any item cardinality and descriptor visibility",
						func(count int) {
							calls := 0
							stdout, err := executeGenericGet(private, func(method string, request, response proto.Message) error {
								calls++
								Expect(method).To(Equal("/"+pkg+"."+service+"/List"), "keyed billing must not invoke the account Get scaffold")
								message := request.ProtoReflect()
								Expect(message.Get(message.Descriptor().Fields().ByName("offset")).Int()).To(BeZero())
								return populateGenericBillingResponse(response, private, kind, 0, count, count)
							}, kind, "billing-id-0", `second"key`, "--output="+format)
							Expect(err).NotTo(HaveOccurred())
							Expect(calls).To(Equal(1))
							var value any
							if format == outputFormatJson {
								Expect(json.Unmarshal([]byte(stdout), &value)).To(Succeed())
							} else {
								Expect(yaml.Unmarshal([]byte(stdout), &value)).To(Succeed())
							}
							var values []any
							if count == 1 {
								values = []any{value}
							} else {
								var ok bool
								values, ok = value.([]any)
								Expect(ok).To(BeTrue(), "zero/many must be sequences, never size/total/items envelopes")
							}
							Expect(values).To(HaveLen(count))
							for i, value := range values {
								expected := genericBillingItem(private, kind, i)
								expected["@type"] = "type.googleapis.com/" + pkg + "." + objectName
								Expect(value).To(Equal(expected), "private descriptors retain their fields; public descriptors omit native identity and finalizers")
							}
						},
						Entry("zero", 0), Entry("one", 1), Entry("many", 2),
					)
				}

				DescribeTable(pkg+" "+kind+" reaches domain tables and the generic empty message",
					func(count int) {
						calls := 0
						stdout, err := executeGenericGet(private, func(method string, _, response proto.Message) error {
							calls++
							Expect(method).To(Equal("/" + pkg + "." + service + "/List"))
							return populateGenericBillingResponse(response, private, kind, 0, count, count)
						}, kind)
						Expect(err).NotTo(HaveOccurred())
						Expect(calls).To(Equal(1))
						if count == 0 {
							Expect(stdout).To(ContainSubstring("There are no objects matching the given criteria."))
							return
						}
						Expect(stdout).To(ContainSubstring("billing-id-0"))
						Expect(stdout).To(ContainSubstring("billing-name-0"))
						Expect(stdout).To(MatchRegexp(`\bNAME\b`))
						Expect(stdout).NotTo(ContainSubstring("provider-native-reference"))
						if kind == "billingaccount" {
							Expect(stdout).NotTo(MatchRegexp(`\bTENANT\b`))
							Expect(stdout).To(ContainSubstring("ACTIVE"))
						} else {
							Expect(stdout).To(MatchRegexp(`\bTENANT\b`))
							Expect(stdout).To(ContainSubstring("PENDING"))
							Expect(stdout).To(ContainSubstring("manual remediation required"))
						}
					},
					Entry("empty", 0), Entry("one", 1),
				)

				DescribeTable(pkg+" "+kind+" carries keys and domain CEL unchanged through every page",
					func(history bool) {
						filter := `this.status.state == 1 && this.status.state_observation_timestamp >= timestamp("2026-01-01T00:00:00Z") && this.metadata.labels["env"] == "test"`
						if kind == "tenantbillingassignment" {
							filter = `this.status.connection_state == 1 && this.status.effective_from_timestamp >= timestamp("2026-01-01T00:00:00Z") && this.metadata.labels["env"] == "test" && `
							if history {
								filter += `has(this.status.effective_to_timestamp)`
							} else {
								filter += `!has(this.status.effective_to_timestamp)`
							}
						}
						filter = "(" + filter + `) || this.metadata.tenant == "other-tenant"`
						expected := `(this.id in ["first", "two\"quoted"] || this.metadata.name in ["first", "two\"quoted"]) && (` + filter + `)`
						if kind == "tenantbillingassignment" {
							expected = `this.metadata.tenant == "p2-tenant" && (` + expected + `)`
						}
						calls := 0
						stdout, err := executeGenericGet(private, func(method string, request, response proto.Message) error {
							calls++
							Expect(calls).To(BeNumerically("<=", 2))
							Expect(method).To(Equal("/" + pkg + "." + service + "/List"))
							message := request.ProtoReflect()
							fields := message.Descriptor().Fields()
							Expect(message.Get(fields.ByName("filter")).String()).To(Equal(expected))
							Expect(message.Get(fields.ByName("limit")).Int()).To(Equal(int64(100)))
							Expect(message.Get(fields.ByName("offset")).Int()).To(Equal(int64(calls - 1)))
							return populateGenericBillingResponse(response, private, kind, calls-1, 1, 2)
						}, kind, "first", `two"quoted`, "--output=json", "--filter="+filter)
						Expect(err).NotTo(HaveOccurred())
						Expect(calls).To(Equal(2))
						var values []any
						Expect(json.Unmarshal([]byte(stdout), &values)).To(Succeed())
						Expect(values).To(HaveLen(2))
					},
					Entry("current", false), Entry("history", true),
				)

				for _, format := range []string{outputFormatTable, outputFormatJson, outputFormatYaml} {
					DescribeTable(pkg+" "+kind+" "+format+" buffers actual command stdout on later-page errors",
						func(rpcFailure bool) {
							calls := 0
							stdout, err := executeGenericGet(private, func(method string, request, response proto.Message) error {
								calls++
								Expect(calls).To(BeNumerically("<=", 2))
								Expect(method).To(Equal("/" + pkg + "." + service + "/List"))
								message := request.ProtoReflect()
								Expect(message.Get(message.Descriptor().Fields().ByName("offset")).Int()).To(Equal(int64(calls - 1)))
								if calls == 2 {
									if rpcFailure {
										return grpcstatus.Error(codes.Internal, "p6 billing second page failed")
									}
									return populateGenericBillingResponse(response, private, kind, 1, 0, 2)
								}
								return populateGenericBillingResponse(response, private, kind, 0, 1, 2)
							}, kind, "--output="+format)
							Expect(calls).To(Equal(2), "the later failure must actually be reached")
							Expect(stdout).To(BeEmpty(), "no buffered billing inventory may escape the real console path")
							if rpcFailure {
								Expect(err).To(MatchError(ContainSubstring("p6 billing second page failed")))
							} else {
								Expect(err).To(MatchError(ContainSubstring("no progress")))
							}
						},
						Entry("RPC failure", true), Entry("no progress", false),
					)
				}
			}
		}

		It("has no public Signal descriptor and preserves descriptor-specific field visibility", func() {
			_, err := protoregistry.GlobalFiles.FindDescriptorByName("osac.public.v1.BillingAccountSignal")
			Expect(err).To(HaveOccurred())
			for _, pkg := range []string{packages.PublicV1, packages.PrivateV1} {
				descriptor, err := protoregistry.GlobalFiles.FindDescriptorByName(protoreflect.FullName(pkg + ".BillingAccount"))
				Expect(err).NotTo(HaveOccurred())
				fields := descriptor.(protoreflect.MessageDescriptor).Fields()
				status := fields.ByName("status").Message().Fields()
				metadata := fields.ByName("metadata").Message().Fields()
				if pkg == packages.PublicV1 {
					Expect(status.ByName("provider_account_reference")).To(BeNil())
					Expect(metadata.ByName("finalizers")).To(BeNil())
				} else {
					Expect(status.ByName("provider_account_reference")).NotTo(BeNil())
					Expect(metadata.ByName("finalizers")).NotTo(BeNil())
				}
			}
		})
	})

	It("uses generic billing dispatch and retains unrelated registered children", func() {
		var names []string
		for _, child := range Cmd().Commands() {
			names = append(names, child.Name())
		}
		Expect(names).To(ConsistOf("externalippool", "storagetier", "token"))
	})

	Describe("complete bounded collection reads", func() {
		DescribeTable("returns every item with checked offsets, including short pages",
			func(total, pageSize int) {
				var requests []*publicv1.ClustersListRequest
				runner := newInterceptedGetRunner(func(_ context.Context, method string, request, response proto.Message) error {
					Expect(method).To(Equal("/osac.public.v1.Clusters/List"))
					req := request.(*publicv1.ClustersListRequest)
					requests = append(requests, proto.Clone(req).(*publicv1.ClustersListRequest))
					if len(requests) > 40 {
						return errors.New("test circuit breaker: pagination did not terminate")
					}
					start := int(req.GetOffset())
					Expect(start).To(BeNumerically(">=", 0))
					Expect(start).To(BeNumerically("<=", total))
					count := min(pageSize, total-start)
					proto.Merge(response, publicv1.ClustersListResponse_builder{
						Items: clusterPageItems(start, count), Total: int32(total),
					}.Build())
					return nil
				})
				runner.args.format = outputFormatTable
				items, err := runner.fetchObjects(ctx, nil)
				Expect(err).NotTo(HaveOccurred())
				Expect(items).To(HaveLen(total), "a successful read must not silently truncate the inventory")
				for i, item := range items {
					Expect(item.(*publicv1.Cluster).GetId()).To(Equal(fmt.Sprintf("p2-cluster-%04d", i)))
				}
				Expect(requests).To(HaveLen(max(1, (total+pageSize-1)/pageSize)))
				for i, request := range requests {
					Expect(request.GetOffset()).To(Equal(int32(i * pageSize)))
					Expect(request.GetLimit()).To(Equal(int32(100)))
				}
			},
			Entry("zero", 0, 100), Entry("one", 1, 100), Entry("100", 100, 100),
			Entry("101", 101, 100), Entry("1000", 1000, 100), Entry("1001", 1001, 100),
			Entry("1205", 1205, 100), Entry("short nonterminal pages", 101, 37),
		)

		It("preserves keys, explicit filter, tenant and project on every page", func() {
			var filters []string
			runner := newInterceptedGetRunner(func(_ context.Context, _ string, request, response proto.Message) error {
				req := request.(*publicv1.ClustersListRequest)
				filters = append(filters, req.GetFilter())
				if len(filters) > 3 {
					return errors.New("test circuit breaker")
				}
				start := int(req.GetOffset())
				proto.Merge(response, publicv1.ClustersListResponse_builder{
					Items: clusterPageItems(start, min(100, 101-start)), Total: 101,
				}.Build())
				return nil
			})
			runner.args.format = outputFormatTable
			runner.args.filter = `this.metadata.labels["environment"] == "test"`
			scoped := config.ProjectIntoContext(config.TenantIntoContext(ctx, "p2-tenant"), "p2-project")
			items, err := runner.fetchObjects(scoped, []string{"one", `two"quoted`})
			Expect(err).NotTo(HaveOccurred())
			Expect(items).To(HaveLen(101))
			Expect(filters).To(HaveLen(2))
			Expect(filters[0]).To(Equal(filters[1]))
			Expect(filters[0]).To(ContainSubstring(`this.metadata.tenant == "p2-tenant"`))
			Expect(filters[0]).To(ContainSubstring(`this.metadata.project == "p2-project"`))
			Expect(filters[0]).To(ContainSubstring(`(this.id in ["one", "two\"quoted"] || this.metadata.name in ["one", "two\"quoted"])`))
			Expect(filters[0]).To(ContainSubstring(`(this.metadata.labels["environment"] == "test")`))
		})

		DescribeTable("fails closed without returning buffered partial results",
			func(scenario string) {
				calls := 0
				runner := newInterceptedGetRunner(func(_ context.Context, _ string, request, response proto.Message) error {
					calls++
					if calls > 3 {
						return errors.New("test circuit breaker")
					}
					total := int32(101)
					items := clusterPageItems(0, 100)
					switch scenario {
					case "negative total":
						total = -1
					case "empty first page":
						items = nil
					case "oversized page":
						items = clusterPageItems(0, 101)
					case "duplicate ids":
						items, total = []*publicv1.Cluster{{Id: "same"}, {Id: "same"}}, 2
					case "more items than total":
						total = 1
					case "changing total":
						if calls == 2 {
							items, total = clusterPageItems(100, 1), 102
						}
					case "empty later page":
						if calls == 2 {
							items = nil
						}
					case "later RPC failure":
						if calls == 2 {
							return errors.New("p2 later page failed")
						}
					case "ignored offset":
						// Every response repeats the first page, despite offset > 0.
					}
					proto.Merge(response, publicv1.ClustersListResponse_builder{Items: items, Total: total}.Build())
					return nil
				})
				runner.args.format = outputFormatJson
				items, err := runner.fetchObjects(ctx, nil)
				Expect(err).To(HaveOccurred(), scenario)
				Expect(items).To(BeEmpty(), "failed traversal must discard all buffered objects")
				Expect(calls).To(BeNumerically("<=", 2), "the test circuit breaker must never be the error oracle")
			},
			Entry("negative total", "negative total"), Entry("empty first page", "empty first page"),
			Entry("oversized page", "oversized page"), Entry("duplicate ids", "duplicate ids"),
			Entry("more items than total", "more items than total"), Entry("changing total", "changing total"),
			Entry("empty later page", "empty later page"), Entry("later RPC failure", "later RPC failure"),
			Entry("ignored offset", "ignored offset"),
		)

		It("observes cancellation after the first page within one second and makes no further RPC", func() {
			canceled, cancel := context.WithCancel(ctx)
			DeferCleanup(cancel)
			calls := 0
			runner := newInterceptedGetRunner(func(_ context.Context, _ string, _, response proto.Message) error {
				calls++
				proto.Merge(response, publicv1.ClustersListResponse_builder{Items: clusterPageItems(0, 100), Total: 101}.Build())
				cancel()
				return nil
			})
			start := time.Now()
			items, err := runner.fetchObjects(canceled, nil)
			Expect(time.Since(start)).To(BeNumerically("<", time.Second))
			Expect(errors.Is(err, context.Canceled)).To(BeTrue())
			Expect(items).To(BeEmpty())
			Expect(calls).To(Equal(1))
		})
	})

	Describe("seam budgets and checked offset arithmetic", func() {
		DescribeTable("uses the actual traversal budgets with tiny progressing pages",
			func(rpcBudget, objectBudget, total, pageSize, expectedCalls int, failure string) {
				calls := 0
				runner := newInterceptedGetRunner(func(_ context.Context, _ string, request, response proto.Message) error {
					calls++
					if calls > 4 {
						return errors.New("test circuit breaker, not the budget oracle")
					}
					start := int(request.(*publicv1.ClustersListRequest).GetOffset())
					proto.Merge(response, &publicv1.ClustersListResponse{Items: clusterPageItems(start, min(pageSize, total-start)), Total: int32(total)})
					return nil
				})
				runner.listRPCBudget, runner.listObjectBudget = rpcBudget, objectBudget
				runner.args.format = outputFormatTable
				items, err := runner.fetchObjects(ctx, nil)
				Expect(calls).To(Equal(expectedCalls))
				if failure != "" {
					Expect(err).To(MatchError(ContainSubstring(failure)))
					Expect(items).To(BeEmpty())
				} else {
					Expect(err).NotTo(HaveOccurred())
					Expect(items).To(HaveLen(total))
				}
			},
			Entry("RPC budget 2", 2, 0, 3, 1, 2, "RPC budget"),
			Entry("object budget 3", 0, 3, 5, 2, 2, "object budget"),
			Entry("exact budget boundary succeeds", 2, 3, 3, 2, 2, ""),
		)

		DescribeTable("checks widened addition before conversion",
			func(current int32, count int, expected int32, invalid bool) {
				next, err := nextListOffset(current, count)
				if invalid {
					Expect(err).To(HaveOccurred())
				} else {
					Expect(err).NotTo(HaveOccurred())
					Expect(next).To(Equal(expected))
				}
			},
			Entry("last representable offset", int32(math.MaxInt32-1), 1, int32(math.MaxInt32), false),
			Entry("overflow", int32(math.MaxInt32-1), 2, int32(0), true),
			Entry("negative offset", int32(-1), 1, int32(0), true),
			Entry("negative count", int32(0), -1, int32(0), true),
			Entry("zero", int32(0), 0, int32(0), false),
		)
	})

	Describe("seam descriptor fallback is honest", func() {
		for _, format := range []string{outputFormatTable, outputFormatJson, outputFormatYaml} {
			DescribeTable(format+" respects authoritative totals and discloses unknown completeness on stderr",
				func(known, offset bool, total int32, count int, fails bool) {
					var stdout, stderr bytes.Buffer
					console, err := terminal.NewConsole().SetLogger(logger).SetStdout(&stdout).SetStderr(&stderr).Build()
					Expect(err).NotTo(HaveOccurred())
					runner := newInterceptedGetRunner(func(context.Context, string, proto.Message, proto.Message) error {
						return errors.New("unexpected rendering RPC")
					})
					runner.objectHelper, runner.console, runner.logger = mockHelper, console, logger
					runner.marshalOptions = protojson.MarshalOptions{UseProtoNames: true}
					runner.args.format = format
					items := make([]proto.Message, count)
					for i, item := range clusterPageItems(0, count) {
						items[i] = item
					}
					mockHelper.EXPECT().List(gomock.Any(), gomock.Any()).Return(reflection.ListResult{
						Items: items, Total: total, TotalKnown: known, OffsetSupported: offset,
					}, nil).Times(1)
					objects, err := runner.fetchObjects(ctx, nil)
					if fails {
						Expect(err).To(HaveOccurred())
						Expect(objects).To(BeEmpty())
						Expect(stdout.String()).To(BeEmpty())
						return
					}
					Expect(err).NotTo(HaveOccurred())
					Expect(objects).To(HaveLen(count))
					switch format {
					case outputFormatJson:
						err = runner.renderJson(ctx, objects)
					case outputFormatYaml:
						err = runner.renderYaml(ctx, objects)
					default:
						Expect(console.AddTemplates(templatesFS, "templates")).To(Succeed())
						err = runner.renderTable(ctx, objects)
					}
					Expect(err).NotTo(HaveOccurred())
					if count > 0 {
						Expect(strings.Count(stdout.String(), "p2-cluster-0000")).To(Equal(1))
					}
					if known {
						Expect(stderr.String()).To(BeEmpty())
					} else {
						Expect(stderr.String()).To(ContainSubstring("completeness unknown"))
						Expect(stdout.String()).NotTo(ContainSubstring("completeness unknown"))
					}
				},
				Entry("known complete without offset", true, false, int32(1), 1, false),
				Entry("known incomplete without offset", true, false, int32(2), 1, true),
				Entry("unknown total with offset", false, true, int32(0), 1, false),
				Entry("unknown total without offset", false, false, int32(0), 1, false),
				Entry("authoritative empty", true, true, int32(0), 0, false),
			)
		}
	})

	Describe("Secret List then keyed Get compatibility", func() {
		DescribeTable("only expands keyed structured reads, after completing List",
			func(format string, keyed bool, total int) {
				var methods []string
				getCalls := 0
				runner := newInterceptedGetRunner(func(_ context.Context, method string, request, response proto.Message) error {
					methods = append(methods, method)
					if strings.HasSuffix(method, "/List") {
						req := request.(*publicv1.SecretsListRequest)
						start := int(req.GetOffset())
						if len(methods) > 4 {
							return errors.New("test circuit breaker")
						}
						secrets := make([]*publicv1.Secret, min(100, total-start))
						for i := range secrets {
							secrets[i] = &publicv1.Secret{Id: fmt.Sprintf("secret-%04d", start+i)}
						}
						proto.Merge(response, publicv1.SecretsListResponse_builder{Items: secrets, Total: int32(total)}.Build())
						return nil
					}
					Expect(method).To(Equal("/osac.public.v1.Secrets/Get"))
					req := request.(*publicv1.SecretsGetRequest)
					Expect(req.GetId()).To(Equal(fmt.Sprintf("secret-%04d", getCalls)))
					getCalls++
					proto.Merge(response, publicv1.SecretsGetResponse_builder{
						Object: &publicv1.Secret{Id: req.GetId(), Data: map[string][]byte{"fixture": []byte("synthetic")}},
					}.Build())
					return nil
				})
				runner.objectHelper = runner.globalHelper.Lookup("secret")
				runner.args.format = format
				var keys []string
				if keyed {
					keys = []string{"fixture-key"}
				}
				items, err := runner.fetchObjects(ctx, keys)
				Expect(err).NotTo(HaveOccurred())
				Expect(items).To(HaveLen(total))
				if keyed && format != outputFormatTable {
					Expect(getCalls).To(Equal(total))
					for _, item := range items {
						Expect(item.(*publicv1.Secret).GetData()).To(HaveKey("fixture"))
					}
				} else {
					Expect(getCalls).To(BeZero())
				}
				listCalls := max(1, (total+99)/100)
				for _, method := range methods[:listCalls] {
					Expect(method).To(Equal("/osac.public.v1.Secrets/List"))
				}
			},
			Entry("keyed JSON", outputFormatJson, true, 1), Entry("keyed YAML", outputFormatYaml, true, 1),
			Entry("keyed JSON across pages", outputFormatJson, true, 101),
			Entry("unkeyed JSON stays List-only", outputFormatJson, false, 1),
			Entry("keyed table stays List-only", outputFormatTable, true, 1),
		)
	})

	Describe("get-only completion and validation order", func() {
		It("rejects invalid output even without configuration or connection context", func() {
			command := &cobra.Command{Use: "osac", SilenceUsage: true, SilenceErrors: true}
			command.AddCommand(Cmd())
			command.SetArgs([]string{"get", "clusters", "--output=invalid"})
			Expect(command.ExecuteContext(context.Background())).To(MatchError(ContainSubstring("unknown output format")))
		})

		It("includes the singleton in generic get completion without using adapter registration", func() {
			names, directive := completeObjectTypes(nil, nil, "")
			Expect(directive).To(Equal(cobra.ShellCompDirectiveNoFileComp))
			Expect(names).To(ContainElements("billingproviderstatus", "billingproviderstatuses"))
		})

		It("rejects invalid output before connection setup can parse configured CA data", func() {
			var stdout bytes.Buffer
			console, err := terminal.NewConsole().SetLogger(logger).SetStdout(&stdout).SetStderr(&bytes.Buffer{}).Build()
			Expect(err).NotTo(HaveOccurred())
			settings, err := config.NewSettings().SetLogger(logger).SetDir("/tmp/opencode").Build()
			Expect(err).NotTo(HaveOccurred())
			settings.SetAddress("p2-unit.invalid:443")
			settings.AddCaFile(config.CaFile{Name: "/tmp/opencode/p2-invalid-ca-fixture.pem", Content: "not-a-pem-certificate"})
			// Establish that this fixture actually fails connection preparation;
			// an ignored/missing CA path alone would make the ordering test vacuous.
			_, err = settings.CaPool(ctx)
			Expect(err).To(HaveOccurred())
			commandCtx := logging.LoggerIntoContext(terminal.ConsoleIntoContext(config.SettingsIntoContext(ctx, settings), console), logger)
			command := &cobra.Command{Use: "osac", SilenceErrors: true, SilenceUsage: true}
			command.AddCommand(Cmd())
			command.SetOut(&stdout)
			command.SetErr(&bytes.Buffer{})
			command.SetArgs([]string{"get", "clusters", "--output=invalid"})
			err = command.ExecuteContext(commandCtx)
			Expect(err).To(MatchError(ContainSubstring("unknown output format")))
			Expect(stdout.String()).To(BeEmpty())
		})
	})

	Describe("generic command buffers stdout until traversal succeeds", func() {
		for _, format := range []string{outputFormatTable, outputFormatJson, outputFormatYaml} {
			It(format+" success control reaches the local RPC and the real output writer", func() {
				calls := 0
				stdout, err := executeGenericGet(false, func(method string, _, response proto.Message) error {
					calls++
					Expect(method).To(Equal("/osac.public.v1.Clusters/List"))
					data, err := proto.Marshal(&publicv1.ClustersListResponse{Items: clusterPageItems(0, 1), Total: 1})
					Expect(err).NotTo(HaveOccurred())
					return proto.Unmarshal(data, response)
				}, "clusters", "--output="+format)
				Expect(err).NotTo(HaveOccurred())
				Expect(calls).To(Equal(1))
				Expect(stdout).To(ContainSubstring("p2-cluster-0000"))
			})

			DescribeTable(format+" emits zero inventory bytes on a second-page failure",
				func(scenario string) {
					calls := 0
					stdout, err := executeGenericGet(false, func(method string, request, response proto.Message) error {
						calls++
						Expect(method).To(Equal("/osac.public.v1.Clusters/List"))
						if calls > 2 {
							return grpcstatus.Error(codes.Internal, "test circuit breaker, not the error oracle")
						}
						items := clusterPageItems(0, 1)
						if calls == 2 {
							req := request.ProtoReflect()
							Expect(req.Get(req.Descriptor().Fields().ByName("offset")).Int()).To(Equal(int64(1)))
							if scenario == "RPC failure" {
								return grpcstatus.Error(codes.Internal, "p2 second page failed")
							}
							items = nil
						}
						data, err := proto.Marshal(&publicv1.ClustersListResponse{Items: items, Total: 2})
						Expect(err).NotTo(HaveOccurred())
						return proto.Unmarshal(data, response)
					}, "clusters", "--output="+format)
					Expect(stdout).To(BeEmpty(), "actual Cobra/console stdout must stay empty; observed RPC calls=%d, error=%v", calls, err)
					Expect(calls).To(Equal(2), "the later failure must actually be reached")
					Expect(err).To(HaveOccurred())
					if scenario == "RPC failure" {
						Expect(err.Error()).To(ContainSubstring("p2 second page failed"))
					}
				},
				Entry("RPC failure", "RPC failure"), Entry("no progress", "no progress"),
			)
		}
	})

	Describe("generic singleton command uses one empty Get", func() {
		for _, private := range []bool{false, true} {
			pkg := packages.PublicV1
			if private {
				pkg = packages.PrivateV1
			}
			for _, format := range []string{outputFormatTable, outputFormatJson, outputFormatYaml} {
				DescribeTable(pkg+" "+format+" preserves singleton payload presence without identity/metadata",
					func(shape string) {
						calls := 0
						stdout, err := executeGenericGet(private, func(method string, request, response proto.Message) error {
							calls++
							Expect(method).To(Equal("/"+pkg+".BillingProviderStatuses/Get"), "never List, Watch, or a keyed fallback")
							Expect(request.ProtoReflect().Descriptor().Fields().Len()).To(BeZero())
							Expect(proto.Size(request)).To(BeZero())
							if shape != "absent" {
								res := response.ProtoReflect()
								object := res.Mutable(res.Descriptor().Fields().ByName("object")).Message()
								Expect(object.Descriptor().Fields().ByName("id")).To(BeNil())
								Expect(object.Descriptor().Fields().ByName("metadata")).To(BeNil())
								if shape == "payload" {
									fields := object.Descriptor().Fields()
									state := fields.ByName("state")
									object.Set(state, protoreflect.ValueOfEnum(state.Enum().Values().ByName("BILLING_PROVIDER_STATE_NOT_CONFIGURED").Number()))
									object.Set(fields.ByName("message"), protoreflect.ValueOfString("p2 singleton payload"))
								}
							}
							return nil
						}, "billingproviderstatus", "--output="+format)
						Expect(calls).To(Equal(1), "actual generic command must invoke Get, not merely offer a discovery capability")
						if shape == "absent" {
							Expect(err).To(HaveOccurred())
							Expect(stdout).To(BeEmpty())
							return
						}
						Expect(err).NotTo(HaveOccurred())
						Expect(stdout).NotTo(BeEmpty())
						if format == outputFormatTable {
							Expect(stdout).NotTo(MatchRegexp(`\b(ID|NAME|TENANT|DELETING)\b`))
							Expect(strings.Fields(strings.Split(strings.TrimSpace(stdout), "\n")[0])).To(Equal([]string{
								"STATE", "PROVIDER", "MESSAGE", "CAPABILITIES", "LAST-SUCCESSFUL-CHECK", "USAGE-DELIVERY",
							}))
							if shape == "payload" {
								Expect(stdout).To(ContainSubstring("p2 singleton payload"))
							}
						} else {
							var value map[string]any
							if format == outputFormatJson {
								Expect(json.Unmarshal([]byte(stdout), &value)).To(Succeed())
							} else {
								Expect(yaml.Unmarshal([]byte(stdout), &value)).To(Succeed())
							}
							expected := map[string]any{"@type": "type.googleapis.com/" + pkg + ".BillingProviderStatus"}
							if shape == "payload" {
								expected["state"] = "BILLING_PROVIDER_STATE_NOT_CONFIGURED"
								expected["message"] = "p2 singleton payload"
							}
							Expect(value).To(Equal(expected))
						}
					},
					Entry("present NOT_CONFIGURED payload", "payload"), Entry("present empty object", "empty"), Entry("absent object", "absent"),
				)
			}

			DescribeTable(pkg+" rejects singleton keys and explicitly supplied flags before RPC",
				func(extra []string) {
					calls := 0
					args := append([]string{"billingproviderstatus"}, extra...)
					stdout, err := executeGenericGet(private, func(string, proto.Message, proto.Message) error {
						calls++
						return grpcstatus.Error(codes.Internal, "singleton validation reached RPC")
					}, args...)
					Expect(err).To(HaveOccurred())
					Expect(calls).To(BeZero())
					Expect(stdout).To(BeEmpty())
				},
				Entry("key", []string{"invented-id"}), Entry("explicit empty filter", []string{"--filter="}),
				Entry("explicit filter", []string{"--filter=true"}), Entry("explicit false watch", []string{"--watch=false"}),
				Entry("watch", []string{"--watch"}), Entry("invalid format", []string{"--output=invalid"}),
			)
		}
	})

	DescribeTable("singleton structured output has truthful shape",
		func(format string, count int) {
			var stdout bytes.Buffer
			console, err := terminal.NewConsole().SetLogger(logger).SetStdout(&stdout).SetStderr(&bytes.Buffer{}).Build()
			Expect(err).NotTo(HaveOccurred())
			runner := &runnerContext{console: console, marshalOptions: protojson.MarshalOptions{UseProtoNames: true}}
			objects := make([]proto.Message, count)
			for i := range objects {
				objects[i] = &publicv1.BillingProviderStatus{State: publicv1.BillingProviderState_BILLING_PROVIDER_STATE_NOT_CONFIGURED}
			}
			var value any
			if format == outputFormatJson {
				Expect(runner.renderJson(ctx, objects)).To(Succeed())
				Expect(json.Unmarshal(stdout.Bytes(), &value)).To(Succeed())
			} else {
				Expect(runner.renderYaml(ctx, objects)).To(Succeed())
				Expect(yaml.Unmarshal(stdout.Bytes(), &value)).To(Succeed())
			}
			var values []any
			if count == 1 {
				values = []any{value}
			} else {
				var ok bool
				values, ok = value.([]any)
				Expect(ok).To(BeTrue(), "zero and many items must be sequences")
			}
			Expect(values).To(HaveLen(count))
			for _, item := range values {
				Expect(item).To(Equal(map[string]any{
					"@type": "type.googleapis.com/osac.public.v1.BillingProviderStatus",
					"state": "BILLING_PROVIDER_STATE_NOT_CONFIGURED",
				}))
			}
		},
		Entry("JSON zero", outputFormatJson, 0), Entry("JSON one", outputFormatJson, 1), Entry("JSON many", outputFormatJson, 2),
		Entry("YAML zero", outputFormatYaml, 0), Entry("YAML one", outputFormatYaml, 1), Entry("YAML many", outputFormatYaml, 2),
	)

	Describe("fetchObjects", func() {
		It("should call Get for each object when UseGetForStructuredOutput is true", func() {
			listObject := &publicv1.Cluster{Id: "cluster-1", Metadata: &publicv1.Metadata{Name: "my-cluster"}}
			fullObject := &publicv1.Cluster{Id: "cluster-1", Metadata: &publicv1.Metadata{Name: "my-cluster"}}

			mockHelper.EXPECT().UseGetForStructuredOutput().Return(true)
			mockHelper.EXPECT().List(gomock.Any(), gomock.Any()).
				Return(reflection.ListResult{Items: []proto.Message{listObject}, Total: 1, TotalKnown: true}, nil)
			mockHelper.EXPECT().GetId(listObject).Return("cluster-1")
			mockHelper.EXPECT().Get(gomock.Any(), "cluster-1").Return(fullObject, nil)

			runner := &runnerContext{
				objectHelper: mockHelper,
			}
			runner.args.format = outputFormatYaml

			results, err := runner.fetchObjects(ctx, []string{"my-cluster"})
			Expect(err).ToNot(HaveOccurred())
			Expect(results).To(HaveLen(1))
			Expect(proto.Equal(results[0], fullObject)).To(BeTrue())
		})

		It("should not call Get when UseGetForStructuredOutput is false", func() {
			listObject := &publicv1.Cluster{Id: "cluster-1", Metadata: &publicv1.Metadata{Name: "my-cluster"}}

			mockHelper.EXPECT().UseGetForStructuredOutput().Return(false)
			mockHelper.EXPECT().List(gomock.Any(), gomock.Any()).
				Return(reflection.ListResult{Items: []proto.Message{listObject}, Total: 1, TotalKnown: true}, nil)

			runner := &runnerContext{
				objectHelper: mockHelper,
			}
			runner.args.format = outputFormatYaml

			results, err := runner.fetchObjects(ctx, []string{"my-cluster"})
			Expect(err).ToNot(HaveOccurred())
			Expect(results).To(HaveLen(1))
			Expect(proto.Equal(results[0], listObject)).To(BeTrue())
		})

		It("should not check UseGetForStructuredOutput for table format", func() {
			listObject := &publicv1.Cluster{Id: "cluster-1"}

			mockHelper.EXPECT().List(gomock.Any(), gomock.Any()).
				Return(reflection.ListResult{Items: []proto.Message{listObject}, Total: 1, TotalKnown: true}, nil)

			runner := &runnerContext{
				objectHelper: mockHelper,
			}
			runner.args.format = outputFormatTable

			results, err := runner.fetchObjects(ctx, []string{"my-cluster"})
			Expect(err).ToNot(HaveOccurred())
			Expect(results).To(HaveLen(1))
		})

		It("should return error when Get fails", func() {
			listObject := &publicv1.Cluster{Id: "cluster-1"}

			mockHelper.EXPECT().UseGetForStructuredOutput().Return(true)
			mockHelper.EXPECT().List(gomock.Any(), gomock.Any()).
				Return(reflection.ListResult{Items: []proto.Message{listObject}, Total: 1, TotalKnown: true}, nil)
			mockHelper.EXPECT().GetId(listObject).Return("cluster-1")
			mockHelper.EXPECT().Get(gomock.Any(), "cluster-1").Return(nil, fmt.Errorf("not found"))

			runner := &runnerContext{
				objectHelper: mockHelper,
			}
			runner.args.format = outputFormatJson

			_, err := runner.fetchObjects(ctx, []string{"my-cluster"})
			Expect(err).To(HaveOccurred())
		})
	})

	It("offers all networking resources for get completion", func() {
		completed, directive := completeObjectTypes(nil, nil, "")
		Expect(directive).To(Equal(cobra.ShellCompDirectiveNoFileComp))
		Expect(completed).To(ContainElements(
			"virtualnetwork", "virtualnetworks",
			"subnet", "subnets",
			"securitygroup", "securitygroups",
			"externalip", "externalips",
			"externalipattachment", "externalipattachments",
			"natgateway", "natgateways",
		))
	})

	for _, format := range []string{outputFormatJson, outputFormatYaml} {
		It("renders public ComputeInstance disk progress in "+format, func() {
			var stdout bytes.Buffer
			console, err := terminal.NewConsole().SetLogger(logger).
				SetStdout(&stdout).SetStderr(&bytes.Buffer{}).Build()
			Expect(err).ToNot(HaveOccurred())
			instance := publicv1.ComputeInstance_builder{
				Status: publicv1.ComputeInstanceStatus_builder{
					Volumes: []*publicv1.ComputeInstanceVolume{
						publicv1.ComputeInstanceVolume_builder{DiskKey: "boot", VolumeId: "volume-boot"}.Build(),
					},
					Conditions: []*publicv1.ComputeInstanceCondition{
						publicv1.ComputeInstanceCondition_builder{
							Type:    publicv1.ComputeInstanceConditionType_COMPUTE_INSTANCE_CONDITION_TYPE_PROVISIONED,
							Status:  publicv1.ConditionStatus_CONDITION_STATUS_FALSE,
							Reason:  proto.String("VolumeRegistrationPending"),
							Message: proto.String("Disk Volume IDs persisted: 1/2; blocked disks: disk-1; recovery pending."),
						}.Build(),
					},
				}.Build(),
			}.Build()
			runner := &runnerContext{
				console:        console,
				marshalOptions: protojson.MarshalOptions{UseProtoNames: true},
			}
			if format == outputFormatJson {
				Expect(runner.renderJson(ctx, []proto.Message{instance})).To(Succeed())
			} else {
				Expect(runner.renderYaml(ctx, []proto.Message{instance})).To(Succeed())
			}
			Expect(stdout.String()).To(ContainSubstring("volume-boot"))
			Expect(stdout.String()).To(ContainSubstring("VolumeRegistrationPending"))
			Expect(stdout.String()).To(ContainSubstring("disk-1"))
			Expect(stdout.String()).NotTo(ContainSubstring("volume_registration"))
		})
	}
})
