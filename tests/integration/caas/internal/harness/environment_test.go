// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package harness

import (
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/client-go/rest"
)

var _ = Describe("Opening the selected Kubernetes context", func() {
	It("uses the caller's kubeconfig without contacting its API", func() {
		By("writing a kubeconfig with caller-selected names")

		path := filepath.Join(GinkgoT().TempDir(), "caller.kubeconfig")
		data := []byte(`apiVersion: v1
kind: Config
current-context: caller-context
clusters:
- name: caller-cluster
  cluster:
    server: https://caller.invalid:6443
contexts:
- name: caller-context
  context:
    cluster: caller-cluster
    user: caller
users:
- name: caller
  user: {}
`)
		Expect(os.WriteFile(path, data, 0o600)).To(Succeed())
		GinkgoT().Setenv("KUBECONFIG", path)

		By("constructing clients for that context")

		environment, err := Open("osac")
		Expect(err).NotTo(HaveOccurred())
		Expect(environment.Kube.CoreV1().RESTClient().Get().URL().Host).To(Equal("caller.invalid:6443"))
		Expect(environment.Namespace).To(Equal("osac"))
		Expect(environment.WatchKube.CoreV1().RESTClient().Get().URL().Host).To(Equal("caller.invalid:6443"))
		Expect(environment.Kube.CoreV1().RESTClient().(*rest.RESTClient).Client.Timeout).To(Equal(20 * time.Second))
		Expect(environment.WatchKube.CoreV1().RESTClient().(*rest.RESTClient).Client.Timeout).To(BeZero())
	})
})
