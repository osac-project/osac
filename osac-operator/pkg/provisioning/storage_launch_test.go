/*
Copyright 2025.

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

package provisioning_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
	"github.com/osac-project/osac/osac-operator/pkg/aap"
	"github.com/osac-project/osac/osac-operator/pkg/provisioning"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

var _ = Describe("Storage launch error protection", func() {
	DescribeTable("redacts a real HTTP failure that echoes storage credentials", func(workflow bool) {
		var logs bytes.Buffer
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodGet {
				if workflow && strings.Contains(r.URL.Path, "/job_templates/") {
					_, _ = fmt.Fprint(w, `{"count":0,"results":[]}`)
					return
				}
				_, _ = fmt.Fprint(w, `{"count":1,"results":[{"id":1,"name":"storage"}]}`)
				return
			}
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.Copy(w, r.Body)
		}))
		defer server.Close()
		ctx := log.IntoContext(context.Background(), zap.New(zap.WriteTo(&logs)))
		ctx = provisioning.WithStorageBackendConnections(ctx, map[string]provisioning.BackendConnection{
			"backend": {Password: "fixture-discovery-password", TenantPreparation: &provisioning.TenantPreparation{
				ProviderConfig: map[string]any{"password": "fixture-runtime-password"},
			}},
		})
		provider := provisioning.NewAAPProvider(aap.NewClient(server.URL, "fixture-token", false), "storage", "storage")
		_, err := provider.TriggerProvision(ctx, &v1alpha1.Tenant{})
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("400"))
		for _, password := range []string{"fixture-discovery-password", "fixture-runtime-password"} {
			Expect(err.Error()).NotTo(ContainSubstring(password))
			Expect(logs.String()).NotTo(ContainSubstring(password))
		}
	}, Entry("job", false), Entry("workflow", true))
})
