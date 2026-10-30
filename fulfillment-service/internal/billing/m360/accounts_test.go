/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package m360

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"time"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/ginkgo/v2/dsl/table"
	. "github.com/onsi/gomega"

	"github.com/osac-project/osac/fulfillment-service/internal/billing"
)

var _ = Describe("M360 accounts", func() {
	var (
		fake    *fakeServer
		adapter billing.Adapter
		queries chan url.Values
	)

	build := func(h http.HandlerFunc) {
		queries = make(chan url.Values, 100)
		fake = newFakeServer(func(w http.ResponseWriter, r *http.Request) {
			queries <- r.URL.Query()
			h(w, r)
		})
		var err error
		adapter, err = New(billing.Config{BaseURL: fake.URL, APIVersion: "v1", Credential: testToken}).
			SetLogger(logger).Build()
		Expect(err).ToNot(HaveOccurred())
	}

	AfterEach(func() {
		fake.expectOnlyGET()
	})

	Describe("ListAccounts", func() {
		It("reads raw status names from the __r objects and returns the next page token", func() {
			build(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(listBody(0, false, `[
					{"Id":"a1","Name":"Acme","ExternalId":"ext-1","Status":null,"Status__r":null,
					 "ApprovalStatus":"7d1b","ApprovalStatus__r":{"Name":"Approved"}},
					{"Id":"a2","Name":"Beta","Status":"3c9e","Status__r":{"Name":"Weird"},"ApprovalStatus":"7d1b",
					 "ApprovalStatus__r":{"Name":"Pending"}}]`)))
			})
			page, err := adapter.ListAccounts(context.Background(), billing.AccountFilter{}, billing.Page{Size: 5, Token: "3"})
			Expect(err).ToNot(HaveOccurred())
			Expect(page.Next).To(Equal("1"))
			Expect(page.Accounts).To(HaveLen(2))
			Expect(page.Accounts[0].ExternalID).To(Equal("ext-1"))
			Expect(page.Accounts[0].RawStatus).To(BeEmpty(), "a null Status has no readable value; the UUID is never exposed")
			Expect(page.Accounts[0].ApprovalStatus).To(Equal("Approved"))
			Expect(page.Accounts[0].Ref.ID()).To(Equal("a1"))
			Expect(page.Accounts[1].ExternalID).To(BeEmpty())
			Expect(page.Accounts[1].RawStatus).To(Equal("Weird"))
			Expect(page.Accounts[1].ApprovalStatus).To(Equal("Pending"))
			q := <-queries
			Expect(q.Get("size")).To(Equal("5"))
			Expect(q.Get("page")).To(Equal("3"))
			Expect(q.Has("pageSize")).To(BeFalse(), "M360 ignores pageSize")
		})

		It("leaves the normalized state UNKNOWN until the design owner defines the mapping", func() {
			build(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(listBody(0, true, `[{"Id":"a1","Status__r":{"Name":"Active"}}]`)))
			})
			page, err := adapter.ListAccounts(context.Background(), billing.AccountFilter{}, billing.Page{})
			Expect(err).ToNot(HaveOccurred())
			Expect(page.Accounts[0].RawStatus).To(Equal("Active"))
			Expect(page.Accounts[0].State).To(Equal(billing.AccountUnknown))
		})

		It("ends paging on the last page", func() {
			build(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(listBody(1, true, `[{"Id":"a21"}]`)))
			})
			page, err := adapter.ListAccounts(context.Background(), billing.AccountFilter{}, billing.Page{Token: "1"})
			Expect(err).ToNot(HaveOccurred())
			Expect(page.Next).To(BeEmpty())
		})

		DescribeTable("clamps the page size to the provider maximum of 20",
			func(requested int, sent int) {
				build(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(listBody(0, true, `[]`))) })
				_, err := adapter.ListAccounts(context.Background(), billing.AccountFilter{}, billing.Page{Size: requested})
				Expect(err).ToNot(HaveOccurred())
				Expect((<-queries).Get("size")).To(Equal(strconv.Itoa(sent)))
			},
			Entry("zero means max", 0, 20),
			Entry("negative means max", -3, 20),
			Entry("over max", 500, 20),
			Entry("within bounds", 7, 7),
		)

		It("returns an empty page without error when the provider has no accounts", func() {
			build(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(listBody(0, true, `[]`))) })
			page, err := adapter.ListAccounts(context.Background(), billing.AccountFilter{}, billing.Page{})
			Expect(err).ToNot(HaveOccurred())
			Expect(page.Accounts).To(BeEmpty())
			Expect(page.Next).To(BeEmpty())
		})

		It("passes duplicate accounts through unchanged", func() {
			build(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(listBody(0, true, `[{"Id":"a1"},{"Id":"a1"}]`)))
			})
			page, err := adapter.ListAccounts(context.Background(), billing.AccountFilter{}, billing.Page{})
			Expect(err).ToNot(HaveOccurred())
			Expect(page.Accounts).To(HaveLen(2))
		})

		It("fails instead of dropping an account that has no id", func() {
			build(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(listBody(0, true, `[{"Name":"Acme"}]`)))
			})
			_, err := adapter.ListAccounts(context.Background(), billing.AccountFilter{}, billing.Page{})
			Expect(reasonOf(err)).To(Equal(billing.ReasonMalformed))
		})

		It("fails instead of returning no accounts when the body is not an M360 page", func() {
			build(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(`{"Items":[{"Id":"a1"}],"NextPage":"2"}`))
			})
			page, err := adapter.ListAccounts(context.Background(), billing.AccountFilter{}, billing.Page{})
			Expect(reasonOf(err)).To(Equal(billing.ReasonMalformed))
			Expect(page.Accounts).To(BeNil())
		})

		It("searches with q and keeps only exact externalId matches", func() {
			build(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(listBody(0, false, `[
					{"Id":"a1","ExternalId":"ext-1","Name":"Acme"},
					{"Id":"a2","ExternalId":"ext-10","Name":"Acme 2"}]`)))
			})
			page, err := adapter.ListAccounts(context.Background(), billing.AccountFilter{ExternalID: "ext-1"}, billing.Page{})
			Expect(err).ToNot(HaveOccurred())
			Expect((<-queries).Get("q")).To(Equal("ext-1"))
			Expect(page.Accounts).To(HaveLen(1))
			Expect(page.Accounts[0].Ref.ID()).To(Equal("a1"))
			Expect(page.Next).To(Equal("1"), "paging must continue even after a filtered page")
		})

		It("filters by exact name and requires every set field to match", func() {
			build(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(listBody(0, true, `[
					{"Id":"a1","ExternalId":"ext-1","Name":"Acme"},
					{"Id":"a2","ExternalId":"ext-2","Name":"Acme"},
					{"Id":"a3","ExternalId":"ext-3","Name":"Acme Corp"}]`)))
			})
			page, err := adapter.ListAccounts(context.Background(), billing.AccountFilter{Name: "Acme"}, billing.Page{})
			Expect(err).ToNot(HaveOccurred())
			Expect((<-queries).Get("q")).To(Equal("Acme"))
			Expect(page.Accounts).To(HaveLen(2))

			page, err = adapter.ListAccounts(context.Background(), billing.AccountFilter{Name: "Acme", ExternalID: "ext-2"}, billing.Page{})
			Expect(err).ToNot(HaveOccurred())
			Expect(page.Accounts).To(HaveLen(1))
			Expect(page.Accounts[0].Ref.ID()).To(Equal("a2"))
		})

		It("surfaces provider failure rather than an empty list", func() {
			build(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(401) })
			page, err := adapter.ListAccounts(context.Background(), billing.AccountFilter{}, billing.Page{})
			Expect(reasonOf(err)).To(Equal(billing.ReasonUnauthorized))
			Expect(page.Accounts).To(BeNil())
		})
	})

	Describe("GetAccount and CorrelationValue", func() {
		It("reads one account by its native id from the external API", func() {
			var path string
			build(func(w http.ResponseWriter, r *http.Request) {
				path = r.URL.EscapedPath()
				_, _ = w.Write([]byte(dataBody(`{"Id":"a/1","ExternalId":"ext-1","Status__r":{"Name":"Inactive"},
					"ApprovalStatus__r":{"Name":"Approved"}}`)))
			})
			acct, err := adapter.GetAccount(context.Background(), billing.NewAccountRef("a/1"))
			Expect(err).ToNot(HaveOccurred())
			Expect(path).To(Equal("/api/v1/external/accounts/a%2F1"))
			Expect(acct.RawStatus).To(Equal("Inactive"))
			Expect(acct.ApprovalStatus).To(Equal("Approved"))
			Expect(acct.State).To(Equal(billing.AccountUnknown))
		})

		It("tells a missing account (NOT_FOUND) apart from other failures", func() {
			build(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(404) })
			_, err := adapter.GetAccount(context.Background(), billing.NewAccountRef("a1"))
			Expect(reasonOf(err)).To(Equal(billing.ReasonNotFound))
			_, _, err = adapter.CorrelationValue(context.Background(), billing.NewAccountRef("a1"))
			Expect(reasonOf(err)).To(Equal(billing.ReasonNotFound))
		})

		It("keeps a 404 on the account collection route as MALFORMED", func() {
			build(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(404) })
			_, err := adapter.ListAccounts(context.Background(), billing.AccountFilter{}, billing.Page{})
			Expect(reasonOf(err)).To(Equal(billing.ReasonMalformed))
		})

		It("reports the correlation value as present when ExternalId is set", func() {
			build(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(dataBody(`{"Id":"a1","ExternalId":"ext-1"}`)))
			})
			v, ok, err := adapter.CorrelationValue(context.Background(), billing.NewAccountRef("a1"))
			Expect(err).ToNot(HaveOccurred())
			Expect(ok).To(BeTrue())
			Expect(v).To(Equal("ext-1"))
		})

		It("reports the correlation value as absent when ExternalId is missing", func() {
			build(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(dataBody(`{"Id":"a1"}`))) })
			v, ok, err := adapter.CorrelationValue(context.Background(), billing.NewAccountRef("a1"))
			Expect(err).ToNot(HaveOccurred())
			Expect(ok).To(BeFalse())
			Expect(v).To(BeEmpty())
		})

		It("propagates lookup failure from CorrelationValue", func() {
			build(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(409) })
			_, _, err := adapter.CorrelationValue(context.Background(), billing.NewAccountRef("a1"))
			Expect(reasonOf(err)).To(Equal(billing.ReasonConflict))
		})
	})

	Describe("correlation write", func() {
		It("is unsupported and never contacts the provider", func() {
			build(func(http.ResponseWriter, *http.Request) {})
			Expect(adapter.SupportsCorrelationWrite()).To(BeFalse())
			err := adapter.SetCorrelationValue(context.Background(), billing.NewAccountRef("a1"), "v")
			Expect(reasonOf(err)).To(Equal(billing.ReasonUnsupported))
			Expect(fake.hits.Load()).To(BeZero())
		})
	})

	Describe("HealthCheck", func() {
		It("succeeds when the account API answers", func() {
			build(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(listBody(0, true, `[]`))) })
			Expect(adapter.HealthCheck(context.Background())).To(Succeed())
			Expect((<-queries).Get("size")).To(Equal("1"))
		})

		It("fails when the account API rejects the credential", func() {
			build(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(401) })
			Expect(reasonOf(adapter.HealthCheck(context.Background()))).To(Equal(billing.ReasonUnauthorized))
		})

		It("fails MALFORMED when a 2xx body reports failure", func() {
			build(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(`{"success":false,"errors":[{"code":"X"}],"data":null}`))
			})
			Expect(reasonOf(adapter.HealthCheck(context.Background()))).To(Equal(billing.ReasonMalformed))
		})

		It("gives up quickly on a hung provider", func() {
			build(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() })
			ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
			defer cancel()
			Expect(adapter.HealthCheck(ctx)).To(HaveOccurred())
		})
	})
})

var _ = It("M360 refuses to build without complete config", func() {
	_, err := New(billing.Config{}).Build()
	Expect(reasonOf(err)).To(Equal(billing.ReasonNotConfigured))
})
