/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package it

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/osac-project/osac/fulfillment-service/internal/billing"
	"github.com/osac-project/osac/fulfillment-service/internal/testing/billingfake"
	"github.com/osac-project/osac/fulfillment-service/internal/testing/billingstub"
	"github.com/osac-project/osac/fulfillment-service/internal/uuid"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

// The dev deployment configures no billing provider, so these specs exercise disabled mode end to end. They need no
// provider, hence no provider call can happen.
var _ = Describe("Billing provider status", Label("billing"), func() {
	It("is NOT_CONFIGURED over public gRPC when no provider is configured", func(ctx context.Context) {
		client := publicv1.NewBillingProviderStatusesClient(tool.ExternalView().AdminConn())
		response, err := client.Get(ctx, publicv1.BillingProviderStatusesGetRequest_builder{}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(response.GetObject().GetState()).To(Equal(
			publicv1.BillingProviderState_BILLING_PROVIDER_STATE_NOT_CONFIGURED))
		Expect(response.GetObject().GetProviderKey()).To(BeEmpty())
	})

	It("is shown by the CLI as NOT_CONFIGURED", func(ctx context.Context) {
		homeDir, err := tool.NewCLIHomeDir()
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { Expect(os.RemoveAll(homeDir)).To(Succeed()) })
		_, _, exitCode := tool.LoginCLI(ctx, homeDir, adminUsername, adminsPassword)
		Expect(exitCode).To(Equal(0), "login should succeed")

		stdout, _, exitCode := tool.RunCLI(ctx, homeDir, "get", "billingproviderstatus", "-o", "json")
		Expect(exitCode).To(Equal(0))
		Expect(stdout).To(MatchJSON(`{
			"@type": "type.googleapis.com/osac.public.v1.BillingProviderStatus",
			"state": "BILLING_PROVIDER_STATE_NOT_CONFIGURED",
			"usage_delivery_state": "BILLING_USAGE_DELIVERY_STATE_UNKNOWN"
		}`))
		items := billingCLIItems(ctx, homeDir, "billingproviderstatus", "yaml", "osac.public.v1.BillingProviderStatus")
		Expect(items).To(ConsistOf(map[string]any{
			"@type":                "type.googleapis.com/osac.public.v1.BillingProviderStatus",
			"state":                "BILLING_PROVIDER_STATE_NOT_CONFIGURED",
			"usage_delivery_state": "BILLING_USAGE_DELIVERY_STATE_UNKNOWN",
		}))
		table := expectBillingCLITable(ctx, homeDir, "billingproviderstatus", []string{
			"STATE", "PROVIDER", "MESSAGE", "CAPABILITIES", "LAST-SUCCESSFUL-CHECK", "USAGE-DELIVERY",
		}, 1)
		Expect(strings.Fields(strings.Split(strings.TrimSpace(table), "\n")[1])).To(Equal(
			[]string{"NOT_CONFIGURED", "-", "-", "-", "-", "UNKNOWN"}))
	})
})

