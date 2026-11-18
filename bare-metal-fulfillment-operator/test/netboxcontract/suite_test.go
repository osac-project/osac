/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package netboxcontract

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2" //nolint:revive,staticcheck
	. "github.com/onsi/gomega"    //nolint:revive,staticcheck

	"github.com/osac-project/osac/bare-metal-fulfillment-operator/internal/baremetalhost"
	"github.com/osac-project/osac/bare-metal-fulfillment-operator/internal/credentials"
	"github.com/osac-project/osac/bare-metal-fulfillment-operator/internal/inventory"
	"github.com/osac-project/osac/bare-metal-fulfillment-operator/internal/netboxclient"
	"github.com/osac-project/osac/bare-metal-fulfillment-operator/test/netboxcontract/netboxtest"
)

const (
	// metal3Namespace is the namespace the adapter derives host IDs from.
	metal3Namespace = "metal3-system"
	// hostClass is the only host class the NetBox adapter accepts.
	hostClass = "metal3"
	// caseFieldName isolates contract cases from one another: every case gives
	// its devices a unique value so one search can only return that case's set.
	caseFieldName = "contract_case"
	// testToken is a v2-shaped token, so the adapter must send it as a Bearer
	// credential rather than a v1 Token header.
	testToken = "nbt_aaaaaaaaaaaa.bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

var (
	server *netboxtest.Server
	caFile string
)

func TestNetBoxContract(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "NetBox API contract suite")
}

var _ = BeforeEach(func() {
	server = netboxtest.New()
	DeferCleanup(server.Stop)
	server.SetAuth("Bearer", testToken)
	for _, field := range contractFields() {
		server.AddField(field)
	}
	caFile = writeCAFile(server.Certificate())
})

// writeCAFile materializes the mock server certificate so the client exercises
// its real TLS and CA-file path instead of skipping verification.
func writeCAFile(certificate *x509.Certificate) string {
	path := filepath.Join(GinkgoT().TempDir(), "netbox-ca.pem")
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificate.Raw})
	Expect(os.WriteFile(path, caPEM, 0o600)).To(Succeed())
	return path
}

func writeTokenFile() string {
	path := filepath.Join(GinkgoT().TempDir(), "netbox-token")
	Expect(os.WriteFile(path, []byte(testToken+"\n"), 0o600)).To(Succeed())
	return path
}

// newAPIClient builds the production NetBox client against the mock server.
func newAPIClient() *netboxclient.Client {
	client, err := netboxclient.New(netboxclient.Config{
		Endpoint:  server.URL(),
		TokenFile: writeTokenFile(),
		CAFile:    caFile,
	})
	Expect(err).NotTo(HaveOccurred())
	return client
}

// newInventoryClient builds the inventory adapter under test. Credential
// resolution is omitted: selection and identity cases never reach the OSAC
// Secret API, and the stub fails the spec if they try.
func newInventoryClient() *inventory.NetBoxClient {
	return inventory.NewNetBoxClient(
		newAPIClient(), stubBMHManager{namespace: metal3Namespace}, noOpCredentialsWriter{}, hostClass, nil,
	)
}

type noOpCredentialsWriter struct{}

func (noOpCredentialsWriter) Create(context.Context, credentials.Reference, credentials.BMC) error {
	return nil
}

func (noOpCredentialsWriter) Delete(context.Context, credentials.Reference) error { return nil }

func contractContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 30*time.Second)
}

// requestMark records how many requests the server has handled, so a case can
// assert exactly what the adapter sent.
func requestMark() int { return len(server.Requests()) }

// requestsSince returns the adapter requests recorded after mark.
func requestsSince(mark int) []netboxtest.Request {
	all := server.Requests()
	if mark > len(all) {
		return nil
	}
	return all[mark:]
}

// patchBodies returns the decoded body of every recorded PATCH.
func patchBodies(requests []netboxtest.Request) []map[string]any {
	bodies := make([]map[string]any, 0, len(requests))
	for _, request := range requests {
		if request.Method != "PATCH" {
			continue
		}
		var body map[string]any
		if err := json.Unmarshal(request.Body, &body); err != nil {
			continue
		}
		bodies = append(bodies, body)
	}
	return bodies
}

// stubBMHManager satisfies the BMH lifecycle dependency for cases that never
// reach Metal3. Every call fails the spec if it is unexpectedly made.
type stubBMHManager struct {
	namespace string
}

func (s stubBMHManager) Namespace() string { return s.namespace }

func (s stubBMHManager) CreateBMH(context.Context, baremetalhost.CreateParams) error {
	Fail("CreateBMH must not be called by this contract case")
	return nil
}

func (s stubBMHManager) DeleteBMH(context.Context, string, string) error {
	Fail("DeleteBMH must not be called by this contract case")
	return nil
}

func (s stubBMHManager) BMHExists(context.Context, string) (bool, error) {
	Fail("BMHExists must not be called by this contract case")
	return false, nil
}

func (s stubBMHManager) IsBMHAvailable(context.Context, string) (bool, error) {
	Fail("IsBMHAvailable must not be called by this contract case")
	return false, nil
}

func (s stubBMHManager) EnsureBMCSecret(context.Context, string, string, string, string) error {
	Fail("EnsureBMCSecret must not be called by this contract case")
	return nil
}

func (s stubBMHManager) DeleteBMCSecret(context.Context, string, string) error {
	Fail("DeleteBMCSecret must not be called by this contract case")
	return nil
}

func (s stubBMHManager) GetHardwareNICs(context.Context, string) ([]string, error) {
	Fail("GetHardwareNICs must not be called by this contract case")
	return nil, nil
}
