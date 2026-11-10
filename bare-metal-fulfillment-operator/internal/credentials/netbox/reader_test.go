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

package netbox

import (
	"context"
	"errors"
	"testing"

	. "github.com/onsi/ginkgo/v2" //nolint:revive,staticcheck
	. "github.com/onsi/gomega"    //nolint:revive,staticcheck

	"github.com/osac-project/osac/bare-metal-fulfillment-operator/internal/credentials"
	"github.com/osac-project/osac/bare-metal-fulfillment-operator/internal/secretapi"
)

// Synthetic test values only; this reader test never uses real credentials.
const (
	testNetBoxUsername = "fake-user"
	testNetBoxPassword = "fake-value"
	testSourceUsername = "fake-source-user"
	testSourcePassword = "fake-source-value"
)

func TestNetBoxCredentials(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "NetBox Credentials Reader Suite")
}

type fakeSecretReader struct {
	listFilter secretapi.Filter
	listLimit  int32
	listResult secretapi.ListResult
	listError  error
	getID      string
	getSecret  secretapi.Secret
	getError   error
	listCalls  int
	getCalls   int
}

func (f *fakeSecretReader) List(_ context.Context, filter secretapi.Filter, limit int32) (secretapi.ListResult, error) {
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

func (*fakeSecretReader) Close() error { return nil }

var _ = Describe("NetBox credentials reader", func() {
	It("resolves typed BMC credentials from one system Secret", func() {
		labels := map[string]string{"osac.openshift.io/netbox-device-42": ""}
		reader := &fakeSecretReader{
			listResult: secretapi.ListResult{Total: 1, Items: []secretapi.Secret{{ID: "secret-1", Tenant: "system", Labels: labels}}},
			getSecret: secretapi.Secret{
				ID: "secret-1", Tenant: "system", Labels: labels,
				Data: map[string][]byte{"username": []byte(testNetBoxUsername), "password": []byte(testNetBoxPassword)},
			},
		}

		got, err := NewReader(reader).GetCredentials(context.Background(), "42")

		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal(&credentials.BMC{Username: []byte(testNetBoxUsername), Password: []byte(testNetBoxPassword)}))
		Expect(reader.listFilter).To(Equal(secretapi.Filter(`this.metadata.tenant == "system" && "osac.openshift.io/netbox-device-42" in this.metadata.labels`)))
		Expect(reader.listLimit).To(Equal(int32(1)))
		Expect(reader.getID).To(Equal("secret-1"))
		Expect(reader.listCalls).To(Equal(1))
		Expect(reader.getCalls).To(Equal(1))
	})

	DescribeTable("rejects missing or ambiguous Secret matches",
		func(result secretapi.ListResult, wantErr string, category error) {
			reader := &fakeSecretReader{listResult: result}

			_, err := NewReader(reader).GetCredentials(context.Background(), "42")

			Expect(err).To(MatchError(ContainSubstring(wantErr)))
			Expect(errors.Is(err, credentials.ErrResolutionFailed)).To(BeTrue())
			Expect(errors.Is(err, category)).To(BeTrue())
			Expect(reader.getCalls).To(BeZero())
		},
		Entry("missing", secretapi.ListResult{}, errMissingNetBoxSecret.Error(), credentials.ErrCredentialsNotFound),
		Entry("ambiguous", secretapi.ListResult{Total: 2, Items: []secretapi.Secret{{ID: "one"}, {ID: "two"}}}, errAmbiguousNetBoxSecret.Error(), credentials.ErrAmbiguousCredentials),
	)

	It("leaves inconsistent list responses retryable", func() {
		reader := &fakeSecretReader{listResult: secretapi.ListResult{Total: 1}}

		_, err := NewReader(reader).GetCredentials(context.Background(), "42")

		Expect(err).To(MatchError(ContainSubstring("inconsistent OSAC Secret list response")))
		Expect(errors.Is(err, credentials.ErrResolutionFailed)).To(BeFalse())
	})

	DescribeTable("rejects missing, malformed, or changed credentials without exposing values",
		func(secret secretapi.Secret, wantErr string, deterministic bool) {
			reader := &fakeSecretReader{
				listResult: secretapi.ListResult{Total: 1, Items: []secretapi.Secret{{
					ID: "secret-1", Tenant: "system", Labels: map[string]string{"osac.openshift.io/netbox-device-42": "true"},
				}}},
				getSecret: secret,
			}

			_, err := NewReader(reader).GetCredentials(context.Background(), "42")

			Expect(err).To(MatchError(ContainSubstring(wantErr)))
			Expect(errors.Is(err, credentials.ErrResolutionFailed)).To(Equal(deterministic))
			Expect(err.Error()).NotTo(ContainSubstring(testSourceUsername))
			Expect(err.Error()).NotTo(ContainSubstring(testSourcePassword))
		},
		Entry("missing username", secretapi.Secret{ID: "secret-1", Tenant: "system", Labels: map[string]string{"osac.openshift.io/netbox-device-42": "true"}, Data: map[string][]byte{"password": []byte(testSourcePassword)}}, "non-empty username and password", true),
		Entry("empty password", secretapi.Secret{ID: "secret-1", Tenant: "system", Labels: map[string]string{"osac.openshift.io/netbox-device-42": "true"}, Data: map[string][]byte{"username": []byte(testSourceUsername), "password": {}}}, "non-empty username and password", true),
		Entry("changed tenant", secretapi.Secret{ID: "secret-1", Tenant: "tenant-a", Labels: map[string]string{"osac.openshift.io/netbox-device-42": "true"}, Data: map[string][]byte{"username": []byte(testSourceUsername), "password": []byte(testSourcePassword)}}, "not a system-scoped Secret associated", false),
		Entry("changed label", secretapi.Secret{ID: "secret-1", Tenant: "system", Labels: map[string]string{"other": "true"}, Data: map[string][]byte{"username": []byte(testSourceUsername), "password": []byte(testSourcePassword)}}, "not a system-scoped Secret associated", false),
	)

	It("returns retryable API errors without credentials in the error", func() {
		reader := &fakeSecretReader{listError: errors.New("permission denied")}

		_, err := NewReader(reader).GetCredentials(context.Background(), "42")

		Expect(err).To(MatchError(ContainSubstring("failed to list OSAC Secrets")))
		Expect(errors.Is(err, credentials.ErrResolutionFailed)).To(BeFalse())
		Expect(err.Error()).NotTo(ContainSubstring(testSourcePassword))
		Expect(reader.getCalls).To(BeZero())
	})

	It("leaves a failed Secret Get retryable", func() {
		reader := &fakeSecretReader{
			listResult: secretapi.ListResult{Total: 1, Items: []secretapi.Secret{{
				ID: "secret-1", Tenant: "system", Labels: map[string]string{"osac.openshift.io/netbox-device-42": "true"},
			}}},
			getError: errors.New("permission denied"),
		}

		_, err := NewReader(reader).GetCredentials(context.Background(), "42")

		Expect(err).To(MatchError(ContainSubstring("failed to get OSAC Secret")))
		Expect(errors.Is(err, credentials.ErrResolutionFailed)).To(BeFalse())
		Expect(reader.getCalls).To(Equal(1))
	})

	It("closes the underlying Secret API client", func() {
		reader := &closeTrackingSecretReader{}

		Expect(NewReader(reader).Close()).To(Succeed())
		Expect(reader.closed).To(BeTrue())
	})
})

type closeTrackingSecretReader struct {
	fakeSecretReader
	closed bool
}

func (r *closeTrackingSecretReader) Close() error {
	r.closed = true
	return nil
}
