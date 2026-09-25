/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package validate

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
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/osac-project/osac/fulfillment-service/internal/exit"
	"github.com/osac-project/osac/fulfillment-service/internal/logging"
	"github.com/osac-project/osac/fulfillment-service/internal/terminal"
	"github.com/osac-project/osac/osac-installer/pkg/install"
)

var crdGVR = schema.GroupVersionResource{
	Group: "apiextensions.k8s.io", Version: "v1", Resource: "customresourcedefinitions",
}

var provisioningGVR = schema.GroupVersionResource{
	Group: "metal3.io", Version: "v1alpha1", Resource: "provisionings",
}

var csvGVR = schema.GroupVersionResource{
	Group: "operators.coreos.com", Version: "v1alpha1", Resource: "clusterserviceversions",
}

// listKinds registers every custom-resource GVR any check in this suite
// might LIST, so a fake dynamic client doesn't panic — client-go's fake
// dynamic client requires every listed GVR's list kind be known up front,
// even when the list will be empty.
var listKinds = map[schema.GroupVersionResource]string{
	crdGVR:          "CustomResourceDefinitionList",
	provisioningGVR: "ProvisioningList",
	csvGVR:          "ClusterServiceVersionList",
}

func newUnstructuredCRD(name string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "apiextensions.k8s.io/v1",
		"kind":       "CustomResourceDefinition",
		"metadata":   map[string]any{"name": name},
	}}
}

func newUnstructuredCSV(namespace, name, phase, version string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "operators.coreos.com/v1alpha1",
		"kind":       "ClusterServiceVersion",
		"metadata":   map[string]any{"name": name, "namespace": namespace},
		"spec":       map[string]any{"version": version},
		"status":     map[string]any{"phase": phase},
	}}
}

// newPassingClients satisfies every "requiredFor: [all]" entry in the
// prerequisites matrix (cert-manager CRD and CSV, the AAP CSV, a default
// StorageClass) — everything a validate run with no --services narrowing
// still requires regardless of which services are requested.
func newPassingClients() *install.Clients {
	scheme := runtime.NewScheme()
	return &install.Clients{
		Typed: fake.NewSimpleClientset(&storagev1.StorageClass{
			ObjectMeta: metav1.ObjectMeta{
				Name:        "standard",
				Annotations: map[string]string{"storageclass.kubernetes.io/is-default-class": "true"},
			},
		}),
		Dynamic: dynamicfake.NewSimpleDynamicClientWithCustomListKinds(
			scheme,
			listKinds,
			newUnstructuredCRD("certificates.cert-manager.io"),
			newUnstructuredCSV("cert-manager-operator", "openshift-cert-manager-operator.v1.20.0", "Succeeded", "1.20.0"),
			newUnstructuredCSV("ansible-aap", "ansible-automation-platform-operator.v2.6.0", "Succeeded", "2.6.0"),
		),
	}
}

func newFailingClients() *install.Clients {
	return &install.Clients{
		Typed:   fake.NewSimpleClientset(),
		Dynamic: dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), listKinds),
	}
}

