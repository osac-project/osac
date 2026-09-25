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
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
)

var _ = Describe("loadRESTConfig", func() {
	// Every case below runs with HOME pointed at an empty temp directory and
	// KUBECONFIG cleared, so ~/.kube/config never resolves to a real file
	// and the real developer/CI environment is never touched.
	BeforeEach(func() {
		GinkgoT().Setenv("HOME", GinkgoT().TempDir())
		GinkgoT().Setenv("KUBECONFIG", "")
	})

	It("returns a plain 'file not found' error for an explicit path that doesn't exist", func() {
		_, err := loadRESTConfig(filepath.Join(GinkgoT().TempDir(), "does-not-exist.yaml"))

		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("kubeconfig file not found"))
		Expect(errors.Is(err, ErrNoClusterConfig)).To(BeFalse())
	})

	It("returns ErrNoClusterConfig when nothing resolves and there's no in-cluster config", func() {
		_, err := loadRESTConfig("")

		Expect(errors.Is(err, ErrNoClusterConfig)).To(BeTrue())
	})

	It("surfaces clientcmd's own error, not ErrNoClusterConfig, for a kubeconfig file that exists but is malformed", func() {
		path := filepath.Join(GinkgoT().TempDir(), "config")
		Expect(os.WriteFile(path, []byte("not: valid: kubeconfig: yaml: ["), 0o600)).To(Succeed())

		_, err := loadRESTConfig(path)

		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, ErrNoClusterConfig)).To(BeFalse())
	})

	It("loads a well-formed kubeconfig at an explicit path", func() {
		path := filepath.Join(GinkgoT().TempDir(), "config")
		Expect(os.WriteFile(path, []byte(minimalKubeconfig), 0o600)).To(Succeed())

		restConfig, err := loadRESTConfig(path)

		Expect(err).NotTo(HaveOccurred())
		Expect(restConfig.Host).To(Equal("https://example.invalid:6443"))
	})

	It("finds a kubeconfig via the KUBECONFIG environment variable when no explicit path is given", func() {
		path := filepath.Join(GinkgoT().TempDir(), "config")
		Expect(os.WriteFile(path, []byte(minimalKubeconfig), 0o600)).To(Succeed())
		GinkgoT().Setenv("KUBECONFIG", path)

		restConfig, err := loadRESTConfig("")

		Expect(err).NotTo(HaveOccurred())
		Expect(restConfig.Host).To(Equal("https://example.invalid:6443"))
	})
})

var _ = Describe("LoadClients", func() {
	BeforeEach(func() {
		GinkgoT().Setenv("HOME", GinkgoT().TempDir())
		GinkgoT().Setenv("KUBECONFIG", "")
	})

	It("fails with one clear error, not a partial client, when the kubeconfig resolves but the server is unreachable", func() {
		// A kubeconfig can parse fine yet point at a server that simply
		// isn't there -- a stale local port-forward, a torn-down cluster.
		// This reproduces that: bind a listener to get a genuinely free
		// port, then close it, so the connection is refused immediately.
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).NotTo(HaveOccurred())
		addr := listener.Addr().String()
		Expect(listener.Close()).To(Succeed())

		path := filepath.Join(GinkgoT().TempDir(), "config")
		Expect(os.WriteFile(path, []byte(fmt.Sprintf(unreachableKubeconfig, addr)), 0o600)).To(Succeed())

		clients, err := LoadClients(path)

		Expect(clients).To(BeNil())
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring(addr))
	})
})

const unreachableKubeconfig = `
apiVersion: v1
kind: Config
clusters:
- name: test
  cluster:
    server: https://%s
    insecure-skip-tls-verify: true
contexts:
- name: test
  context:
    cluster: test
    user: test
current-context: test
users:
- name: test
  user:
    token: fake-token
`

const minimalKubeconfig = `
apiVersion: v1
kind: Config
clusters:
- name: test
  cluster:
    server: https://example.invalid:6443
    insecure-skip-tls-verify: true
contexts:
- name: test
  context:
    cluster: test
    user: test
current-context: test
users:
- name: test
  user:
    token: fake-token
`
