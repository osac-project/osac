/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package install

import (
	"context"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

var _ = Describe("SecretChecks", func() {
	It("builds one Required check per ref, in order", func() {
		checks := SecretChecks([]SecretRef{
			{Namespace: "osac", Name: "config-as-code-manifest-ig", Keys: []string{"license.zip"}},
			{Namespace: "osac", Name: "osac-db-config"},
		})

		Expect(checks).To(HaveLen(2))
		for _, check := range checks {
			Expect(check.Severity).To(Equal(Required))
		}
	})

	It("passes when the Secret exists and has every required key", func() {
		checks := SecretChecks([]SecretRef{{Namespace: "osac", Name: "config-as-code-manifest-ig", Keys: []string{"license.zip"}}})
		clients := newFakeClients([]runtime.Object{
			&corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: "config-as-code-manifest-ig", Namespace: "osac"},
				Data:       map[string][]byte{"license.zip": []byte("fake")},
			},
		}, nil)

		status, message := checks[0].Run(context.Background(), clients)

		Expect(status).To(Equal(Pass))
		Expect(message).To(ContainSubstring("config-as-code-manifest-ig"))
	})

	It("fails when the Secret doesn't exist", func() {
		checks := SecretChecks([]SecretRef{{Namespace: "osac", Name: "missing"}})
		clients := newFakeClients(nil, nil)

		status, message := checks[0].Run(context.Background(), clients)

		Expect(status).To(Equal(Failed))
		Expect(message).To(ContainSubstring("not found"))
	})

	It("fails and names the missing key when a required key is absent", func() {
		checks := SecretChecks([]SecretRef{{Namespace: "osac", Name: "config-as-code-manifest-ig", Keys: []string{"license.zip"}}})
		clients := newFakeClients([]runtime.Object{
			&corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: "config-as-code-manifest-ig", Namespace: "osac"},
				Data:       map[string][]byte{"other-key": []byte("x")},
			},
		}, nil)

		status, message := checks[0].Run(context.Background(), clients)

		Expect(status).To(Equal(Failed))
		Expect(message).To(ContainSubstring("license.zip"))
	})

	It("passes on existence alone when no keys are required", func() {
		checks := SecretChecks([]SecretRef{{Namespace: "osac", Name: "osac-db-config"}})
		clients := newFakeClients([]runtime.Object{
			&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "osac-db-config", Namespace: "osac"}},
		}, nil)

		status, _ := checks[0].Run(context.Background(), clients)

		Expect(status).To(Equal(Pass))
	})
})
