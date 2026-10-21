/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package idp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Service-account Keycloak client", func() {
	const clientUUID = "d3c4e3db-95b8-481d-8b5c-963da477da12"
	const clientID = "osac-sa-tenant-a-e80e9561-48cc-41c9-89b4-8c9a944b45a5"
	const credential = "test-only-credential-marker"
	var client *Client
	var server *httptest.Server
	var calls []string
	var created map[string]any
	var mappers []map[string]any
	var failure string
	var failureStatus int
	var lookup []map[string]string
	var malformed string
	var emptyCredential bool
	var cancelCreate context.CancelFunc
	var logs bytes.Buffer

	BeforeEach(func() {
		calls = nil
		created = nil
		mappers = nil
		failure = ""
		failureStatus = http.StatusInternalServerError
		lookup = []map[string]string{{"id": clientUUID, "clientId": clientID}}
		malformed = ""
		emptyCredential = false
		cancelCreate = nil
		logs.Reset()
		server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer GinkgoRecover()
			call := r.Method + " " + r.URL.Path
			calls = append(calls, call)
			Expect(r.Header.Get("Authorization")).To(Equal("Bearer test-token"))
			if call == malformed {
				_, _ = fmt.Fprint(w, "invalid JSON "+credential)
				return
			}
			if call == failure {
				w.WriteHeader(failureStatus)
				_, _ = fmt.Fprint(w, credential)
				return
			}
			switch call {
			case "POST /admin/realms/osac/clients":
				Expect(json.NewDecoder(r.Body).Decode(&created)).To(Succeed())
				if cancelCreate != nil {
					cancelCreate()
					<-r.Context().Done()
					return
				}
				w.Header().Set("Location", server.URL+"/admin/realms/osac/clients/"+clientUUID)
				w.WriteHeader(http.StatusCreated)
			case "GET /admin/realms/osac/clients":
				Expect(r.URL.Query().Get("clientId")).To(Equal(clientID))
				Expect(json.NewEncoder(w).Encode(lookup)).To(Succeed())
			case "POST /admin/realms/osac/clients/" + clientUUID + "/protocol-mappers/add-models":
				Expect(json.NewDecoder(r.Body).Decode(&mappers)).To(Succeed())
				w.WriteHeader(http.StatusCreated)
			case "GET /admin/realms/osac/clients/" + clientUUID + "/client-secret":
				value := credential
				if emptyCredential {
					value = ""
				}
				Expect(json.NewEncoder(w).Encode(map[string]string{"type": "secret", "value": value})).To(Succeed())
			case "PUT /admin/realms/osac/clients/" + clientUUID:
				var update map[string]any
				Expect(json.NewDecoder(r.Body).Decode(&update)).To(Succeed())
				Expect(update).To(HaveKey("enabled"))
				if update["enabled"] == true {
					Expect(created["enabled"]).To(BeFalse())
					Expect(mappers).To(HaveLen(2))
				}
				w.WriteHeader(http.StatusNoContent)
			case "DELETE /admin/realms/osac/clients/" + clientUUID:
				w.WriteHeader(http.StatusNoContent)
			default:
				Fail("unexpected request: " + call)
			}
		}))
		DeferCleanup(server.Close)
		var err error
		client, err = NewClient().SetLogger(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))).
			SetBaseURL(server.URL).SetTokenSource(&staticTokenSource{}).Build()
		Expect(err).ToNot(HaveOccurred())
	})

	It("configures a disabled confidential identity before enabling it and returning its initial secret", func() {
		id, secret, err := client.CreateServiceAccountClient(context.Background(), clientID, "tenant-a")
		Expect(err).ToNot(HaveOccurred())
		Expect(id).To(Equal(clientUUID))
		Expect(secret).To(Equal(credential))
		Expect(created).To(HaveKeyWithValue("clientId", clientID))
		Expect(created).To(HaveKeyWithValue("protocol", "openid-connect"))
		for _, flag := range []string{"enabled", "publicClient", "standardFlowEnabled", "implicitFlowEnabled", "directAccessGrantsEnabled", "fullScopeAllowed"} {
			Expect(created).To(HaveKeyWithValue(flag, false))
		}
		Expect(created).To(HaveKeyWithValue("serviceAccountsEnabled", true))
		Expect(created).To(HaveKeyWithValue("clientAuthenticatorType", "client-secret"))
		Expect(created).To(HaveKeyWithValue("defaultClientScopes", BeEmpty()))
		Expect(created).To(HaveKeyWithValue("optionalClientScopes", BeEmpty()))
		Expect(mappers).To(ConsistOf(
			HaveKeyWithValue("protocolMapper", "oidc-hardcoded-claim-mapper"),
			HaveKeyWithValue("protocolMapper", "oidc-audience-mapper"),
		))
		for _, mapper := range mappers {
			config := mapper["config"].(map[string]any)
			Expect(mapper["protocol"]).To(Equal("openid-connect"))
			Expect(config["access.token.claim"]).To(Equal("true"))
			switch mapper["protocolMapper"] {
			case "oidc-hardcoded-claim-mapper":
				Expect(config["claim.name"]).To(Equal("osac_tenant"))
				Expect(config["claim.value"]).To(Equal("tenant-a"))
				Expect(config["jsonType.label"]).To(Equal("String"))
			case "oidc-audience-mapper":
				Expect(config["included.custom.audience"]).To(Equal("osac-api"))
			}
		}
		Expect(calls[len(calls)-1]).To(Equal("PUT /admin/realms/osac/clients/" + clientUUID))
		Expect(strings.Contains(logs.String(), credential)).To(BeFalse(), "logs must omit credential material")
	})
	DescribeTable("returns a cleanup UUID and no secret after partial setup failure", func(step string) {
		failure = step
		id, secret, err := client.CreateServiceAccountClient(context.Background(), clientID, "tenant-a")
		Expect(err).To(HaveOccurred())
		Expect(id).To(Equal(clientUUID))
		Expect(secret).To(BeEmpty())
		Expect(strings.Contains(err.Error(), credential)).To(BeFalse(), "errors must omit upstream response bodies")
		Expect(strings.Contains(logs.String(), credential)).To(BeFalse(), "logs must omit credential material")
	},
		Entry("mapper", "POST /admin/realms/osac/clients/"+clientUUID+"/protocol-mappers/add-models"),
		Entry("secret retrieval", "GET /admin/realms/osac/clients/"+clientUUID+"/client-secret"),
		Entry("enable", "PUT /admin/realms/osac/clients/"+clientUUID),
	)
	It("never adopts an existing client after a definite conflict", func() {
		failure = "POST /admin/realms/osac/clients"
		failureStatus = http.StatusConflict
		id, secret, err := client.CreateServiceAccountClient(context.Background(), clientID, "tenant-a")
		Expect(err).To(HaveOccurred())
		Expect(id).To(BeEmpty())
		Expect(secret).To(BeEmpty())
		Expect(calls).To(Equal([]string{failure}))
	})
	DescribeTable("rejects malformed responses without returning a secret", func(step string) {
		malformed = step
		_, secret, err := client.CreateServiceAccountClient(context.Background(), clientID, "tenant-a")
		Expect(err).To(HaveOccurred())
		Expect(secret).To(BeEmpty())
		Expect(strings.Contains(err.Error(), credential)).To(BeFalse())
		Expect(calls).ToNot(ContainElement("PUT /admin/realms/osac/clients/" + clientUUID))
	}, Entry("lookup", "GET /admin/realms/osac/clients"),
		Entry("credential", "GET /admin/realms/osac/clients/"+clientUUID+"/client-secret"))
	It("does not enable a client when the initial secret is missing", func() {
		emptyCredential = true
		id, secret, err := client.CreateServiceAccountClient(context.Background(), clientID, "tenant-a")
		Expect(err).To(HaveOccurred())
		Expect(id).To(Equal(clientUUID))
		Expect(secret).To(BeEmpty())
		Expect(calls).ToNot(ContainElement("PUT /admin/realms/osac/clients/" + clientUUID))
	})
	It("recovers the cleanup UUID after the creating request is cancelled", func() {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		cancelCreate = cancel
		id, secret, err := client.CreateServiceAccountClient(ctx, clientID, "tenant-a")
		Expect(err).To(MatchError(ContainSubstring("context canceled")))
		Expect(id).To(Equal(clientUUID))
		Expect(secret).To(BeEmpty())
		Expect(calls).To(Equal([]string{"POST /admin/realms/osac/clients", "GET /admin/realms/osac/clients"}))
	})
	It("resolves an ambiguous create failure only by the exact generated identity", func() {
		failure = "POST /admin/realms/osac/clients"
		id, secret, err := client.CreateServiceAccountClient(context.Background(), clientID, "tenant-a")
		Expect(err).To(HaveOccurred())
		Expect(id).To(Equal(clientUUID))
		Expect(secret).To(BeEmpty())
		Expect(calls).To(Equal([]string{failure, "GET /admin/realms/osac/clients"}))
	})
	DescribeTable("never returns an unrelated or ambiguous recovery target", func(results []map[string]string) {
		failure = "POST /admin/realms/osac/clients"
		lookup = results
		id, secret, err := client.CreateServiceAccountClient(context.Background(), clientID, "tenant-a")
		Expect(err).To(HaveOccurred())
		Expect(id).To(BeEmpty())
		Expect(secret).To(BeEmpty())
	},
		Entry("missing", []map[string]string{}),
		Entry("different client ID", []map[string]string{{"id": clientUUID, "clientId": "unrelated"}}),
		Entry("invalid UUID", []map[string]string{{"id": "../unrelated", "clientId": clientID}}),
		Entry("duplicate exact matches", []map[string]string{{"id": clientUUID, "clientId": clientID}, {"id": clientUUID, "clientId": clientID}}),
	)
	It("disables and deletes only the stored client UUID", func() {
		Expect(client.DisableServiceAccountClient(context.Background(), clientUUID)).To(Succeed())
		Expect(client.DeleteServiceAccountClient(context.Background(), clientUUID)).To(Succeed())
		Expect(calls).To(Equal([]string{"PUT /admin/realms/osac/clients/" + clientUUID, "DELETE /admin/realms/osac/clients/" + clientUUID}))
	})
	DescribeTable("treats absent clients as successfully cleaned up", func(method string) {
		failure = method + " /admin/realms/osac/clients/" + clientUUID
		failureStatus = http.StatusNotFound
		if method == "PUT" {
			Expect(client.DisableServiceAccountClient(context.Background(), clientUUID)).To(Succeed())
		} else {
			Expect(client.DeleteServiceAccountClient(context.Background(), clientUUID)).To(Succeed())
		}
	}, Entry("disable", "PUT"), Entry("delete", "DELETE"))
	DescribeTable("reports cleanup failures without exposing server bodies", func(method string) {
		failure = method + " /admin/realms/osac/clients/" + clientUUID
		var err error
		if method == "PUT" {
			err = client.DisableServiceAccountClient(context.Background(), clientUUID)
		} else {
			err = client.DeleteServiceAccountClient(context.Background(), clientUUID)
		}
		Expect(err).To(HaveOccurred())
		Expect(strings.Contains(err.Error(), credential)).To(BeFalse())
	}, Entry("disable", "PUT"), Entry("delete", "DELETE"))
	It("rejects invalid cleanup UUIDs before making a request", func() {
		for _, id := range []string{"", "osac-admin", "../clients", "client/uuid"} {
			Expect(client.DisableServiceAccountClient(context.Background(), id)).To(HaveOccurred())
			Expect(client.DeleteServiceAccountClient(context.Background(), id)).To(HaveOccurred())
		}
		Expect(calls).To(BeEmpty())
	})
})
