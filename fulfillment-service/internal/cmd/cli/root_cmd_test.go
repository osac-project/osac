/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/ginkgo/v2/dsl/table"
	. "github.com/onsi/gomega"
	"github.com/spf13/cobra"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/dynamicpb"
	"sigs.k8s.io/yaml"

	"github.com/osac-project/osac/fulfillment-service/internal/config"
	"github.com/osac-project/osac/fulfillment-service/internal/logging"
	"github.com/osac-project/osac/fulfillment-service/internal/terminal"
	itesting "github.com/osac-project/osac/fulfillment-service/internal/testing"
)

// Exercise the actual Root command graph and native gRPC transport. Only the
// OS-console/config pre-run is overridden for stdout capture: these are NOT
// login, saved-setting resolution, JWT, OPA or server-authorization tests.
func executeRootWithRPC(private bool, invoke func(string, proto.Message, proto.Message) error, args ...string) (string, error) {
	server := itesting.NewServer(grpc.UnknownServiceHandler(func(_ any, stream grpc.ServerStream) error {
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
	logger, err := logging.NewLogger().SetWriter(GinkgoWriter).Build()
	Expect(err).NotTo(HaveOccurred())
	var stdout bytes.Buffer
	console, err := terminal.NewConsole().SetLogger(logger).SetStdout(&stdout).SetStderr(&bytes.Buffer{}).Build()
	Expect(err).NotTo(HaveOccurred())
	settings, err := config.NewSettings().SetLogger(logger).SetDir(GinkgoT().TempDir()).Build()
	Expect(err).NotTo(HaveOccurred())
	settings.SetAddress(server.Address())
	settings.SetPlaintext(true)
	settings.SetPrivate(private)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ctx = config.ProjectIntoContext(config.TenantIntoContext(ctx, "customer"), "selected-project")
	ctx = logging.LoggerIntoContext(terminal.ConsoleIntoContext(config.SettingsIntoContext(ctx, settings), console), logger)
	root, err := Root()
	Expect(err).NotTo(HaveOccurred())
	root.PersistentPreRunE = nil
	root.SetOut(&stdout)
	root.SetErr(&bytes.Buffer{})
	root.SetArgs(args)
	err = root.ExecuteContext(ctx)
	return stdout.String(), err
}

func populateRootBillingList(response proto.Message, start, count, total int) {
	items := make([]any, count)
	for i := range items {
		items[i] = map[string]any{"id": fmt.Sprintf("billing-%04d", start+i),
			"metadata": map[string]any{"name": fmt.Sprintf("billing-name-%04d", start+i)}}
	}
	data, err := json.Marshal(map[string]any{"items": items, "size": count, "total": total})
	Expect(err).NotTo(HaveOccurred())
	Expect(protojson.Unmarshal(data, response)).To(Succeed())
}

func rootBillingItem(kind string, index int) map[string]any {
	metadata := map[string]any{"name": fmt.Sprintf("billing-name-%04d", index), "tenant": "customer",
		"labels": map[string]any{"environment": "test"}, "finalizers": []any{"acceptance-finalizer"}}
	item := map[string]any{"id": fmt.Sprintf("billing-%04d", index), "metadata": metadata}
	if kind == "billingaccount" {
		metadata["tenant"] = "system"
		item["spec"] = map[string]any{}
		state := map[string]any{"provider_key": "m360", "display_name": "Acceptance account", "base_currency": "USD",
			"state": "BILLING_ACCOUNT_STATE_ACTIVE", "state_observation_timestamp": "2026-11-18T00:00:00Z",
			"external_billing_account_id": "attribution-token",
			"provider_account_reference":  "native-reference-not-for-table"}
		item["status"] = state
	} else {
		item["spec"] = map[string]any{
			"billing_account": map[string]any{"id": "account-id", "name": "ba-canonical"}}
		state := map[string]any{"connection_state": "TENANT_BILLING_ASSIGNMENT_CONNECTION_STATE_PENDING",
			"effective_from_timestamp": "2026-11-18T00:00:00Z", "message": "manual remediation required"}
		if index > 0 {
			state["effective_to_timestamp"] = "2026-11-18T01:00:00Z"
		}
		item["status"] = state
	}
	return item
}

func populateRootBillingInventory(response proto.Message, kind string, start, count, total int) {
	items := make([]any, count)
	for i := range items {
		items[i] = rootBillingItem(kind, start+i)
	}
	data, err := json.Marshal(map[string]any{"items": items, "size": count, "total": total})
	Expect(err).NotTo(HaveOccurred())
	Expect(protojson.Unmarshal(data, response)).To(Succeed())
}

func decodeRootOutputValues(stdout, format string, count int) []any {
	var value any
	if format == "json" {
		Expect(json.Unmarshal([]byte(stdout), &value)).To(Succeed())
	} else {
		Expect(yaml.Unmarshal([]byte(stdout), &value)).To(Succeed())
	}
	if count == 1 {
		return []any{value}
	}
	values, ok := value.([]any)
	Expect(ok).To(BeTrue(), "zero/many must be sequences, not API List envelopes")
	Expect(values).To(HaveLen(count))
	return values
}

func rootBillingHeaders(kind string) []string {
	if kind == "billingproviderstatus" {
		return []string{"STATE", "PROVIDER", "MESSAGE", "CAPABILITIES", "LAST-SUCCESSFUL-CHECK", "USAGE-DELIVERY"}
	}
	headers := []string{"ID", "NAME", "PROVIDER", "DISPLAY-NAME", "CURRENCY", "STATE", "EXTERNAL-BILLING-ACCOUNT-ID", "STATUS-OBSERVED"}
	if kind == "tenantbillingassignment" {
		headers = []string{"ID", "NAME", "TENANT", "ACCOUNT", "STATE", "EFFECTIVE-FROM", "EFFECTIVE-TO", "MESSAGE"}
	}
	// The private collection Metadata descriptor exposes finalizers. The renderer
	// inserts this descriptor-guarded column immediately after ID, not at the end
	// of the YAML.
	return append([]string{headers[0], "DELETING"}, headers[1:]...)
}

var _ = Describe("actual Root billing acceptance", func() {
	It("registers billing status alongside the existing get subcommands", func() {
		root, err := Root()
		Expect(err).NotTo(HaveOccurred())
		command, _, err := root.Find([]string{"get"})
		Expect(err).NotTo(HaveOccurred())
		var names []string
		for _, child := range command.Commands() {
			names = append(names, child.Name())
		}
		Expect(names).To(ConsistOf("billingproviderstatus", "externalippool", "storagetier", "token"))
	})

	DescribeTable("routes canonical keyed collection reads through the actual generic Root",
		func(resource, service string) {
			var calls atomic.Int32
			pkg := "private"
			_, err := executeRootWithRPC(true, func(path string, request, response proto.Message) error {
				calls.Add(1)
				Expect(path).To(Equal("/osac." + pkg + ".v1." + service + "/List"))
				filter := request.ProtoReflect().Get(request.ProtoReflect().Descriptor().Fields().ByName("filter")).String()
				Expect(filter).To(ContainSubstring(`this.id in ["one", "two\"quoted"] || this.metadata.name in ["one", "two\"quoted"]`))
				Expect(filter).To(ContainSubstring(`this.metadata.labels["environment"] == "test"`))
				Expect(filter).NotTo(ContainSubstring("metadata.project"))
				if service == "BillingAccounts" {
					Expect(filter).NotTo(ContainSubstring("metadata.tenant"))
				} else {
					Expect(filter).To(ContainSubstring(`this.metadata.tenant == "customer"`))
				}
				populateRootBillingList(response, 0, 0, 0)
				return nil
			}, "get", resource, "one", `two"quoted`, "--filter", `this.metadata.labels["environment"] == "test"`, "-o", "json")
			Expect(err).NotTo(HaveOccurred())
			Expect(calls.Load()).To(Equal(int32(1)), "keyed JSON must use List, never scaffold Get")
		},
		Entry("account", "billingaccount", "BillingAccounts"),
		Entry("assignment", "tenantbillingassignment", "TenantBillingAssignments"),
	)

	pkg := "osac.private.v1"
	for _, kind := range []string{"billingaccount", "tenantbillingassignment"} {
		service, object := "BillingAccounts", "BillingAccount"
		if kind == "tenantbillingassignment" {
			service, object = "TenantBillingAssignments", "TenantBillingAssignment"
		}
		for _, format := range []string{"table", "json", "yaml"} {
			DescribeTable(pkg+" "+kind+" "+format+" actual Root cardinality and nested descriptor output", func(count int) {
				var calls atomic.Int32
				stdout, err := executeRootWithRPC(true, func(path string, _, response proto.Message) error {
					calls.Add(1)
					Expect(path).To(Equal("/" + pkg + "." + service + "/List"))
					populateRootBillingInventory(response, kind, 0, count, count)
					return nil
				}, "get", kind, "billing-0000", "--output="+format)
				Expect(err).NotTo(HaveOccurred())
				Expect(calls.Load()).To(Equal(int32(1)))
				if format == "table" {
					if count == 0 {
						Expect(stdout).To(ContainSubstring("There are no objects matching the given criteria."))
					} else {
						lines := strings.Split(strings.TrimSpace(stdout), "\n")
						Expect(strings.Fields(lines[0])).To(Equal(rootBillingHeaders(kind)))
						Expect(lines).To(HaveLen(count + 1))
						Expect(stdout).To(ContainSubstring("billing-name-0000"))
					}
					Expect(stdout).NotTo(ContainSubstring("native-reference-not-for-table"))
					return
				}
				for i, value := range decodeRootOutputValues(stdout, format, count) {
					expected := rootBillingItem(kind, i)
					expected["@type"] = "type.googleapis.com/" + pkg + "." + object
					Expect(value).To(Equal(expected))
				}
			}, Entry("zero", 0), Entry("one", 1), Entry("many", 2))
		}
	}

	for _, format := range []string{"table", "json", "yaml"} {
		DescribeTable(pkg+" "+format+" actual Root singleton presence", func(mode string) {
			var calls atomic.Int32
			stdout, err := executeRootWithRPC(true, func(path string, request, response proto.Message) error {
				calls.Add(1)
				Expect(path).To(Equal("/" + pkg + ".BillingProviderStatuses/Get"))
				Expect(proto.Size(request)).To(BeZero())
				if mode == "absent" {
					return nil
				}
				body := `{"object":{}}`
				if mode == "not configured" {
					body = `{"object":{"state":"BILLING_PROVIDER_STATE_NOT_CONFIGURED","usage_delivery_state":"BILLING_USAGE_DELIVERY_STATE_UNKNOWN"}}`
				}
				return protojson.Unmarshal([]byte(body), response)
			}, "get", "billingproviderstatus", "--output="+format)
			Expect(calls.Load()).To(Equal(int32(1)))
			if mode == "absent" {
				Expect(err).To(HaveOccurred())
				Expect(stdout).To(BeEmpty())
				return
			}
			Expect(err).NotTo(HaveOccurred())
			if format == "table" {
				Expect(strings.Fields(strings.Split(strings.TrimSpace(stdout), "\n")[0])).To(Equal(rootBillingHeaders("billingproviderstatus")))
			} else {
				expected := map[string]any{"@type": "type.googleapis.com/" + pkg + ".BillingProviderStatus"}
				if mode == "not configured" {
					expected["state"] = "BILLING_PROVIDER_STATE_NOT_CONFIGURED"
					expected["usage_delivery_state"] = "BILLING_USAGE_DELIVERY_STATE_UNKNOWN"
				}
				Expect(decodeRootOutputValues(stdout, format, 1)).To(Equal([]any{expected}))
			}
		}, Entry("absent response", "absent"), Entry("present empty object", "empty"), Entry("NOT_CONFIGURED succeeds", "not configured"))
	}

	DescribeTable("actual Root canonical singular plural and qualified package priority", func(resource, pkg, service string, singleton bool) {
		var calls atomic.Int32
		_, err := executeRootWithRPC(true, func(path string, request, response proto.Message) error {
			calls.Add(1)
			method := "List"
			if singleton {
				method = "Get"
				Expect(proto.Size(request)).To(BeZero())
				Expect(protojson.Unmarshal([]byte(`{"object":{}}`), response)).To(Succeed())
			} else {
				populateRootBillingList(response, 0, 0, 0)
			}
			Expect(path).To(Equal("/osac." + pkg + ".v1." + service + "/" + method))
			return nil
		}, "get", resource, "-o", "json")
		Expect(err).NotTo(HaveOccurred())
		Expect(calls.Load()).To(Equal(int32(1)))
	},
		Entry("accounts plural", "billingaccounts", "private", "BillingAccounts", false),
		Entry("assignment plural", "tenantbillingassignments", "private", "TenantBillingAssignments", false),
		Entry("status plural", "billingproviderstatuses", "private", "BillingProviderStatuses", true),
		Entry("explicit account", "osac.private.v1.BillingAccount", "private", "BillingAccounts", false),
		Entry("explicit assignment", "osac.private.v1.TenantBillingAssignment", "private", "TenantBillingAssignments", false),
		Entry("explicit status", "osac.private.v1.BillingProviderStatus", "private", "BillingProviderStatuses", true),
	)

	DescribeTable("actual Root retired aliases are wrong-object guidance without RPC", func(name string) {
		var calls atomic.Int32
		stdout, err := executeRootWithRPC(true, func(string, proto.Message, proto.Message) error { calls.Add(1); return nil }, "get", name)
		Expect(err).NotTo(HaveOccurred(), "preserve the existing wrong-object exit policy")
		Expect(stdout).To(ContainSubstring(name))
		Expect(calls.Load()).To(BeZero())
	}, Entry("accounts", "billing-accounts"), Entry("assignments", "tenant-billing-assignments"),
		Entry("status", "billing-provider-status"), Entry("shorthand", "tba"))

	DescribeTable("actual Root retired pagination flags fail parsing without RPC", func(flag string) {
		var calls atomic.Int32
		_, err := executeRootWithRPC(true, func(string, proto.Message, proto.Message) error { calls.Add(1); return nil }, "get", "billingaccount", flag+"=1")
		Expect(err).To(MatchError(ContainSubstring("unknown flag: " + flag)))
		Expect(calls.Load()).To(BeZero())
	}, Entry("limit", "--limit"), Entry("offset", "--offset"))

	It("uses the actual get delete label and edit completion callbacks without server effects", func() {
		root, err := Root()
		Expect(err).NotTo(HaveOccurred())
		logger, err := logging.NewLogger().SetWriter(GinkgoWriter).Build()
		Expect(err).NotTo(HaveOccurred())
		settings, err := config.NewSettings().SetLogger(logger).SetDir(GinkgoT().TempDir()).Build()
		Expect(err).NotTo(HaveOccurred())
		settings.SetPrivate(true)
		ctx := config.SettingsIntoContext(context.Background(), settings)
		for _, verb := range []string{"get", "delete", "label", "edit"} {
			command, _, err := root.Find([]string{verb})
			Expect(err).NotTo(HaveOccurred())
			Expect(command.ValidArgsFunction).NotTo(BeNil())
			command.SetContext(ctx)
			names, directive := command.ValidArgsFunction(command, nil, "")
			Expect(directive).To(Equal(cobra.ShellCompDirectiveNoFileComp))
			Expect(names).To(ContainElements("cluster", "clusters"))
			for _, retired := range []string{"billing-accounts", "tenant-billing-assignments", "billing-provider-status", "tba"} {
				Expect(names).NotTo(ContainElement(retired))
			}
			if verb == "get" {
				Expect(names).To(ContainElements("billingaccount", "billingaccounts", "tenantbillingassignment", "tenantbillingassignments"))
			} else {
				Expect(names).NotTo(ContainElements("billingproviderstatus", "billingproviderstatuses"))
				if verb == "edit" {
					Expect(names).To(ContainElements("billingaccount", "billingaccounts"))
				}
			}
		}
	})
})

var _ = Describe("CLI root color flag", func() {
	It("enables styled help on a redirected writer", func() {
		GinkgoT().Setenv("NO_COLOR", "")
		cmd, err := Root()
		Expect(err).NotTo(HaveOccurred())
		Expect(cmd.PersistentFlags().Lookup("color")).NotTo(BeNil())
		var output bytes.Buffer
		cmd.SetOut(&output)
		cmd.SetArgs([]string{"--color", "describe", "computeinstancecatalogitem", "--help"})
		Expect(cmd.Execute()).To(Succeed())
		Expect(output.String()).To(ContainSubstring("\x1b["))
	})
})

var _ = Describe("CLI root --project flag", func() {
	It("registers the --project persistent flag", func() {
		cmd, err := Root()
		Expect(err).NotTo(HaveOccurred())
		flag := cmd.PersistentFlags().Lookup("project")
		Expect(flag).NotTo(BeNil())
		Expect(flag.DefValue).To(Equal(""))
	})

	It("includes the project subcommand", func() {
		cmd, err := Root()
		Expect(err).NotTo(HaveOccurred())
		var names []string
		for _, sub := range cmd.Commands() {
			names = append(names, sub.Name())
		}
		Expect(names).To(ContainElement("project"))
	})
})