// These specs point the deployed grpc-server at a deterministic stub M360 server through the real M360 settings
// (OSAC_BILLING_PROVIDER=m360), mounted at the chart's paths, so they exercise the real adapter. The stub runs in this
// test process and serves TLS with a certificate for the host of its URL, signed by a test CA that the specs mount as
// OSAC_BILLING_CA_FILE. The cluster must reach the stub, which depends on the environment, so the specs are skipped
// unless both variables are set:
//
//	OSAC_IT_BILLING_STUB_LISTEN  address to bind, such as 0.0.0.0:18443
//	OSAC_IT_BILLING_STUB_URL     URL of that listener as seen from the cluster, such as https://172.18.0.1:18443
//
// They restart the shared grpc-server, so they must not overlap other specs.
var _ = Describe("Billing provider status with a stub M360", Label("billing", "billing-configured"), Serial, func() {
	var (
		stub           *billingstub.Stub
		stubURL        string
		accounts       []billingfake.AccountSpec
		correlationIDs []string
	)

	// The scenario lists the substring-only candidate before the exact match, so a resolver that keeps the first
	// provider match fails instead of passing by accident.
	accountScenario := func() billingfake.Scenario {
		return billingfake.Scenario{Accounts: []billingfake.AccountSpec{accounts[1], accounts[0]}}
	}

	// expectCleanLog checks the grpc-server log of every current pod: the provider was configured, and neither the
	// credential, the base URL nor the credential path leaked into it.
	expectCleanLog := func(ctx context.Context) {
		GinkgoHelper()
		current := &appsv1.Deployment{}
		Expect(tool.KubeClient().Get(ctx, crclient.ObjectKey{Namespace: billingStubNamespace,
			Name: billingStubDeployment}, current)).To(Succeed())
		pods := &corev1.PodList{}
		Expect(tool.KubeClient().List(ctx, pods, crclient.InNamespace(billingStubNamespace),
			crclient.MatchingLabels(current.Spec.Selector.MatchLabels))).To(Succeed())
		checked := 0
		for _, pod := range pods.Items {
			// A pod of the previous revision can still be terminating after the rollout.
			if pod.DeletionTimestamp != nil {
				continue
			}
			raw, err := tool.KubeClientSet().CoreV1().Pods(billingStubNamespace).GetLogs(pod.Name,
				&corev1.PodLogOptions{Container: billingStubContainer}).DoRaw(ctx)
			Expect(err).ToNot(HaveOccurred())
			var entries []map[string]any
			for _, line := range strings.Split(string(raw), "\n") {
				var entry map[string]any
				if json.Unmarshal([]byte(line), &entry) == nil {
					entries = append(entries, entry)
				}
			}
			Expect(entries).To(ContainElement(SatisfyAll(HaveKeyWithValue("msg", "Billing provider configured"),
				HaveKeyWithValue("provider", "m360"))), "pod %s", pod.Name)
			absentValues := []string{"configured but unusable", "failed to build", billingStubToken, stubURL,
				billingStubCredentialFile}
			for _, account := range accounts {
				absentValues = append(absentValues, account.ID)
				if account.ExternalID != "" {
					absentValues = append(absentValues, account.ExternalID)
				}
			}
			absentValues = append(absentValues, correlationIDs...)
			for _, absent := range absentValues {
				Expect(string(raw)).ToNot(ContainSubstring(absent), "pod %s", pod.Name)
			}
			checked++
		}
		Expect(checked).To(BeNumerically(">", 0))
	}

	BeforeEach(func(ctx context.Context) {
		listen := os.Getenv("OSAC_IT_BILLING_STUB_LISTEN")
		stubURL = os.Getenv("OSAC_IT_BILLING_STUB_URL")
		if listen == "" || stubURL == "" {
			Skip("OSAC_IT_BILLING_STUB_LISTEN and OSAC_IT_BILLING_STUB_URL are not set")
		}
		// Discovery retains OSAC account/history rows. Never reuse a provider reference, display name, or tenant
		// between specs or reruns, including when the same database is reused.
		suffix := uuid.New()
		correlationIDs = nil
		accounts = []billingfake.AccountSpec{
			{ID: uuid.New(), Name: "billing-stub-first-" + suffix, ExternalID: "billing-correlation-" + suffix,
				RawStatus: "Active", ApprovalStatus: "Approved"},
			// M360's substring search returns both accounts for the first value. Only one is an exact match.
			{ID: uuid.New(), Name: "billing-stub-second-" + suffix, ExternalID: "billing-correlation-" + suffix + "-other",
				RawStatus: "Active", ApprovalStatus: "Approved"},
		}
		stub = billingstub.New(accountScenario())
		configureBillingStub(ctx, stub, listen, stubURL)
	})

	// Each List reaches the deployed public service; no database fixture inserts or fake adapter are involved.
	findAccount := func(ctx context.Context, name string) *publicv1.BillingAccount {
		GinkgoHelper()
		client := publicv1.NewBillingAccountsClient(tool.ExternalView().AdminConn())
		var account *publicv1.BillingAccount
		Eventually(ctx, func(g Gomega) {
			pollCtx, cancel := context.WithTimeout(ctx, billingPollRequestTimeout)
			defer cancel()
			response, err := client.List(pollCtx, publicv1.BillingAccountsListRequest_builder{
				Filter: new(fmt.Sprintf("this.status.display_name == %q", name)),
			}.Build())
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(response.GetItems()).To(HaveLen(1))
			account = response.GetItems()[0]
		}, time.Minute, time.Second).Should(Succeed())
		Expect(account.GetStatus().GetProviderKey()).To(Equal("m360"))
		Expect(account.GetStatus().GetState()).To(Equal(publicv1.BillingAccountState_BILLING_ACCOUNT_STATE_ACTIVE))
		Expect(account.GetStatus().GetStateObservationTimestamp()).ToNot(BeNil())
		if account.GetStatus().GetExternalBillingAccountId() != "" {
			correlationIDs = append(correlationIDs, account.GetStatus().GetExternalBillingAccountId())
		}
		return account
	}

	newTenant := func(ctx context.Context) string {
		GinkgoHelper()
		// Tenant references are tenant names, not object UUIDs.
		return newBillingTenant(ctx, nil, nil).GetMetadata().GetName()
	}

	// Prove assignment-time resolution reads the selected account and then scans for the exact correlation value.
	// M360 has no correlation-write operation: discovery, assignment, and remediation must all remain read-only.
	expectReadOnlyResolution := func(nativeID, value string, since int) {
		GinkgoHelper()
		requests := stub.Requests()[since:]
		path := "/api/v1/external/accounts/" + nativeID
		readAccount, scanned := false, false
		for _, request := range requests {
			if request.Failed {
				continue
			}
			if request.Method == "GET" && request.Op == "GetAccount" && request.Path == path {
				readAccount = true
			}
			if readAccount && request.Method == "GET" && request.Op == "ListAccounts" &&
				request.Path == "/api/v1/external/accounts" {
				query, err := url.ParseQuery(request.Query)
				Expect(err).ToNot(HaveOccurred())
				if query.Get("q") == value {
					scanned = true
				}
			}
		}
		Expect(value).ToNot(BeEmpty())
		Expect(readAccount).To(BeTrue(), "resolution must read the selected account through the real adapter")
		Expect(scanned).To(BeTrue(), "resolution must scan provider accounts for the exact correlation value")
		Expect(stub.Writes()).To(BeEmpty(), "OSAC must never attempt an M360 account PUT or any other provider write")
	}

	It("reads platform accounts and singleton status with saved customer/project selections in both API modes", func(ctx context.Context) {
		first, second := findAccount(ctx, accounts[0].Name), findAccount(ctx, accounts[1].Name)
		tenant := newTenant(ctx)
		for _, private := range []bool{false, true} {
			home := newBillingCLI(ctx, tenant, private)
			pkg := "osac.public.v1."
			if private {
				pkg = "osac.private.v1."
			}
			for _, format := range []string{"json", "yaml"} {
				// Keys are immutable OSAC id/name, not a display label, native id or attribution token.
				for _, key := range []string{first.GetId(), first.GetMetadata().GetName()} {
					items := billingCLIItems(ctx, home, "billingaccount", format, pkg+"BillingAccount", key,
						"--filter", `this.status.state == 1 && has(this.status.state_observation_timestamp) && this.status.state_observation_timestamp >= timestamp("2026-01-01T00:00:00Z")`)
					Expect(items).To(HaveLen(1))
					Expect(items[0]).To(HaveKeyWithValue("id", first.GetId()))
					Expect(items[0]).To(HaveKey("spec"))
					metadata := items[0]["metadata"].(map[string]any)
					Expect(metadata).To(HaveKeyWithValue("name", first.GetMetadata().GetName()))
					Expect(metadata).To(HaveKeyWithValue("tenant", "system"))
					Expect(metadata).ToNot(HaveKey("project"))
					observed := items[0]["status"].(map[string]any)
					Expect(observed).To(HaveKeyWithValue("display_name", accounts[0].Name))
					Expect(observed).To(HaveKeyWithValue("provider_external_id", accounts[0].ExternalID))
					Expect(observed).ToNot(HaveKey("external_billing_account_id"), "discovery must not establish attribution")
					Expect(observed).To(HaveKeyWithValue("state", "BILLING_ACCOUNT_STATE_ACTIVE"))
					if private {
						Expect(observed).To(HaveKeyWithValue("provider_account_reference", accounts[0].ID))
					} else {
						Expect(observed).ToNot(HaveKey("provider_account_reference"))
						Expect(metadata).ToNot(HaveKey("finalizers"))
					}
				}
				many := billingCLIItems(ctx, home, "billingaccounts", format, pkg+"BillingAccount",
					first.GetId(), second.GetMetadata().GetName())
				Expect(many).To(HaveLen(2))
				Expect([]any{many[0]["id"], many[1]["id"]}).To(ConsistOf(first.GetId(), second.GetId()))
				for _, key := range []string{accounts[0].Name, accounts[0].ID, accounts[0].ExternalID} {
					Expect(billingCLIItems(ctx, home, "billingaccount", format, pkg+"BillingAccount", key)).To(BeEmpty())
				}
				Expect(billingCLIItems(ctx, home, "billingaccount", format, pkg+"BillingAccount", first.GetId(),
					"--filter", fmt.Sprintf("this.metadata.tenant == %q", tenant))).To(BeEmpty(),
					"explicit caller scope must remain, even for a platform account")
				provider := billingCLIItems(ctx, home, "billingproviderstatus", format, pkg+"BillingProviderStatus")
				Expect(provider).To(HaveLen(1))
				Expect(provider[0]).To(HaveKeyWithValue("state", "BILLING_PROVIDER_STATE_AVAILABLE"))
				Expect(provider[0]).To(HaveKeyWithValue("provider_key", "m360"))
				Expect(provider[0]).To(HaveKeyWithValue("usage_delivery_state", "BILLING_USAGE_DELIVERY_STATE_UNKNOWN"))
			}
			headers := []string{
				"ID", "DELETING", "NAME", "PROVIDER", "DISPLAY-NAME", "CURRENCY", "STATE",
				"EXTERNAL-BILLING-ACCOUNT-ID", "STATUS-OBSERVED",
			}
			table := expectBillingCLITable(ctx, home, "billingaccount", headers, 2, first.GetId(), second.GetMetadata().GetName())
			Expect(table).To(ContainSubstring(accounts[0].Name))
			Expect(table).To(ContainSubstring(first.GetMetadata().GetName()))
			Expect(table).ToNot(ContainSubstring(accounts[0].ID), "private native identity is not a default column")
			expectBillingCLITable(ctx, home, "billingaccount", headers, 1, first.GetMetadata().GetName())
			expectBillingCLITable(ctx, home, "billingaccount", headers, 0, accounts[0].Name)
			expectBillingCLITable(ctx, home, "billingproviderstatus", []string{
				"STATE", "PROVIDER", "MESSAGE", "CAPABILITIES", "LAST-SUCCESSFUL-CHECK", "USAGE-DELIVERY",
			}, 1)
		}
		Expect(stub.Writes()).To(BeEmpty())
	})

	It("retains 1001 stored accounts across two legal provider discovery epochs and traverses every CLI page", func(ctx context.Context) {
		prefix := "billing-inventory-" + uuid.New() + "-"
		inventory := make([]billingfake.AccountSpec, 1001)
		for i := range inventory {
			inventory[i] = billingfake.AccountSpec{ID: uuid.New(), Name: fmt.Sprintf("%s%04d", prefix, i),
				ExternalID: fmt.Sprintf("inventory-attribution-%s%04d", prefix, i), RawStatus: "Active", ApprovalStatus: "Approved"}
		}
		// A single 1001-account provider walk is deliberately MALFORMED (20 x 50 maximum).
		// This positive case accumulates retained rows across two separately bounded walks instead.
		stub.SetScenario(billingfake.Scenario{Accounts: inventory[:1000]})
		client := publicv1.NewBillingAccountsClient(tool.ExternalView().AdminConn())
		filter := fmt.Sprintf("this.status.display_name.startsWith(%q)", prefix)
		eventuallyStored := func(total int32) {
			GinkgoHelper()
			Eventually(ctx, func(g Gomega) {
				pollCtx, cancel := context.WithTimeout(ctx, billingPollRequestTimeout)
				defer cancel()
				response, err := client.List(pollCtx, publicv1.BillingAccountsListRequest_builder{
					Filter: &filter, Limit: new(int32(37)),
				}.Build())
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(response.GetTotal()).To(Equal(total))
				g.Expect(response.GetSize()).To(Equal(int32(37)))
				g.Expect(response.GetItems()).To(HaveLen(37))
			}, time.Minute, time.Second).Should(Succeed())
		}
		eventuallyStored(1000)
		before := stub.Succeeded("ListAccounts")
		Expect(before).To(Equal(50), "the first legal discovery uses 50 ordinary 20-account pages")
		stub.SetScenario(billingfake.Scenario{Accounts: inventory[1000:]})
		// Poll List through the normal 30-second freshness window; no sleep, restart or cache override.
		eventuallyStored(1001)
		Expect(stub.Succeeded("ListAccounts")).To(BeNumerically(">", before))
		tenant := newTenant(ctx)
		home := newBillingCLI(ctx, tenant, false)
		for _, format := range []string{"json", "yaml"} {
			items := billingCLIItems(ctx, home, "billingaccounts", format, "osac.public.v1.BillingAccount", "--filter", filter)
			Expect(items).To(HaveLen(1001))
			ids, names := map[string]bool{}, map[string]bool{}
			unknown := 0
			for _, item := range items {
				id := item["id"].(string)
				Expect(id).ToNot(BeEmpty())
				Expect(ids).ToNot(HaveKey(id))
				ids[id] = true
				observed := item["status"].(map[string]any)
				name := observed["display_name"].(string)
				Expect(names).ToNot(HaveKey(name))
				names[name] = true
				if name == inventory[1000].Name {
					Expect(observed["state"]).To(Equal("BILLING_ACCOUNT_STATE_ACTIVE"))
				} else {
					Expect(observed["state"]).To(Equal("BILLING_ACCOUNT_STATE_UNKNOWN"))
					unknown++
				}
			}
			Expect(unknown).To(Equal(1000))
			for _, account := range inventory {
				Expect(names).To(HaveKey(account.Name))
			}
		}
		expectBillingCLITable(ctx, home, "billingaccounts", []string{
			"ID", "DELETING", "NAME", "PROVIDER", "DISPLAY-NAME", "CURRENCY", "STATE",
			"EXTERNAL-BILLING-ACCOUNT-ID", "STATUS-OBSERVED",
		}, 1001, "--filter", filter)
		end, err := client.List(ctx, publicv1.BillingAccountsListRequest_builder{
			Filter: &filter, Offset: new(int32(1001)), Limit: new(int32(37)),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(end.GetTotal()).To(Equal(int32(1001)))
		Expect(end.GetSize()).To(BeZero())
		Expect(end.GetItems()).To(BeEmpty())
		Expect(stub.Writes()).To(BeEmpty())
	})

	It("rejects an oversized single provider walk without printing partial CLI inventory", func(ctx context.Context) {
		inventory := make([]billingfake.AccountSpec, 1001)
		prefix := "billing-oversized-" + uuid.New() + "-"
		for i := range inventory {
			inventory[i] = billingfake.AccountSpec{ID: uuid.New(), Name: fmt.Sprintf("%s%04d", prefix, i),
				RawStatus: "Active", ApprovalStatus: "Approved"}
		}
		stub.SetScenario(billingfake.Scenario{Accounts: inventory})
		response, err := publicv1.NewBillingAccountsClient(tool.ExternalView().AdminConn()).List(ctx,
			publicv1.BillingAccountsListRequest_builder{}.Build())
		Expect(response).To(BeNil())
		expectBillingReason(err, "MALFORMED")
		Expect(stub.Succeeded("ListAccounts")).To(Equal(50))
		home := newBillingCLI(ctx, newTenant(ctx), false)
		for _, format := range []string{"table", "json", "yaml"} {
			// The failed API call above opens the deployment's discovery-failure backoff. Poll until a fresh walk is
			// admitted and prove the stub served that walk for this format instead of passing on UNAVAILABLE.
			Eventually(ctx, func(g Gomega) {
				before := stub.Succeeded("ListAccounts")
				pollCtx, cancel := context.WithTimeout(ctx, billingPollRequestTimeout)
				defer cancel()
				stdout, stderr, code := tool.RunCLI(pollCtx, home, "get", "billingaccounts", "-o", format)
				g.Expect(code).ToNot(Equal(0), "stderr: %s", stderr)
				g.Expect(stdout).To(BeEmpty(), "failed discovery must not emit partial inventory in %s", format)
				g.Expect(stderr).To(ContainSubstring("MALFORMED"), "stderr: %s", stderr)
				g.Expect(stub.Succeeded("ListAccounts")).To(Equal(before+50),
					"the %s request must serve a fresh oversized provider walk", format)
			}, time.Minute, time.Second).Should(Succeed())
		}
		Expect(stub.Writes()).To(BeEmpty())
	})

	It("observes provider IDs during discovery and establishes attribution on Create and reassignment Update using exact-match reads only", func(ctx context.Context) {
		first := findAccount(ctx, accounts[0].Name)
		second := findAccount(ctx, accounts[1].Name)
		Expect(first.GetId()).ToNot(BeEmpty())
		Expect(second.GetId()).ToNot(BeEmpty())
		Expect(first.GetId()).ToNot(BeElementOf(accounts[0].ID, accounts[1].ID))
		Expect(second.GetId()).ToNot(BeElementOf(accounts[0].ID, accounts[1].ID, first.GetId()))
		Expect(first.GetStatus().GetProviderExternalId()).To(Equal(accounts[0].ExternalID))
		Expect(second.GetStatus().GetProviderExternalId()).To(Equal(accounts[1].ExternalID))
		Expect(first.GetStatus().HasExternalBillingAccountId()).To(BeFalse())
		Expect(second.GetStatus().HasExternalBillingAccountId()).To(BeFalse())
		Expect(stub.Succeeded("ListAccounts")).To(BeNumerically(">", 0))
		Expect(stub.Succeeded("GetAccount")).To(BeZero(), "discovery must not resolve attribution")
		Expect(stub.Writes()).To(BeEmpty(), "discovery must not perform account writes")
		tenant := newTenant(ctx)
		home := newBillingCLI(ctx, tenant, false)
		client := publicv1.NewTenantBillingAssignmentsClient(tool.ExternalView().AdminConn())
		beforeCreate := len(stub.Requests())
		created, err := client.Create(ctx, publicv1.TenantBillingAssignmentsCreateRequest_builder{
			Object: publicv1.TenantBillingAssignment_builder{
				Metadata: publicv1.Metadata_builder{Name: "billing-" + uuid.New()}.Build(),
				Spec: publicv1.TenantBillingAssignmentSpec_builder{
					Tenant:         publicv1.TenantReference_builder{Id: tenant}.Build(),
					BillingAccount: publicv1.BillingAccountReference_builder{Id: first.GetId()}.Build(),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		assignment := created.GetObject()
		Expect(assignment.GetId()).ToNot(BeEmpty())
		Expect(assignment.GetSpec().GetTenant().GetId()).To(Equal(tenant))
		Expect(assignment.GetSpec().GetBillingAccount().GetId()).To(Equal(first.GetId()))
		Expect(assignment.GetStatus().GetConnectionState()).To(Equal(
			publicv1.TenantBillingAssignmentConnectionState_TENANT_BILLING_ASSIGNMENT_CONNECTION_STATE_CONNECTED))
		Expect(assignment.GetStatus().GetMessage()).To(BeEmpty())
		Expect(assignment.GetStatus().GetEffectiveFromTimestamp()).ToNot(BeNil())
		Expect(assignment.GetStatus().GetEffectiveToTimestamp()).To(BeNil())
		Expect(assignment.GetMetadata().GetCreator()).ToNot(BeEmpty())
		expectBillingAssignmentCLI(ctx, home, false, []*publicv1.TenantBillingAssignment{assignment}, assignment.GetId())
		expectReadOnlyResolution(accounts[0].ID, accounts[0].ExternalID, beforeCreate)
		refreshed := findAccount(ctx, accounts[0].Name)
		Expect(refreshed.GetId()).To(Equal(first.GetId()))
		firstToken := refreshed.GetStatus().GetExternalBillingAccountId()
		Expect(firstToken).To(Equal(accounts[0].ExternalID))
		Expect(firstToken).ToNot(BeElementOf(tenant, accounts[0].ID, first.GetId()))
		Expect(findAccount(ctx, accounts[1].Name).GetStatus().HasExternalBillingAccountId()).To(BeFalse(),
			"the substring match must not establish attribution for the unselected account")

		beforeUpdate := len(stub.Requests())
		updated, err := client.Update(ctx, publicv1.TenantBillingAssignmentsUpdateRequest_builder{
			Object: publicv1.TenantBillingAssignment_builder{
				Id: assignment.GetId(),
				Spec: publicv1.TenantBillingAssignmentSpec_builder{
					Tenant:         publicv1.TenantReference_builder{Id: tenant}.Build(),
					BillingAccount: publicv1.BillingAccountReference_builder{Id: second.GetId()}.Build(),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(updated.GetObject().GetId()).ToNot(Equal(assignment.GetId()))
		Expect(updated.GetObject().GetSpec().GetTenant().GetId()).To(Equal(tenant))
		Expect(updated.GetObject().GetSpec().GetBillingAccount().GetId()).To(Equal(second.GetId()))
		Expect(updated.GetObject().GetStatus().GetConnectionState()).To(Equal(
			publicv1.TenantBillingAssignmentConnectionState_TENANT_BILLING_ASSIGNMENT_CONNECTION_STATE_CONNECTED))
		Expect(updated.GetObject().GetStatus().GetMessage()).To(BeEmpty())
		Expect(updated.GetObject().GetStatus().GetEffectiveFromTimestamp()).ToNot(BeNil())
		Expect(updated.GetObject().GetStatus().GetEffectiveToTimestamp()).To(BeNil())
		expectReadOnlyResolution(accounts[1].ID, accounts[1].ExternalID, beforeUpdate)
		refreshed = findAccount(ctx, accounts[1].Name)
		Expect(refreshed.GetId()).To(Equal(second.GetId()))
		secondToken := refreshed.GetStatus().GetExternalBillingAccountId()
		Expect(secondToken).To(Equal(accounts[1].ExternalID))
		Expect(secondToken).ToNot(BeElementOf(tenant, accounts[1].ID, second.GetId(), firstToken))
		Expect(findAccount(ctx, accounts[0].Name).GetStatus().GetExternalBillingAccountId()).To(Equal(firstToken),
			"reassignment must not change the original account's attribution")
		old, err := client.Get(ctx, publicv1.TenantBillingAssignmentsGetRequest_builder{Id: assignment.GetId()}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(old.GetObject().GetSpec().GetBillingAccount().GetId()).To(Equal(first.GetId()))
		Expect(proto.Equal(old.GetObject().GetStatus().GetEffectiveFromTimestamp(),
			assignment.GetStatus().GetEffectiveFromTimestamp())).To(BeTrue())
		Expect(old.GetObject().GetStatus().GetEffectiveToTimestamp()).ToNot(BeNil())
		Expect(proto.Equal(old.GetObject().GetStatus().GetEffectiveToTimestamp(),
			updated.GetObject().GetStatus().GetEffectiveFromTimestamp())).To(BeTrue())
		history, err := client.List(ctx, publicv1.TenantBillingAssignmentsListRequest_builder{
			Filter: new(fmt.Sprintf("this.spec.tenant.id == %q", tenant)),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(history.GetItems()).To(HaveLen(2), "reassignment must retain both contiguous intervals")
		active := 0
		for _, interval := range history.GetItems() {
			if interval.GetStatus().GetEffectiveToTimestamp() == nil {
				active++
				Expect(interval.GetId()).To(Equal(updated.GetObject().GetId()))
			}
		}
		Expect(active).To(Equal(1), "exactly one interval must remain active after reassignment")
		Expect(history.GetSize()).To(Equal(int32(2)))
		Expect(history.GetTotal()).To(Equal(int32(2)))
		expectBillingAssignmentCLI(ctx, home, false, history.GetItems())
		expectBillingAssignmentCLI(ctx, home, false, []*publicv1.TenantBillingAssignment{old.GetObject()},
			assignment.GetMetadata().GetName(), "--filter", "has(this.status.effective_to_timestamp)")
		expectBillingAssignmentCLI(ctx, home, false, []*publicv1.TenantBillingAssignment{updated.GetObject()},
			"--filter", `this.status.connection_state == 2 && !has(this.status.effective_to_timestamp) && this.status.effective_from_timestamp >= timestamp("2026-01-01T00:00:00Z")`)
		privateHome := newBillingCLI(ctx, tenant, true)
		expectBillingAssignmentCLI(ctx, privateHome, true, history.GetItems(), assignment.GetId(), updated.GetObject().GetMetadata().GetName())
		expectBillingAssignmentCLI(ctx, privateHome, true, []*publicv1.TenantBillingAssignment{updated.GetObject()}, updated.GetObject().GetId())
		expectBillingAssignmentCLI(ctx, privateHome, true, nil, assignment.GetId(), "--filter", "!has(this.status.effective_to_timestamp)")
		otherHome := newBillingCLI(ctx, newTenant(ctx), false)
		expectBillingAssignmentCLI(ctx, otherHome, false, nil, assignment.GetId(), "--filter",
			fmt.Sprintf("this.spec.tenant.id == %q || this.id == %q", tenant, assignment.GetId()))
		// The admin is universal; the preceding empty result is a saved-selection intersection, not auth proof.
		// A real non-admin token must be denied by the billing method allow-list, not leak foreign history.
		_, err = publicv1.NewTenantBillingAssignmentsClient(tool.ExternalView().UserConn()).Get(ctx,
			publicv1.TenantBillingAssignmentsGetRequest_builder{Id: assignment.GetId()}.Build())
		Expect(status.Code(err)).To(Equal(codes.PermissionDenied))
		_, err = publicv1.NewTenantBillingAssignmentsClient(tool.ExternalView().UserConn()).List(ctx,
			publicv1.TenantBillingAssignmentsListRequest_builder{Filter: new(fmt.Sprintf("this.spec.tenant.id == %q", tenant))}.Build())
		Expect(status.Code(err)).To(Equal(codes.PermissionDenied))
		Expect(stub.Writes()).To(BeEmpty())
		expectCleanLog(ctx)
	})

	It("persists PENDING without a provider write and connects after external-admin remediation and Update", func(ctx context.Context) {
		accounts[0].ExternalID = ""
		stub.SetScenario(accountScenario())
		account := findAccount(ctx, accounts[0].Name)
		Expect(account.GetStatus().GetExternalBillingAccountId()).To(BeEmpty())
		tenant := newTenant(ctx)
		home := newBillingCLI(ctx, tenant, false)
		client := publicv1.NewTenantBillingAssignmentsClient(tool.ExternalView().AdminConn())
		beforeCreate := len(stub.Requests())
		created, err := client.Create(ctx, publicv1.TenantBillingAssignmentsCreateRequest_builder{
			Object: publicv1.TenantBillingAssignment_builder{
				Metadata: publicv1.Metadata_builder{Name: "billing-" + uuid.New(), Labels: map[string]string{"billing-cli": "configured"}}.Build(),
				Spec: publicv1.TenantBillingAssignmentSpec_builder{
					Tenant:         publicv1.TenantReference_builder{Id: tenant}.Build(),
					BillingAccount: publicv1.BillingAccountReference_builder{Id: account.GetId()}.Build(),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		pending := created.GetObject()
		Expect(pending.GetId()).ToNot(BeEmpty())
		Expect(pending.GetSpec().GetTenant().GetId()).To(Equal(tenant))
		Expect(pending.GetSpec().GetBillingAccount().GetId()).To(Equal(account.GetId()))
		Expect(pending.GetStatus().GetConnectionState()).To(Equal(
			publicv1.TenantBillingAssignmentConnectionState_TENANT_BILLING_ASSIGNMENT_CONNECTION_STATE_PENDING))
		remediation := billing.ReasonProviderCorrelationWriteUnsupported +
			": set externalId on the account in the provider to the external_billing_account_id shown by BillingAccounts.List"
		Expect(pending.GetStatus().GetMessage()).To(Equal(remediation))
		Expect(pending.GetStatus().GetEffectiveFromTimestamp()).ToNot(BeNil())
		Expect(pending.GetStatus().GetEffectiveToTimestamp()).To(BeNil())
		Expect(pending.GetMetadata().GetCreator()).ToNot(BeEmpty())
		expectBillingAssignmentCLI(ctx, home, false, []*publicv1.TenantBillingAssignment{pending}, pending.GetMetadata().GetName(),
			"--filter", `this.metadata.labels["billing-cli"] == "configured" && this.status.connection_state == 1 && !has(this.status.effective_to_timestamp)`)
		Expect(stub.Writes()).To(BeEmpty(), "unsupported capability must not attempt even a failed PUT")
		accountID := account.GetId()
		account = findAccount(ctx, accounts[0].Name)
		Expect(account.GetId()).To(Equal(accountID))
		Expect(account.GetStatus().GetExternalBillingAccountId()).ToNot(BeEmpty(), "PENDING must retain the OSAC-side correlation")
		Expect(account.GetStatus().GetExternalBillingAccountId()).ToNot(BeElementOf(tenant, accounts[0].ID, accountID, accounts[1].ExternalID))
		expectReadOnlyResolution(accounts[0].ID, account.GetStatus().GetExternalBillingAccountId(), beforeCreate)
		persisted, err := client.Get(ctx, publicv1.TenantBillingAssignmentsGetRequest_builder{Id: pending.GetId()}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(persisted.GetObject().GetStatus().GetConnectionState()).To(Equal(pending.GetStatus().GetConnectionState()))
		Expect(persisted.GetObject().GetStatus().GetMessage()).To(Equal(remediation))
		Expect(persisted.GetObject().GetSpec().GetBillingAccount().GetId()).To(Equal(accountID))

		// A retry without provider-side remediation stays PENDING and must not rotate the OSAC correlation or interval.
		beforeRetry := len(stub.Requests())
		retried, err := client.Update(ctx, publicv1.TenantBillingAssignmentsUpdateRequest_builder{Object: pending}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(retried.GetObject().GetId()).To(Equal(pending.GetId()))
		Expect(retried.GetObject().GetStatus().GetConnectionState()).To(Equal(pending.GetStatus().GetConnectionState()))
		Expect(retried.GetObject().GetStatus().GetMessage()).To(Equal(remediation))
		Expect(proto.Equal(retried.GetObject().GetStatus().GetEffectiveFromTimestamp(),
			pending.GetStatus().GetEffectiveFromTimestamp())).To(BeTrue())
		Expect(retried.GetObject().GetStatus().GetEffectiveToTimestamp()).To(BeNil())
		expectReadOnlyResolution(accounts[0].ID, account.GetStatus().GetExternalBillingAccountId(), beforeRetry)
		Expect(findAccount(ctx, accounts[0].Name).GetStatus().GetExternalBillingAccountId()).To(Equal(account.GetStatus().GetExternalBillingAccountId()))

		// Simulate the external provider admin, not an OSAC API write. Preserve every native ID, name and state;
		// only this account's provider-side correlation changes. SetScenario keeps the HTTP request log intact.
		accounts[0].ExternalID = account.GetStatus().GetExternalBillingAccountId()
		stub.SetScenario(accountScenario())
		persisted, err = client.Get(ctx, publicv1.TenantBillingAssignmentsGetRequest_builder{Id: pending.GetId()}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(persisted.GetObject().GetStatus().GetConnectionState()).To(Equal(pending.GetStatus().GetConnectionState()),
			"provider remediation alone must not replace assignment-time revalidation")
		beforeUpdate := len(stub.Requests())
		updated, err := client.Update(ctx, publicv1.TenantBillingAssignmentsUpdateRequest_builder{Object: pending}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(updated.GetObject().GetStatus().GetConnectionState()).To(Equal(
			publicv1.TenantBillingAssignmentConnectionState_TENANT_BILLING_ASSIGNMENT_CONNECTION_STATE_CONNECTED))
		Expect(updated.GetObject().GetStatus().GetMessage()).To(BeEmpty())
		Expect(updated.GetObject().GetId()).To(Equal(pending.GetId()))
		Expect(updated.GetObject().GetMetadata().GetCreator()).To(Equal(pending.GetMetadata().GetCreator()))
		Expect(updated.GetObject().GetSpec().GetBillingAccount().GetId()).To(Equal(accountID))
		Expect(proto.Equal(updated.GetObject().GetStatus().GetEffectiveFromTimestamp(),
			pending.GetStatus().GetEffectiveFromTimestamp())).To(BeTrue())
		Expect(updated.GetObject().GetStatus().GetEffectiveToTimestamp()).To(BeNil())
		expectReadOnlyResolution(accounts[0].ID, account.GetStatus().GetExternalBillingAccountId(), beforeUpdate)
		Expect(findAccount(ctx, accounts[0].Name).GetStatus().GetExternalBillingAccountId()).To(Equal(account.GetStatus().GetExternalBillingAccountId()))
		persisted, err = client.Get(ctx, publicv1.TenantBillingAssignmentsGetRequest_builder{Id: pending.GetId()}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(persisted.GetObject().GetStatus().GetConnectionState()).To(Equal(
			publicv1.TenantBillingAssignmentConnectionState_TENANT_BILLING_ASSIGNMENT_CONNECTION_STATE_CONNECTED))
		Expect(persisted.GetObject().GetStatus().GetMessage()).To(BeEmpty())
		history, err := client.List(ctx, publicv1.TenantBillingAssignmentsListRequest_builder{
			Filter: new(fmt.Sprintf("this.spec.tenant.id == %q", tenant)),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(history.GetItems()).To(HaveLen(1), "same-account revalidation must not create another interval")
		Expect(history.GetItems()[0].GetId()).To(Equal(pending.GetId()))
		Expect(history.GetTotal()).To(Equal(int32(1)))
		Expect(history.GetSize()).To(Equal(int32(1)))
		expectBillingAssignmentCLI(ctx, home, false, history.GetItems(), pending.GetId(),
			"--filter", `this.status.connection_state == 2 && !has(this.status.effective_to_timestamp)`)
		Expect(stub.Writes()).To(BeEmpty())
		expectCleanLog(ctx)
	})

	It("reads retained closed history through the CLI after the billed tenant is physically removed", func(ctx context.Context) {
		account := findAccount(ctx, accounts[0].Name)
		tenant := newBillingTenant(ctx, nil, nil)
		home := newBillingCLI(ctx, tenant.GetMetadata().GetName(), false)
		tenants := privatev1.NewTenantsClient(tool.InternalView().AdminConn())
		waitForTenantSyncedWithFinalizers(ctx, tenants, tenant.GetId())
		assignments := publicv1.NewTenantBillingAssignmentsClient(tool.ExternalView().AdminConn())
		created, err := assignments.Create(ctx, publicv1.TenantBillingAssignmentsCreateRequest_builder{
			Object: publicv1.TenantBillingAssignment_builder{
				Metadata: publicv1.Metadata_builder{Name: "billing-retained-" + uuid.New()}.Build(),
				Spec: publicv1.TenantBillingAssignmentSpec_builder{
					Tenant:         publicv1.TenantReference_builder{Name: tenant.GetMetadata().GetName()}.Build(),
					BillingAccount: publicv1.BillingAccountReference_builder{Id: account.GetId()}.Build(),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		// The established lifecycle helper also removes named projects: the tenant
		// controller deliberately waits for those, including our saved CLI selection.
		deleteTenant(ctx, tenants, privatev1.NewProjectsClient(tool.InternalView().AdminConn()),
			tenant.GetId(), tenant.GetMetadata().GetName())
		_, err = tenants.Get(ctx, privatev1.TenantsGetRequest_builder{Id: tenant.GetId()}.Build())
		Expect(status.Code(err)).To(Equal(codes.NotFound))
		retained, err := assignments.Get(ctx, publicv1.TenantBillingAssignmentsGetRequest_builder{Id: created.GetObject().GetId()}.Build())
		Expect(err).ToNot(HaveOccurred(), "history must not re-resolve a live tenant")
		Expect(retained.GetObject().GetStatus().GetEffectiveToTimestamp()).ToNot(BeNil())
		Expect(retained.GetObject().GetMetadata().GetCreator()).To(Equal(created.GetObject().GetMetadata().GetCreator()))
		Expect(proto.Equal(retained.GetObject().GetStatus().GetEffectiveFromTimestamp(),
			created.GetObject().GetStatus().GetEffectiveFromTimestamp())).To(BeTrue())
		listed, err := assignments.List(ctx, publicv1.TenantBillingAssignmentsListRequest_builder{
			Filter: new(fmt.Sprintf("this.metadata.tenant == %q", tenant.GetMetadata().GetName())),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(listed.GetSize()).To(Equal(int32(1)))
		Expect(listed.GetTotal()).To(Equal(int32(1)))
		Expect(listed.GetItems()).To(HaveLen(1))
		Expect(proto.Equal(listed.GetItems()[0], retained.GetObject())).To(BeTrue())
		expectBillingAssignmentCLI(ctx, home, false, listed.GetItems(), retained.GetObject().GetMetadata().GetName(),
			"--filter", "has(this.status.effective_to_timestamp)")
		expectBillingAssignmentCLI(ctx, home, false, nil, "--filter", "!has(this.status.effective_to_timestamp)")
		Expect(stub.Writes()).To(BeEmpty())
	})

	It("is AVAILABLE when the stub provider answers, using reads only", func(ctx context.Context) {
		Eventually(ctx, func(g Gomega) {
			object, err := billingStubStatus(ctx)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(object.GetState()).To(Equal(publicv1.BillingProviderState_BILLING_PROVIDER_STATE_AVAILABLE))
		}, time.Minute, time.Second).Should(Succeed())
		Expect(stub.Succeeded("HealthCheck")).To(BeNumerically(">", 0))
		// The pod sends the mounted credential to the configured API path.
		Expect(stub.Requests()).To(ContainElement(SatisfyAll(HaveField("Op", "HealthCheck"),
			HaveField("Path", HavePrefix("/api/v1/external/")), HaveField("Authorization", "Bearer "+billingStubToken))))
		Expect(stub.Writes()).To(BeEmpty())
		expectCleanLog(ctx)
	})

	It("is UNAVAILABLE when the stub provider fails its health check", func(ctx context.Context) {
		stub.SetScenario(billingfake.Scenario{Failures: map[string]billingfake.Failure{
			"HealthCheck": {Reason: billing.ReasonUnavailable, Status: 503}}})
		Eventually(ctx, func(g Gomega) {
			object, err := billingStubStatus(ctx)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(object.GetState()).To(Equal(publicv1.BillingProviderState_BILLING_PROVIDER_STATE_UNAVAILABLE))
			g.Expect(object.GetMessage()).To(Equal("billing provider account API is unavailable"))
		}, time.Minute, time.Second).Should(Succeed())
		// A failed HealthCheck the stub served proves the probe reached it, not a config or TLS rejection.
		Expect(stub.Requests()).To(ContainElement(SatisfyAll(HaveField("Op", "HealthCheck"),
			HaveField("Failed", true))))
		Expect(stub.Writes()).To(BeEmpty())
		expectCleanLog(ctx)
	})
})
