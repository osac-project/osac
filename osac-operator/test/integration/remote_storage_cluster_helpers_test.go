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
	"strconv"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/yaml"
)

// TestCSPFixturePodNetworkingMode checks that either installed networking mode
// reaches the fixture process through the rendered Pod command.
func TestCSPFixturePodNetworkingMode(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(strconv.FormatBool(enabled), func(t *testing.T) {
			var pod corev1.Pod
			if err := yaml.Unmarshal([]byte(cspFixturePodYAML("fixture:test", enabled)), &pod); err != nil {
				t.Fatal(err)
			}
			if len(pod.Spec.Containers) != 1 {
				t.Fatalf("expected one fixture container, got %d", len(pod.Spec.Containers))
			}
			command := strings.Join(pod.Spec.Containers[0].Command, " ")
			want := "--networking-provisioning-enabled=" + strconv.FormatBool(enabled)
			if !strings.Contains(command, want) {
				t.Fatalf("fixture command does not pass %s", want)
			}
		})
	}
}
