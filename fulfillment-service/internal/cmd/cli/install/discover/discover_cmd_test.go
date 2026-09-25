/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package discover

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/osac-project/osac/fulfillment-service/internal/exit"
	"github.com/osac-project/osac/fulfillment-service/internal/logging"
	"github.com/osac-project/osac/fulfillment-service/internal/terminal"
	"github.com/osac-project/osac/osac-installer/pkg/install"
)

// listKinds registers every custom-resource GVR any check in this suite
// might LIST, so a fake dynamic client with zero seed objects doesn't
// panic — client-go's fake dynamic client requires every listed GVR's list
// kind be known up front, even when the list will be empty.
var listKinds = map[schema.GroupVersionResource]string{
	{Group: "metal3.io", Version: "v1alpha1", Resource: "provisionings"}:                     "ProvisioningList",
	{Group: "operators.coreos.com", Version: "v1alpha1", Resource: "clusterserviceversions"}: "ClusterServiceVersionList",
}

func newEmptyDynamicClient() *dynamicfake.FakeDynamicClient {
	return dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), listKinds)
}

var _ = Describe("Discover command flags", func() {
	It("has the expected use string", func() {
		Expect(Cmd().Use).To(Equal("discover [FLAG...]"))
	})

	It("has short and long help text", func() {
		cmd := Cmd()
		Expect(cmd.Short).ToNot(BeEmpty())
		Expect(cmd.Long).ToNot(BeEmpty())
	})

	It("rejects positional arguments", func() {
		cmd := Cmd()
		Expect(cmd.Args(cmd, []string{"unexpected"})).To(HaveOccurred())
	})
})

