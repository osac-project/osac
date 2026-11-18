/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package it

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

var _ = Describe("Deployed BMF with mock NetBox", Label("bmf-netbox"), func() {
	It("allocates, releases, and reconnects after token rotation", func(ctx context.Context) {
		deviceID := strconv.FormatInt(time.Now().UnixMilli()%1_000_000_000+1, 10)
		name := "it-netbox-" + deviceID
		secrets := privatev1.NewSecretsClient(tool.InternalView().AdminConn())
		created, err := secrets.Create(ctx, privatev1.SecretsCreateRequest_builder{
			Object: privatev1.Secret_builder{
				Metadata: privatev1.Metadata_builder{
					Name:   name,
					Tenant: "system",
					Labels: map[string]string{
						"osac.openshift.io/netbox-device-" + deviceID: "true",
					},
					Annotations: map[string]string{
						"osac.openshift.io/tenant":          "system",
						"osac.openshift.io/owner-reference": name,
					},
				}.Build(),
				Data: map[string][]byte{
					"username": []byte("fixture-user"),
					"password": []byte("fixture-password"),
				},
			}.Build(),
		}.Build())
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() {
			_, deleteErr := secrets.Delete(context.Background(), privatev1.SecretsDeleteRequest_builder{
				Id: created.GetObject().GetId(),
			}.Build())
			Expect(deleteErr).NotTo(HaveOccurred())
		})

		arguments := []string{"../../osac-installer/scripts/test-bmf-netbox-render.py", "--live-install-only", "--device-id", deviceID}
		python := "python3"
		if _, lookupErr := exec.LookPath("uv"); lookupErr == nil {
			python = "uv"
			arguments = append([]string{"run", "--project", "../..", "python"}, arguments...)
		}
		command := exec.CommandContext(ctx, python, arguments...)
		command.Env = os.Environ()
		output, err := command.CombinedOutput()
		Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("deployed NetBox check failed: %s", output))
	})
})