var _ = Describe("Validate command flags", func() {
	It("has the expected use string", func() {
		Expect(Cmd().Use).To(Equal("validate [FLAG...]"))
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

var _ = Describe("Validate command execution", func() {
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

	execute := func(runner *runnerContext, args ...string) error {
		cmd := newCmd(runner)
		cmd.SetOut(GinkgoWriter)
		cmd.SetErr(GinkgoWriter)
		cmd.SetContext(ctx)
		// args is nil, not an empty slice, when execute(runner) is called with
		// no trailing arguments (Go variadic semantics) -- cobra's SetArgs
		// treats a nil slice as "not set" and falls back to parsing os.Args,
		// which in a `go test`/ginkgo run includes flags like -test.timeout
		// that this command doesn't recognize. append() onto a fresh slice
		// guarantees a non-nil (possibly empty) slice either way.
		cmd.SetArgs(append([]string{}, args...))
		return cmd.Execute()
	}

	It("exits cleanly when every required check passes", func() {
		runner := &runnerContext{
			loadClients: func(string) (*install.Clients, error) { return newPassingClients(), nil },
		}

		// --services= narrows to just the "all"-scoped entries newPassingClients
		// satisfies; the default (every service) would also require
		// CNV/LVMS/MetalLB/MCE/Kafka CSVs this fixture doesn't seed.
		err := execute(runner, "--services=")

		Expect(err).ToNot(HaveOccurred())
		Expect(stdout.String()).To(ContainSubstring("PASS"))
	})

	It("exits with code 1 when a required check fails, and says why", func() {
		runner := &runnerContext{
			loadClients: func(string) (*install.Clients, error) { return newFailingClients(), nil },
		}

		err := execute(runner)

		Expect(err).To(HaveOccurred())
		var exitErr exit.Error
		Expect(errors.As(err, &exitErr)).To(BeTrue())
		Expect(exitErr.Code()).To(Equal(1))
		Expect(stdout.String()).To(ContainSubstring("cert-manager-crds"))
		Expect(stdout.String()).To(ContainSubstring("FAIL"))
		Expect(stderr.String()).To(ContainSubstring("not ready to install OSAC"))
	})

	It("does not fail on a Warning-only failure (unreadable OCP version, everything else present)", func() {
		scheme := runtime.NewScheme()
		clients := &install.Clients{
			Typed: fake.NewSimpleClientset(&storagev1.StorageClass{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "standard",
					Annotations: map[string]string{"storageclass.kubernetes.io/is-default-class": "true"},
				},
			}),
			// No ClusterVersion object -> ocp-version (Warning) fails; every
			// other "all"-scoped entry (including default-storageclass,
			// Required as of this test) passes.
			Dynamic: dynamicfake.NewSimpleDynamicClientWithCustomListKinds(
				scheme,
				listKinds,
				newUnstructuredCRD("certificates.cert-manager.io"),
				newUnstructuredCSV("cert-manager-operator", "openshift-cert-manager-operator.v1.20.0", "Succeeded", "1.20.0"),
				newUnstructuredCSV("ansible-aap", "ansible-automation-platform-operator.v2.6.0", "Succeeded", "2.6.0"),
			),
		}
		runner := &runnerContext{
			loadClients: func(string) (*install.Clients, error) { return clients, nil },
		}

		err := execute(runner, "--services=")

		Expect(err).ToNot(HaveOccurred())
		Expect(stdout.String()).To(ContainSubstring("FAIL")) // reported...
		Expect(stdout.String()).To(ContainSubstring("ocp-version"))
		// ...but did not block the exit code, since it's Warning severity.
	})

	It("requires the Metal3 checks only when both bmaas and --metal3 are requested", func() {
		runner := &runnerContext{
			loadClients: func(string) (*install.Clients, error) { return newPassingClients(), nil },
		}

		errWithout := execute(runner, "--services=")
		Expect(errWithout).ToNot(HaveOccurred())

		stdout.Reset()
		stderr.Reset()
		runnerWithMetal3 := &runnerContext{
			loadClients: func(string) (*install.Clients, error) { return newPassingClients(), nil },
		}
		errWith := execute(runnerWithMetal3, "--services=bmaas", "--metal3")

		Expect(errWith).To(HaveOccurred())
		var exitErr exit.Error
		Expect(errors.As(errWith, &exitErr)).To(BeTrue())
		Expect(stdout.String()).To(ContainSubstring("metal3-baremetalhost-crd"))
	})

	It("exits with code 1 and a clear error when the kubeconfig can't be loaded", func() {
		runner := &runnerContext{
			loadClients: func(string) (*install.Clients, error) {
				return nil, errors.New("no such file or directory")
			},
		}

		err := execute(runner)

		Expect(err).To(HaveOccurred())
		var exitErr exit.Error
		Expect(errors.As(err, &exitErr)).To(BeTrue())
		Expect(exitErr.Code()).To(Equal(1))
		Expect(stderr.String()).To(ContainSubstring("Failed to connect to the Hub cluster"))
	})

	It("prints JSON instead of a table when --json is set, with no intro line, and still gates on required failures", func() {
		runner := &runnerContext{
			loadClients: func(string) (*install.Clients, error) { return newFailingClients(), nil },
		}

		err := execute(runner, "--json")

		Expect(err).To(HaveOccurred())
		var exitErr exit.Error
		Expect(errors.As(err, &exitErr)).To(BeTrue())
		Expect(exitErr.Code()).To(Equal(1))
		Expect(stdout.String()).NotTo(ContainSubstring("Validating Hub cluster prerequisites"))
		var report struct {
			Ready   bool `json:"ready"`
			Results []struct {
				Check string `json:"check"`
			} `json:"results"`
		}
		Expect(json.Unmarshal(stdout.Bytes(), &report)).To(Succeed())
		Expect(report.Ready).To(BeFalse())
		Expect(report.Results).NotTo(BeEmpty())
	})

	It("rejects a malformed --require-secret value", func() {
		runner := &runnerContext{
			loadClients: func(string) (*install.Clients, error) { return newPassingClients(), nil },
		}

		err := execute(runner, "--services=", "--require-secret=not-valid")

		Expect(err).To(HaveOccurred())
		var exitErr exit.Error
		Expect(errors.As(err, &exitErr)).To(BeTrue())
		Expect(exitErr.Code()).To(Equal(1))
		Expect(stderr.String()).To(ContainSubstring("Invalid --require-secret value"))
	})

	It("requires a Secret named by --require-secret", func() {
		runner := &runnerContext{
			loadClients: func(string) (*install.Clients, error) { return newPassingClients(), nil },
		}

		err := execute(runner, "--services=", "--require-secret=osac/config-as-code-manifest-ig")

		Expect(err).To(HaveOccurred())
		Expect(stdout.String()).To(ContainSubstring("secret-osac-config-as-code-manifest-ig"))
		Expect(stdout.String()).To(ContainSubstring("FAIL"))
	})
})