var _ = Describe("Discover command execution", func() {
	var (
		ctx    context.Context
		stdout *bytes.Buffer
		stderr *bytes.Buffer
	)

	BeforeEach(func() {
		ctx = context.Background()
		ctx = logging.LoggerIntoContext(ctx, slog.Default())
		stdout = &bytes.Buffer{}
		stderr = &bytes.Buffer{}

		console, err := terminal.NewConsole().
			SetLogger(slog.Default()).
			SetStdout(stdout).
			SetStderr(stderr).
			Build()
		Expect(err).ToNot(HaveOccurred())
		ctx = terminal.ConsoleIntoContext(ctx, console)
	})

	It("reports a passing check and exits cleanly", func() {
		runner := &runnerContext{
			loadClients: func(string) (*install.Clients, error) {
				return &install.Clients{
					Typed: fake.NewSimpleClientset(&storagev1.StorageClass{
						ObjectMeta: metav1.ObjectMeta{
							Name:        "standard",
							Annotations: map[string]string{"storageclass.kubernetes.io/is-default-class": "true"},
						},
					}),
					Dynamic: newEmptyDynamicClient(),
				}, nil
			},
		}
		cmd := newCmd(runner)
		cmd.SetOut(GinkgoWriter)
		cmd.SetErr(GinkgoWriter)
		cmd.SetContext(ctx)
		cmd.SetArgs([]string{})

		err := cmd.Execute()

		Expect(err).ToNot(HaveOccurred())
		Expect(stdout.String()).To(ContainSubstring("default-storageclass"))
		Expect(stdout.String()).To(ContainSubstring("PASS"))
	})

	It("still exits cleanly and reports FAIL when a required check fails — discover never gates", func() {
		runner := &runnerContext{
			loadClients: func(string) (*install.Clients, error) {
				return &install.Clients{
					Typed:   fake.NewSimpleClientset(),
					Dynamic: newEmptyDynamicClient(),
				}, nil
			},
		}
		cmd := newCmd(runner)
		cmd.SetOut(GinkgoWriter)
		cmd.SetErr(GinkgoWriter)
		cmd.SetContext(ctx)
		cmd.SetArgs([]string{})

		err := cmd.Execute()

		Expect(err).ToNot(HaveOccurred())
		Expect(stdout.String()).To(ContainSubstring("cert-manager-crds"))
		Expect(stdout.String()).To(ContainSubstring("FAIL"))
	})

	It("includes Metal3 checks only when --metal3 is set", func() {
		runner := &runnerContext{
			loadClients: func(string) (*install.Clients, error) {
				return &install.Clients{
					Typed:   fake.NewSimpleClientset(),
					Dynamic: newEmptyDynamicClient(),
				}, nil
			},
		}
		cmd := newCmd(runner)
		cmd.SetOut(GinkgoWriter)
		cmd.SetErr(GinkgoWriter)
		cmd.SetContext(ctx)
		cmd.SetArgs([]string{"--services=bmaas", "--metal3"})

		err := cmd.Execute()

		Expect(err).ToNot(HaveOccurred())
		Expect(stdout.String()).To(ContainSubstring("metal3-baremetalhost-crd"))
	})

	It("defaults to every service, so CNV (vmaas-only) is reported with no --services flag", func() {
		runner := &runnerContext{
			loadClients: func(string) (*install.Clients, error) {
				return &install.Clients{
					Typed:   fake.NewSimpleClientset(),
					Dynamic: newEmptyDynamicClient(),
				}, nil
			},
		}
		cmd := newCmd(runner)
		cmd.SetOut(GinkgoWriter)
		cmd.SetErr(GinkgoWriter)
		cmd.SetContext(ctx)
		cmd.SetArgs([]string{})

		err := cmd.Execute()

		Expect(err).ToNot(HaveOccurred())
		Expect(stdout.String()).To(ContainSubstring("cnv-operator"))
	})

	It("prints JSON instead of a table when --json is set, with no intro line", func() {
		runner := &runnerContext{
			loadClients: func(string) (*install.Clients, error) {
				return &install.Clients{
					Typed: fake.NewSimpleClientset(&storagev1.StorageClass{
						ObjectMeta: metav1.ObjectMeta{
							Name:        "standard",
							Annotations: map[string]string{"storageclass.kubernetes.io/is-default-class": "true"},
						},
					}),
					Dynamic: newEmptyDynamicClient(),
				}, nil
			},
		}
		cmd := newCmd(runner)
		cmd.SetOut(GinkgoWriter)
		cmd.SetErr(GinkgoWriter)
		cmd.SetContext(ctx)
		cmd.SetArgs([]string{"--services=", "--json"})

		err := cmd.Execute()

		Expect(err).ToNot(HaveOccurred())
		Expect(stdout.String()).NotTo(ContainSubstring("Checking Hub cluster prerequisites"))
		var report struct {
			Ready   bool `json:"ready"`
			Results []struct {
				Check string `json:"check"`
			} `json:"results"`
		}
		Expect(json.Unmarshal(stdout.Bytes(), &report)).To(Succeed())
		Expect(report.Results).NotTo(BeEmpty())
	})

	It("exits with code 1 and a clear error when the kubeconfig can't be loaded", func() {
		runner := &runnerContext{
			loadClients: func(string) (*install.Clients, error) {
				return nil, errors.New("no such file or directory")
			},
		}
		cmd := newCmd(runner)
		cmd.SetOut(GinkgoWriter)
		cmd.SetErr(GinkgoWriter)
		cmd.SetContext(ctx)
		cmd.SetArgs([]string{})

		err := cmd.Execute()

		Expect(err).To(HaveOccurred())
		var exitErr exit.Error
		Expect(errors.As(err, &exitErr)).To(BeTrue())
		Expect(exitErr.Code()).To(Equal(1))
		Expect(stderr.String()).To(ContainSubstring("Failed to connect to the Hub cluster"))
	})

	It("passes the --kubeconfig flag value through to loadClients", func() {
		var gotPath string
		runner := &runnerContext{
			loadClients: func(path string) (*install.Clients, error) {
				gotPath = path
				return &install.Clients{
					Typed:   fake.NewSimpleClientset(),
					Dynamic: newEmptyDynamicClient(),
				}, nil
			},
		}
		cmd := newCmd(runner)
		cmd.SetOut(GinkgoWriter)
		cmd.SetErr(GinkgoWriter)
		cmd.SetContext(ctx)
		cmd.SetArgs([]string{"--kubeconfig", "/tmp/my-kubeconfig"})

		err := cmd.Execute()

		Expect(err).ToNot(HaveOccurred())
		Expect(gotPath).To(Equal("/tmp/my-kubeconfig"))
	})
})
