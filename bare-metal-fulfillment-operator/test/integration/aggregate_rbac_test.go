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

package integration

import (
	"encoding/json"
	"os/exec"
	"sort"
	"time"

	. "github.com/onsi/ginkgo/v2" //nolint:revive,staticcheck
	. "github.com/onsi/gomega"    //nolint:revive,staticcheck
	rbacv1 "k8s.io/api/rbac/v1"

	"github.com/osac-project/osac/bare-metal-fulfillment-operator/test/utils"
)

var _ = Describe("BareMetalPool admin aggregate RBAC", func() {
	It("adds only the explicit BareMetalPool permissions to the admin ClusterRole", func() {
		want := []string{"create", "delete", "get", "list", "patch", "update", "watch"}
		sort.Strings(want)

		Eventually(func() ([]string, error) {
			output, err := utils.Run(exec.Command("kubectl", "get", "clusterrole", "admin", "-o", "json"))
			if err != nil {
				return nil, err
			}
			var role rbacv1.ClusterRole
			if err := json.Unmarshal([]byte(output), &role); err != nil {
				return nil, err
			}

			verbSet := make(map[string]struct{})
			for _, rule := range role.Rules {
				if contains(rule.APIGroups, "osac.openshift.io") && contains(rule.Resources, "baremetalpools") {
					for _, verb := range rule.Verbs {
						verbSet[verb] = struct{}{}
					}
				}
			}
			verbs := make([]string, 0, len(verbSet))
			for verb := range verbSet {
				verbs = append(verbs, verb)
			}
			sort.Strings(verbs)
			return verbs, nil
		}).WithTimeout(2 * time.Minute).WithPolling(time.Second).Should(Equal(want))
	})
})

func contains(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}
