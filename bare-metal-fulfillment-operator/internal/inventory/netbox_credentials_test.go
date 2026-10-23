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

package inventory

import (
	"context"
	"errors"

	. "github.com/onsi/ginkgo/v2" //nolint:revive,staticcheck
	. "github.com/onsi/gomega"    //nolint:revive,staticcheck
	"github.com/osac-project/osac/bare-metal-fulfillment-operator/internal/netboxclient"
	"github.com/osac-project/osac/bare-metal-fulfillment-operator/internal/secretapi"
)

type fakeSecretReader struct {
	listFilter string
	listLimit  int32
	listResult secretapi.ListResult
	listError  error
	getID      string
	getSecret  secretapi.Secret
	getError   error
	listCalls  int
	getCalls   int
}

func (f *fakeSecretReader) List(_ context.Context, filter string, limit int32) (secretapi.ListResult, error) {
	f.listCalls++
	f.listFilter = filter
	f.listLimit = limit
	return f.listResult, f.listError
}

func (f *fakeSecretReader) Get(_ context.Context, id string) (secretapi.Secret, error) {
	f.getCalls++
	f.getID = id
	return f.getSecret, f.getError
}

var _ = Describe("NetBox credential resolver", func() {
	It("resolves one system Secret by label-key existence and validates device data", func() {
		secret := secretapi.Secret{
			ID:     "secret-1",
			Tenant: "system",
			Labels: map[string]string{"osac.openshift.io/netbox-device-42": ""},
			Data: map[string][]byte{
				"username": []byte("bmc-user"),
				"password": []byte("bmc-password"),
			},
		}
		reader := &fakeSecretReader{
			listResult: secretapi.ListResult{
				Total: 1,
				Items: []secretapi.Secret{{ID: "secret-1", Tenant: "system", Labels: secret.Labels}},
			},
			getSecret: secret,
		}
		resolver := NewNetBoxCredentialResolver(reader)
		device := netboxclient.Device{
			ID: "42",
			CustomFields: map[string]interface{}{
				"osac_bmc_address": "redfish-virtualmedia+https://192.0.2.42/redfish/v1/Systems/1",
				"osac_boot_mac":    "AA:BB:CC:DD:EE:42",
			},
		}

		got, err := resolver.Resolve(context.Background(), device)

		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal(&NetBoxCredentials{
			BMCAddress:     "redfish-virtualmedia+https://192.0.2.42/redfish/v1/Systems/1",
			BootMACAddress: "aa:bb:cc:dd:ee:42",
			Username:       []byte("bmc-user"),
			Password:       []byte("bmc-password"),
		}))
		Expect(reader.listFilter).To(Equal(`this.metadata.tenant == "system" && "osac.openshift.io/netbox-device-42" in this.metadata.labels`))
		Expect(reader.listLimit).To(Equal(int32(2)))
		Expect(reader.getID).To(Equal("secret-1"))
		Expect(reader.listCalls).To(Equal(1))
		Expect(reader.getCalls).To(Equal(1))
	})

	DescribeTable("rejects invalid device identity or BMC fields before contacting the Secret API",
		func(device netboxclient.Device, wantErr string, deterministic bool) {
			reader := &fakeSecretReader{}
			resolver := NewNetBoxCredentialResolver(reader)

			_, err := resolver.Resolve(context.Background(), device)

			Expect(err).To(MatchError(ContainSubstring(wantErr)))
			Expect(errors.Is(err, ErrCredentialResolutionFailed)).To(Equal(deterministic))
			Expect(reader.listCalls).To(BeZero())
			Expect(reader.getCalls).To(BeZero())
		},
		Entry("non-numeric device ID", netboxclient.Device{ID: "42||true"}, "invalid NetBox device ID", false),
		Entry("missing BMC address", netboxclient.Device{ID: "42", CustomFields: map[string]interface{}{"osac_boot_mac": "aa:bb:cc:dd:ee:42"}}, "invalid BMC address", true),
		Entry("unsupported BMC address scheme", netboxclient.Device{ID: "42", CustomFields: map[string]interface{}{"osac_bmc_address": "http://192.0.2.42", "osac_boot_mac": "aa:bb:cc:dd:ee:42"}}, "invalid BMC address", true),
		Entry("invalid boot MAC", netboxclient.Device{ID: "42", CustomFields: map[string]interface{}{"osac_bmc_address": "ipmi://192.0.2.42", "osac_boot_mac": "not-a-mac"}}, "invalid boot MAC", true),
	)

	DescribeTable("rejects missing or ambiguous Secret matches",
		func(result secretapi.ListResult, wantErr string, deterministic bool) {
			reader := &fakeSecretReader{listResult: result}
			resolver := NewNetBoxCredentialResolver(reader)

			_, err := resolver.Resolve(context.Background(), validCredentialTestDevice())

			Expect(err).To(MatchError(ContainSubstring(wantErr)))
			Expect(errors.Is(err, ErrCredentialResolutionFailed)).To(Equal(deterministic))
			Expect(reader.getCalls).To(BeZero())
		},
		Entry("missing", secretapi.ListResult{}, "no system-scoped OSAC Secret is associated", true),
		Entry("ambiguous", secretapi.ListResult{Total: 2, Items: []secretapi.Secret{{ID: "one"}, {ID: "two"}}}, "multiple system-scoped OSAC Secrets are associated", true),
		Entry("inconsistent total", secretapi.ListResult{Total: 1}, "inconsistent OSAC Secret list response", false),
	)

	DescribeTable("rejects missing, malformed, or changed credentials without exposing values",
		func(secret secretapi.Secret, wantErr string, deterministic bool) {
			reader := &fakeSecretReader{
				listResult: secretapi.ListResult{Total: 1, Items: []secretapi.Secret{{ID: "secret-1", Tenant: "system", Labels: map[string]string{"osac.openshift.io/netbox-device-42": "true"}}}},
				getSecret:  secret,
			}
			resolver := NewNetBoxCredentialResolver(reader)

			_, err := resolver.Resolve(context.Background(), validCredentialTestDevice())

			Expect(err).To(MatchError(ContainSubstring(wantErr)))
			Expect(errors.Is(err, ErrCredentialResolutionFailed)).To(Equal(deterministic))
			Expect(err.Error()).NotTo(ContainSubstring("source-user"))
			Expect(err.Error()).NotTo(ContainSubstring("source-password"))
		},
		Entry("missing username", secretapi.Secret{ID: "secret-1", Tenant: "system", Labels: map[string]string{"osac.openshift.io/netbox-device-42": "true"}, Data: map[string][]byte{"password": []byte("source-password")}}, "non-empty username and password", true),
		Entry("empty password", secretapi.Secret{ID: "secret-1", Tenant: "system", Labels: map[string]string{"osac.openshift.io/netbox-device-42": "true"}, Data: map[string][]byte{"username": []byte("source-user"), "password": {}}}, "non-empty username and password", true),
		Entry("changed tenant", secretapi.Secret{ID: "secret-1", Tenant: "tenant-a", Labels: map[string]string{"osac.openshift.io/netbox-device-42": "true"}, Data: map[string][]byte{"username": []byte("source-user"), "password": []byte("source-password")}}, "not a system-scoped Secret associated", false),
		Entry("changed label", secretapi.Secret{ID: "secret-1", Tenant: "system", Labels: map[string]string{"other": "true"}, Data: map[string][]byte{"username": []byte("source-user"), "password": []byte("source-password")}}, "not a system-scoped Secret associated", false),
	)

	It("returns retryable API errors without credentials in the error", func() {
		reader := &fakeSecretReader{listError: errors.New("permission denied")}
		resolver := NewNetBoxCredentialResolver(reader)

		_, err := resolver.Resolve(context.Background(), validCredentialTestDevice())

		Expect(err).To(MatchError(ContainSubstring("failed to list OSAC Secrets")))
		Expect(errors.Is(err, ErrCredentialResolutionFailed)).To(BeFalse())
		Expect(err.Error()).NotTo(ContainSubstring("source-password"))
		Expect(reader.getCalls).To(BeZero())
	})

	It("leaves a failed Secret Get retryable", func() {
		reader := &fakeSecretReader{
			listResult: secretapi.ListResult{Total: 1, Items: []secretapi.Secret{{
				ID: "secret-1", Tenant: "system", Labels: map[string]string{"osac.openshift.io/netbox-device-42": "true"},
			}}},
			getError: errors.New("permission denied"),
		}

		_, err := NewNetBoxCredentialResolver(reader).Resolve(context.Background(), validCredentialTestDevice())

		Expect(err).To(MatchError(ContainSubstring("failed to get OSAC Secret")))
		Expect(errors.Is(err, ErrCredentialResolutionFailed)).To(BeFalse())
		Expect(reader.getCalls).To(Equal(1))
	})
})

func validCredentialTestDevice() netboxclient.Device {
	return netboxclient.Device{
		ID: "42",
		CustomFields: map[string]interface{}{
			"osac_bmc_address": "ipmi://192.0.2.42",
			"osac_boot_mac":    "aa:bb:cc:dd:ee:42",
		},
	}
}
