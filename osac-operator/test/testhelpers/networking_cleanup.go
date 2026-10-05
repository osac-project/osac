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

// Package testhelpers provides shared helpers for deployed networking tests.
package testhelpers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"slices"
	"time"

	. "github.com/onsi/gomega" //nolint:revive,staticcheck
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// Get returns a namespaced resource read through kubectl.
func Get(namespace, kind, name string) (*unstructured.Unstructured, error) {
	output, err := exec.Command("kubectl", "get", kind, name,
		"-n", namespace, "-o", "json").CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("get %s/%s: %w: %s", kind, name, err, output)
	}
	object := &unstructured.Unstructured{}
	if err := object.UnmarshalJSON(output); err != nil {
		return nil, err
	}
	return object, nil
}

// Absent reports whether a namespaced resource no longer exists.
func Absent(namespace, kind, name string) (bool, error) {
	output, err := exec.Command("kubectl", "get", kind, name,
		"-n", namespace, "--ignore-not-found", "-o", "name").CombinedOutput()
	return len(bytes.TrimSpace(output)) == 0, err
}

// DeleteAndWaitForCleanup deletes a resource, clears provider finalizers if needed,
// and waits until the API server removes it.
func DeleteAndWaitForCleanup(namespace, kind, name string) {
	output, err := exec.Command("kubectl", "delete", kind, name,
		"-n", namespace, "--ignore-not-found", "--wait=false").CombinedOutput()
	Expect(err).NotTo(HaveOccurred(), string(output))

	Eventually(func() (bool, error) {
		patch, marshalErr := json.Marshal(map[string]any{"metadata": map[string]any{
			"finalizers": []string{},
		}})
		Expect(marshalErr).NotTo(HaveOccurred())
		output, err := exec.Command("kubectl", "patch", kind, name,
			"-n", namespace, "--type=merge", "-p", string(patch)).CombinedOutput()
		if err == nil {
			return true, nil
		}
		absent, getErr := Absent(namespace, kind, name)
		if getErr != nil {
			return false, getErr
		}
		if absent {
			return true, nil
		}
		return false, fmt.Errorf("clear finalizers on %s/%s: %w: %s", kind, name, err, output)
	}, time.Minute, time.Second).Should(BeTrue())
	Eventually(func() (bool, error) { return Absent(namespace, kind, name) }, time.Minute, time.Second).Should(BeTrue())
}

// ReleaseFinalizer removes only the named finalizer and retries resource-version conflicts.
func ReleaseFinalizer(namespace, kind, name, finalizer string) {
	Eventually(func() error {
		current, err := Get(namespace, kind, name)
		if err != nil {
			return err
		}
		finalizers := slices.DeleteFunc(current.GetFinalizers(), func(value string) bool {
			return value == finalizer
		})
		patch, err := json.Marshal(map[string]any{"metadata": map[string]any{
			"resourceVersion": current.GetResourceVersion(),
			"finalizers":      finalizers,
		}})
		if err != nil {
			return err
		}
		output, err := exec.Command("kubectl", "patch", kind, name,
			"-n", namespace, "--type=merge", "-p", string(patch)).CombinedOutput()
		if err != nil {
			return fmt.Errorf("release %s/%s finalizer %q: %w: %s", kind, name, finalizer, err, output)
		}
		return nil
	}, time.Minute, time.Second).Should(Succeed())
}
