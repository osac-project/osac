/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package realmauth

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"syscall"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const (
	csiRoleUUID   = "c4d5e6f7-a8b9-0c1d-2e3f-456789abcdef"
	osacRealmUUID = "01e6c34f-6906-4907-9cbd-2dec7a6a0e1d"
	scopePath     = "/admin/realms/osac/clients/" + csiClientUUID + "/scope-mappings/realm"
)

type scopeCLIResult struct {
	Result  string `json:"result"`
	Changed bool   `json:"changed"`
}

// Every test executes the production entry point. Never put the bearer in argv
// or let failed assertions print subprocess output, response bodies or records.
func runScopeCLI(ctx context.Context, server, tokenFile, caFile string, args ...string) (scopeCLIResult, int, string) {
	GinkgoHelper()
	argv := []string{"../../../osac-installer/scripts/update-csi-realm-scope.py", "--server", server,
		"--token-file", tokenFile}
	if caFile != "" {
		argv = append(argv, "--ca-file", caFile)
	}
	argv = append(argv, args...)
	output, err := exec.CommandContext(ctx, "python3", argv...).CombinedOutput()
	exit := 0
	if err != nil {
		var failure *exec.ExitError
		if errors.As(err, &failure) {
			exit = failure.ExitCode()
		} else {
			Fail("updater could not execute (details withheld)")
		}
	}
	var result scopeCLIResult
	if exit == 0 {
		parsed := json.Unmarshal(output, &result) == nil
		Expect(parsed).To(BeTrue(), "updater success output must be structured (output withheld)")
	}
	return result, exit, string(output)
}

func roleRepresentation(id, name string) map[string]any {
	return map[string]any{"id": id, "name": name, "composite": false, "clientRole": false, "containerId": osacRealmUUID}
}

type scopeProtocolFixture struct {
	mu                             sync.Mutex
	server                         *httptest.Server
	dir, caFile, tokenFile, bearer string
	client, role                   map[string]any
	clients                        any
	scopes                         []any
	calls                          []string
	writes, violations, scopeReads int
	failureMethod, failurePath     string
	failureStatus                  int
	failureBody, redirect          string
	delay                          time.Duration
	changeOnRead                   int
}

func newScopeProtocolFixture() *scopeProtocolFixture {
	GinkgoHelper()
	f := &scopeProtocolFixture{dir: GinkgoT().TempDir(), bearer: rand.Text()}
	f.client = map[string]any{
		"id": csiClientUUID, "clientId": "osac-csi-driver", "protocol": "openid-connect",
		"enabled": true, "fullScopeAllowed": false, "publicClient": false, "bearerOnly": false,
		"serviceAccountsEnabled": true, "clientAuthenticatorType": "client-secret",
		"standardFlowEnabled": false, "implicitFlowEnabled": false, "directAccessGrantsEnabled": false,
		"defaultClientScopes": []string{"roles", "basic"}, "optionalClientScopes": []string{},
		// A sensitive response property must not be logged or placed in a record.
		"secret": rand.Text(),
	}
	f.role = roleRepresentation(csiRoleUUID, "osac-csi-driver")
	f.clients = []any{f.client}
	f.scopes = []any{roleRepresentation(sentinelRole, sentinelName)}
	f.server = httptest.NewUnstartedServer(http.HandlerFunc(f.serve))
	f.server.Config.ErrorLog = log.New(io.Discard, "", 0)
	f.server.StartTLS()
	DeferCleanup(f.server.Close)
	f.caFile, f.tokenFile = filepath.Join(f.dir, "ca.pem"), filepath.Join(f.dir, "token")
	cert := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: f.server.Certificate().Raw})
	Expect(os.WriteFile(f.caFile, cert, 0600)).To(Succeed())
	Expect(os.WriteFile(f.tokenFile, []byte(f.bearer), 0600)).To(Succeed())
	return f
}

