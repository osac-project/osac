/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package status

import (
	"bytes"
	"context"
	"errors"
	"log/slog"

	tea "charm.land/bubbletea/v2"
	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/osac-project/osac/fulfillment-service/internal/exit"
	"github.com/osac-project/osac/fulfillment-service/internal/logging"
	"github.com/osac-project/osac/fulfillment-service/internal/terminal"
	"github.com/osac-project/osac/osac-installer/pkg/install"
)

var listKinds = map[schema.GroupVersionResource]string{
	{Group: "operators.coreos.com", Version: "v1alpha1", Resource: "clusterserviceversions"}: "ClusterServiceVersionList",
}

func newEmptyDynamicClient() *dynamicfake.FakeDynamicClient {
	return dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), listKinds)
}

var _ = Describe("Status command flags", func() {
	It("has the expected use string", func() {
		Expect(Cmd().Use).To(Equal("status [FLAG...]"))
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

var _ = Describe("Status command execution", func() {
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

	It("prints a one-shot status view and exits cleanly, without --watch", func() {
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
		cmd.SetArgs([]string{"--services="})

		err := cmd.Execute()

		Expect(err).ToNot(HaveOccurred())
		Expect(stdout.String()).To(ContainSubstring("ready"))
		Expect(stdout.String()).To(ContainSubstring("cert-manager-crds"))
	})

	It("never fails the command on a failed check -- status is a report, not a gate", func() {
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

	It("rejects a malformed --require-secret value", func() {
		runner := &runnerContext{
			loadClients: func(string) (*install.Clients, error) {
				return &install.Clients{Typed: fake.NewSimpleClientset(), Dynamic: newEmptyDynamicClient()}, nil
			},
		}
		cmd := newCmd(runner)
		cmd.SetOut(GinkgoWriter)
		cmd.SetErr(GinkgoWriter)
		cmd.SetContext(ctx)
		cmd.SetArgs([]string{"--require-secret=not-valid"})

		err := cmd.Execute()

		Expect(err).To(HaveOccurred())
		Expect(stderr.String()).To(ContainSubstring("Invalid --require-secret value"))
	})

	It("with --watch, runs the interactive program instead of printing once", func() {
		var gotModel tea.Model
		runner := &runnerContext{
			loadClients: func(string) (*install.Clients, error) {
				return &install.Clients{Typed: fake.NewSimpleClientset(), Dynamic: newEmptyDynamicClient()}, nil
			},
			runProgram: func(m tea.Model) error {
				gotModel = m
				return nil
			},
		}
		cmd := newCmd(runner)
		cmd.SetOut(GinkgoWriter)
		cmd.SetErr(GinkgoWriter)
		cmd.SetContext(ctx)
		cmd.SetArgs([]string{"--watch"})

		err := cmd.Execute()

		Expect(err).ToNot(HaveOccurred())
		Expect(gotModel).NotTo(BeNil())
		Expect(stdout.String()).To(BeEmpty()) // nothing printed directly; the program owns rendering
	})

	It("propagates a --watch program error as exit code 1", func() {
		runner := &runnerContext{
			loadClients: func(string) (*install.Clients, error) {
				return &install.Clients{Typed: fake.NewSimpleClientset(), Dynamic: newEmptyDynamicClient()}, nil
			},
			runProgram: func(tea.Model) error {
				return errors.New("not a terminal")
			},
		}
		cmd := newCmd(runner)
		cmd.SetOut(GinkgoWriter)
		cmd.SetErr(GinkgoWriter)
		cmd.SetContext(ctx)
		cmd.SetArgs([]string{"--watch"})

		err := cmd.Execute()

		Expect(err).To(HaveOccurred())
		var exitErr exit.Error
		Expect(errors.As(err, &exitErr)).To(BeTrue())
		Expect(exitErr.Code()).To(Equal(1))
	})
})