func (f *scopeProtocolFixture) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	path := r.URL.Path
	f.calls = append(f.calls, r.Method+" "+path)
	if r.Header.Get("Authorization") != "Bearer "+f.bearer {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	if f.delay > 0 {
		time.Sleep(f.delay)
	}
	if r.Method == f.failureMethod && (f.failurePath == "" || path == f.failurePath) {
		if f.redirect != "" {
			w.Header().Set("Location", f.redirect)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(f.failureStatus)
		_, _ = io.WriteString(w, f.failureBody)
		return
	}
	var response any
	switch {
	case r.Method == http.MethodGet && path == "/admin/realms/osac/clients":
		if r.URL.Query().Get("clientId") != "osac-csi-driver" || r.URL.Query().Get("max") != "2" {
			f.violations++
		}
		response = f.clients
	case r.Method == http.MethodGet && path == "/admin/realms/osac/clients/"+csiClientUUID:
		response = f.client
	case r.Method == http.MethodGet && path == "/admin/realms/osac/roles/osac-csi-driver":
		response = f.role
	case path == scopePath && r.Method == http.MethodGet:
		f.scopeReads++
		if f.scopeReads == f.changeOnRead {
			f.scopes = append(f.scopes, roleRepresentation("concurrent-role", "concurrent"))
		}
		response = f.scopes
	case path == scopePath && (r.Method == http.MethodPost || r.Method == http.MethodDelete):
		// The recoverable intent must exist before any remote mutation starts.
		data, err := os.ReadFile(f.record())
		var record map[string]any
		intent := "prepared"
		if r.Method == http.MethodDelete {
			intent = "rollback-pending"
		}
		if err != nil || json.Unmarshal(data, &record) != nil || record["phase"] != intent {
			f.violations++
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		var body any
		valid := json.NewDecoder(r.Body).Decode(&body) == nil
		expected := []any{map[string]any{"id": csiRoleUUID, "name": "osac-csi-driver"}}
		if !valid || !reflect.DeepEqual(body, expected) {
			f.violations++
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if r.Method == http.MethodPost {
			f.scopes = append(f.scopes, roleRepresentation(csiRoleUUID, "osac-csi-driver"))
		} else {
			retained := []any{}
			for _, role := range f.scopes {
				if role.(map[string]any)["id"] != csiRoleUUID {
					retained = append(retained, role)
				}
			}
			f.scopes = retained
		}
		f.writes++
		w.WriteHeader(http.StatusNoContent)
		return
	default:
		f.violations++
		w.WriteHeader(http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		f.violations++
	}
}

func (f *scopeProtocolFixture) change(fn func(*scopeProtocolFixture)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn(f)
}

func (f *scopeProtocolFixture) assertWrites(count int) {
	GinkgoHelper()
	f.mu.Lock()
	defer f.mu.Unlock()
	Expect(f.writes).To(Equal(count))
	Expect(f.violations).To(BeZero(), "only the bound endpoints and single-role mutation payload are permitted")
}

func (f *scopeProtocolFixture) run(ctx context.Context, args ...string) (scopeCLIResult, int) {
	GinkgoHelper()
	result, code, output := runScopeCLI(ctx, f.server.URL, f.tokenFile, f.caFile, args...)
	f.mu.Lock()
	leaked := strings.Contains(output, f.bearer) || strings.Contains(output, f.client["secret"].(string))
	f.mu.Unlock()
	Expect(leaked).To(BeFalse(), "updater output must withhold response/credential data")
	return result, code
}

func (f *scopeProtocolFixture) record() string { return filepath.Join(f.dir, "operation.json") }

var _ = Describe("CSI updater protocol safety", func() {
	It("defaults to a read-only dry run", func(ctx SpecContext) {
		f := newScopeProtocolFixture()
		result, code := f.run(ctx)
		Expect(code).To(BeZero())
		Expect(result).To(Equal(scopeCLIResult{Result: "would-add"}))
		f.assertWrites(0)
		_, err := os.Stat(f.record())
		Expect(os.IsNotExist(err)).To(BeTrue())
	})

	It("adds only its role once, records ownership, rolls back once and rejects record replay", func(ctx SpecContext) {
		f := newScopeProtocolFixture()
		result, code := f.run(ctx, "--apply", "--record", f.record())
		Expect(code).To(BeZero())
		Expect(result).To(Equal(scopeCLIResult{Result: "added", Changed: true}))
		f.assertWrites(1)
		data, err := os.ReadFile(f.record())
		Expect(err).ToNot(HaveOccurred())
		f.mu.Lock()
		leaked := strings.Contains(string(data), f.bearer) || strings.Contains(string(data), f.client["secret"].(string))
		f.mu.Unlock()
		Expect(leaked).To(BeFalse(), "the operation record must contain no credentials or client representations")
		info, err := os.Stat(f.record())
		Expect(err).ToNot(HaveOccurred())
		Expect(info.Mode().Perm()).To(Equal(os.FileMode(0600)))
		result, code = f.run(ctx, "--apply", "--record", f.record())
		Expect(code).To(BeZero())
		Expect(result).To(Equal(scopeCLIResult{Result: "already-present"}))
		f.assertWrites(1)
		same, err := os.ReadFile(f.record())
		Expect(err).ToNot(HaveOccurred())
		sameRecord := string(same) == string(data)
		Expect(sameRecord).To(BeTrue(), "second apply must not replace its original ownership record")
		result, code = f.run(ctx, "--rollback", "--record", f.record())
		Expect(code).To(BeZero())
		Expect(result).To(Equal(scopeCLIResult{Result: "rolled-back", Changed: true}))
		f.assertWrites(2)
		result, code = f.run(ctx, "--rollback", "--record", f.record())
		Expect(code).To(BeZero())
		Expect(result).To(Equal(scopeCLIResult{Result: "rollback-noop"}))
		f.assertWrites(2)
		f.change(func(f *scopeProtocolFixture) { f.scopes = append(f.scopes, f.role) })
		_, code = f.run(ctx, "--rollback", "--record", f.record())
		Expect(code).ToNot(BeZero(), "a consumed record must not delete a subsequent addition")
		f.assertWrites(2)
	})

	It("never acquires ownership of a preexisting binding", func(ctx SpecContext) {
		f := newScopeProtocolFixture()
		f.change(func(f *scopeProtocolFixture) { f.scopes = append(f.scopes, f.role) })
		result, code := f.run(ctx, "--apply", "--record", f.record())
		Expect(code).To(BeZero())
		Expect(result).To(Equal(scopeCLIResult{Result: "already-present"}))
		result, code = f.run(ctx, "--rollback", "--record", f.record())
		Expect(code).To(BeZero())
		Expect(result).To(Equal(scopeCLIResult{Result: "rollback-noop"}))
		f.assertWrites(0)
	})

	DescribeTable("rejects unsafe target/API state before mutation", func(ctx SpecContext, change func(*scopeProtocolFixture)) {
		f := newScopeProtocolFixture()
		f.change(change)
		_, code := f.run(ctx, "--apply", "--record", f.record())
		Expect(code).ToNot(BeZero())
		f.assertWrites(0)
	},
		Entry("missing client", func(f *scopeProtocolFixture) { f.clients = []any{} }),
		Entry("ambiguous client", func(f *scopeProtocolFixture) { f.clients = []any{f.client, f.client} }),
		Entry("non-list client response", func(f *scopeProtocolFixture) { f.clients = f.client }),
		Entry("wrong client name", func(f *scopeProtocolFixture) { f.client["clientId"] = "osac-admin" }),
		Entry("wrong client UUID", func(f *scopeProtocolFixture) { f.client["id"] = "replacement-client" }),
		Entry("wrong protocol", func(f *scopeProtocolFixture) { f.client["protocol"] = "saml" }),
		Entry("disabled client", func(f *scopeProtocolFixture) { f.client["enabled"] = false }),
		Entry("public client", func(f *scopeProtocolFixture) { f.client["publicClient"] = true }),
		Entry("bearer-only client", func(f *scopeProtocolFixture) { f.client["bearerOnly"] = true }),
		Entry("disabled service account", func(f *scopeProtocolFixture) { f.client["serviceAccountsEnabled"] = false }),
		Entry("full scope enabled", func(f *scopeProtocolFixture) { f.client["fullScopeAllowed"] = true }),
		Entry("missing scope flag", func(f *scopeProtocolFixture) { delete(f.client, "fullScopeAllowed") }),
		Entry("non-boolean scope flag", func(f *scopeProtocolFixture) { f.client["fullScopeAllowed"] = 0 }),
		Entry("unexpected authenticator", func(f *scopeProtocolFixture) { f.client["clientAuthenticatorType"] = "client-jwt" }),
		Entry("unexpected browser flow", func(f *scopeProtocolFixture) { f.client["standardFlowEnabled"] = true }),
		Entry("wrong role name", func(f *scopeProtocolFixture) { f.role["name"] = "admin" }),
		Entry("wrong role UUID", func(f *scopeProtocolFixture) { f.role["id"] = "replacement-role" }),
		Entry("client role", func(f *scopeProtocolFixture) { f.role["clientRole"] = true }),
		Entry("composite role", func(f *scopeProtocolFixture) { f.role["composite"] = true }),
		Entry("non-boolean role type", func(f *scopeProtocolFixture) { f.role["composite"] = 0 }),
		Entry("foreign realm role", func(f *scopeProtocolFixture) { f.role["containerId"] = "foreign-realm" }),
		Entry("null direct scopes", func(f *scopeProtocolFixture) { f.scopes = nil }),
		Entry("duplicate scope", func(f *scopeProtocolFixture) { f.scopes = append(f.scopes, f.scopes[0]) }),
		Entry("conflicting scope identity", func(f *scopeProtocolFixture) {
			f.scopes = append(f.scopes, roleRepresentation("wrong-id", "osac-csi-driver"))
		}),
		Entry("concurrent scope change before POST", func(f *scopeProtocolFixture) { f.changeOnRead = 2 }),
		Entry("HTTP 401", func(f *scopeProtocolFixture) { f.failureMethod, f.failureStatus = "GET", 401; f.failureBody = f.bearer }),
		Entry("HTTP 403", func(f *scopeProtocolFixture) { f.failureMethod, f.failureStatus = "GET", 403; f.failureBody = f.bearer }),
		Entry("HTTP 404", func(f *scopeProtocolFixture) { f.failureMethod, f.failureStatus = "GET", 404; f.failureBody = f.bearer }),
		Entry("HTTP 500", func(f *scopeProtocolFixture) { f.failureMethod, f.failureStatus = "GET", 500; f.failureBody = f.bearer }),
		Entry("invalid JSON", func(f *scopeProtocolFixture) { f.failureMethod, f.failureStatus = "GET", 200; f.failureBody = f.bearer }),
		Entry("duplicate JSON keys", func(f *scopeProtocolFixture) {
			f.failureMethod, f.failureStatus = "GET", 200
			f.failureBody = `[{"id":"first","id":"second"}]`
		}),
		Entry("oversized response", func(f *scopeProtocolFixture) {
			f.failureMethod, f.failureStatus = "GET", 200
			f.failureBody = strings.Repeat("x", (1<<20)+1)
		}),
	)

	DescribeTable("fails closed on uncertain writes", func(ctx SpecContext, method string) {
		f := newScopeProtocolFixture()
		writes := 0
		if method == "DELETE" {
			_, code := f.run(ctx, "--apply", "--record", f.record())
			Expect(code).To(BeZero())
			writes = 1
		}
		f.change(func(f *scopeProtocolFixture) {
			f.failureMethod, f.failureStatus, f.failureBody = method, 500, f.bearer
		})
		action := "--apply"
		if method == "DELETE" {
			action = "--rollback"
		}
		_, code := f.run(ctx, action, "--record", f.record())
		Expect(code).ToNot(BeZero())
		f.change(func(f *scopeProtocolFixture) { f.failureMethod = "" })
		_, code = f.run(ctx, "--apply", "--record", f.record())
		Expect(code).ToNot(BeZero(), "an uncertain operation needs administrator reconciliation")
		_, code = f.run(ctx, "--rollback", "--record", f.record())
		Expect(code).ToNot(BeZero())
		f.assertWrites(writes)
	}, Entry("POST", "POST"), Entry("DELETE", "DELETE"))

	DescribeTable("refuses recovery when a completed write cannot be verified", func(ctx SpecContext, rollback bool) {
		f := newScopeProtocolFixture()
		writes, read, action := 1, 3, "--apply"
		if rollback {
			_, code := f.run(ctx, "--apply", "--record", f.record())
			Expect(code).To(BeZero())
			writes, read, action = 2, 6, "--rollback"
		}
		f.change(func(f *scopeProtocolFixture) { f.changeOnRead = read })
		_, code := f.run(ctx, action, "--record", f.record())
		Expect(code).ToNot(BeZero())
		_, code = f.run(ctx, "--rollback", "--record", f.record())
		Expect(code).ToNot(BeZero())
		_, code = f.run(ctx, "--apply", "--record", f.record())
		Expect(code).ToNot(BeZero())
		f.assertWrites(writes)
	}, Entry("after POST", false), Entry("after DELETE", true))

	It("rejects an operation record bound to a different server", func(ctx SpecContext) {
		f := newScopeProtocolFixture()
		_, code := f.run(ctx, "--apply", "--record", f.record())
		Expect(code).To(BeZero())
		data, err := os.ReadFile(f.record())
		Expect(err).ToNot(HaveOccurred())
		var record map[string]any
		Expect(json.Unmarshal(data, &record)).To(Succeed())
		record["server"] = "https://different.example.invalid"
		data, err = json.Marshal(record)
		Expect(err).ToNot(HaveOccurred())
		Expect(os.WriteFile(f.record(), data, 0600)).To(Succeed())
		_, code = f.run(ctx, "--rollback", "--record", f.record())
		Expect(code).ToNot(BeZero())
		f.assertWrites(1)
	})

	It("refuses rollback after unrelated direct scopes change", func(ctx SpecContext) {
		f := newScopeProtocolFixture()
		_, code := f.run(ctx, "--apply", "--record", f.record())
		Expect(code).To(BeZero())
		f.change(func(f *scopeProtocolFixture) {
			f.scopes = append(f.scopes, roleRepresentation("later-role", "later-role"))
		})
		_, code = f.run(ctx, "--rollback", "--record", f.record())
		Expect(code).ToNot(BeZero())
		f.assertWrites(1)
	})

	DescribeTable("rejects unsafe local credential/record files", func(ctx SpecContext, prepare func(*scopeProtocolFixture)) {
		f := newScopeProtocolFixture()
		prepare(f)
		_, code := f.run(ctx, "--apply", "--record", f.record())
		Expect(code).ToNot(BeZero())
		f.assertWrites(0)
	},
		Entry("readable token", func(f *scopeProtocolFixture) { Expect(os.Chmod(f.tokenFile, 0644)).To(Succeed()) }),
		Entry("token symlink", func(f *scopeProtocolFixture) {
			path := f.tokenFile + ".link"
			Expect(os.Symlink(f.tokenFile, path)).To(Succeed())
			f.tokenFile = path
		}),
		Entry("record symlink", func(f *scopeProtocolFixture) { Expect(os.Symlink(f.tokenFile, f.record())).To(Succeed()) }),
		Entry("shared record directory", func(f *scopeProtocolFixture) { Expect(os.Chmod(f.dir, 0755)).To(Succeed()) }),
		Entry("malformed existing record", func(f *scopeProtocolFixture) {
			Expect(os.WriteFile(f.record(), []byte(`{"state":"applied"}`), 0600)).To(Succeed())
		}),
		Entry("locked record", func(f *scopeProtocolFixture) {
			lock, err := os.OpenFile(f.record()+".lock", os.O_CREATE|os.O_RDWR, 0600)
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(lock.Close)
			Expect(syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)).To(Succeed())
		}),
	)

	DescribeTable("rejects unsafe transport and arguments", func(ctx SpecContext, variant string) {
		f := newScopeProtocolFixture()
		server, ca := f.server.URL, f.caFile
		args := []string{"--apply", "--record", f.record()}
		switch variant {
		case "http":
			server = strings.Replace(server, "https:", "http:", 1)
		case "userinfo":
			server = strings.Replace(server, "https://", "https://operator@", 1)
		case "query":
			server += "?unexpected=true"
		case "empty-query":
			server += "?"
		case "fragment":
			server += "#unexpected"
		case "empty-fragment":
			server += "#"
		case "traversal":
			server += "/../admin"
		case "unknown-ca":
			ca = ""
		case "invalid-ca":
			ca = f.tokenFile
		case "arbitrary-client":
			args = append(args, "--client", "osac-admin")
		case "arbitrary-realm":
			args = append(args, "--realm", "master")
		case "missing-record":
			args = []string{"--apply"}
		case "conflicting-actions":
			args = append(args, "--rollback")
		case "unbounded-timeout":
			args = append(args, "--timeout", "0")
		}
		_, code, output := runScopeCLI(ctx, server, f.tokenFile, ca, args...)
		Expect(code).ToNot(BeZero())
		Expect(strings.Contains(output, f.bearer)).To(BeFalse())
		f.assertWrites(0)
	},
		Entry("plaintext", "http"), Entry("URL credentials", "userinfo"), Entry("query", "query"),
		Entry("empty query", "empty-query"), Entry("empty fragment", "empty-fragment"),
		Entry("fragment", "fragment"), Entry("path traversal", "traversal"), Entry("untrusted CA", "unknown-ca"),
		Entry("invalid CA", "invalid-ca"), Entry("other client", "arbitrary-client"), Entry("other realm", "arbitrary-realm"),
		Entry("missing record", "missing-record"), Entry("both mutations", "conflicting-actions"), Entry("unbounded timeout", "unbounded-timeout"),
	)

	It("does not follow credential-bearing redirects", func(ctx SpecContext) {
		f := newScopeProtocolFixture()
		var received int
		var mu sync.Mutex
		target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			received++
			mu.Unlock()
			w.WriteHeader(200)
		}))
		defer target.Close()
		f.change(func(f *scopeProtocolFixture) { f.failureMethod, f.failureStatus, f.redirect = "GET", 307, target.URL })
		_, code := f.run(ctx, "--apply", "--record", f.record())
		Expect(code).ToNot(BeZero())
		mu.Lock()
		defer mu.Unlock()
		Expect(received).To(BeZero())
		f.assertWrites(0)
	})

	It("bounds stalled requests", func(ctx SpecContext) {
		f := newScopeProtocolFixture()
		f.change(func(f *scopeProtocolFixture) { f.delay = 1500 * time.Millisecond })
		started := time.Now()
		_, code := f.run(ctx, "--apply", "--record", f.record(), "--timeout", "1")
		Expect(code).ToNot(BeZero())
		Expect(time.Since(started)).To(BeNumerically("<", 5*time.Second))
		// The timed-out connection may reject the server's eventual write; only
		// count API mutations, since a closed socket is expected in this case.
		f.mu.Lock()
		defer f.mu.Unlock()
		Expect(f.writes).To(BeZero())
		_, err := fmt.Fprintln(GinkgoWriter, "updater request_timeout=bounded credential_output=withheld")
		Expect(err).ToNot(HaveOccurred())
	})
})
