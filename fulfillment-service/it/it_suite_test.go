/*
Copyright (c) 2025 Red Hat Inc.

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
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/go-logr/logr"
	"github.com/kelseyhightower/envconfig"
	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/ginkgo/v2/dsl/decorators"
	. "github.com/onsi/gomega"
	"k8s.io/klog/v2"
	crlog "sigs.k8s.io/controller-runtime/pkg/log"

	privatev1 "github.com/osac-project/fulfillment-service/internal/api/osac/private/v1"
	"github.com/osac-project/fulfillment-service/internal/logging"
)

// Config contains configuration options for the integration tests.
type Config struct {
	// KeepKind indicates whether to preserve the kind cluster after tests complete.
	// By default, the kind cluster is deleted after running the tests.
	KeepKind bool `json:"keep_kind" envconfig:"keep_kind" default:"false"`

	// KeepService indicates whether to preserve the application chart after tests complete.
	// By default, the application chart is uninstalled after running the tests.
	KeepService bool `json:"keep_service" envconfig:"keep_service" default:"false"`

	// Debug indicates if the debug mode should be enabled. This means that the debugger binary will be added to
	// the container image, and that the services will be started under the control of the debugger. Access to the
	// debugger will be done via the following ports:
	//
	// - gRPC server: 30001
	// - REST gateway: 30002
	// - Controller: 30003
	Debug bool `json:"debug" envconfig:"debug" default:"false"`

	// Secret is the secret used in all places where passwords or secrets are needed, such as service account
	// client secrets and user passwords. If the environment variable is set then that value will be used, otherwise
	// a random one will be generated.
	Secret string `json:"secret" envconfig:"secret" default:""`

	// CaKey is the path to a PEM file containing a pre-generated CA private key. When both CaKey and CaCrt are
	// set, the integration tests will use these files instead of generating a new CA each run.
	CaKey string `json:"ca_key" envconfig:"ca_key" default:""`

	// CaCrt is the path to a PEM file containing a pre-generated CA certificate. When both CaKey and CaCrt are
	// set, the integration tests will use these files instead of generating a new CA each run.
	CaCrt string `json:"ca_crt" envconfig:"ca_crt" default:""`
}

var (
	logger *slog.Logger
	config *Config
	tool   *Tool
)

func TestIntegration(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Integration")
}

var _ = BeforeSuite(func() {
	var err error

	// Create a context:
	ctx := context.Background()

	// Create the logger:
	logger, err = logging.NewLogger().
		SetWriter(GinkgoWriter).
		SetLevel(slog.LevelDebug.String()).
		Build()
	Expect(err).ToNot(HaveOccurred())

	// Configure the Kubernetes libraries to use our logger:
	logrLogger := logr.FromSlogHandler(logger.Handler())
	crlog.SetLogger(logrLogger)
	klog.SetLogger(logrLogger)

	// Load configuration from environment variables:
	config = &Config{}
	err = envconfig.Process("it", config)
	Expect(err).ToNot(HaveOccurred())
	logger.Info(
		"Configuration",
		slog.Bool("keep_kind", config.KeepKind),
		slog.Bool("keep_service", config.KeepService),
		slog.Bool("debug", config.Debug),
		slog.String("!secret", config.Secret),
		slog.Bool("ca_key_set", config.CaKey != ""),
		slog.Bool("ca_crt_set", config.CaCrt != ""),
	)

	// Create and setup the tool:
	tool, err = NewTool().
		SetLogger(logger).
		SetKeepCluster(config.KeepKind).
		SetKeepService(config.KeepService).
		SetDebug(config.Debug).
		SetSecret(config.Secret).
		SetCaFiles(config.CaKey, config.CaCrt).
		AddCrdFile(filepath.Join("crds", "clusterorders.osac.openshift.io.yaml")).
		AddCrdFile(filepath.Join("crds", "hostedclusters.hypershift.openshift.io.yaml")).
		AddCrdFile(filepath.Join("crds", "tenants.osac.openshift.io.yaml")).
		AddCrdFile(filepath.Join("crds", "osac.openshift.io_baremetalinstances.yaml")).
		Build()
	Expect(err).ToNot(HaveOccurred())
	err = tool.Setup(ctx)
	Expect(err).ToNot(HaveOccurred())
	DeferCleanup(func() {
		err := tool.Cleanup(ctx)
		Expect(err).ToNot(HaveOccurred())
	})

	// Create a default cluster version for version resolution during cluster creation:
	cvClient := privatev1.NewClusterVersionsClient(tool.InternalView().AdminConn())
	_, err = cvClient.Create(ctx, privatev1.ClusterVersionsCreateRequest_builder{
		Object: privatev1.ClusterVersion_builder{
			Spec: privatev1.ClusterVersionSpec_builder{
				Version:   "4.17.0",
				Image:     "quay.io/openshift-release-dev/ocp-release:4.17.0-multi",
				IsDefault: new(true),
			}.Build(),
		}.Build(),
	}.Build())
	Expect(err).ToNot(HaveOccurred())
})

var _ = Describe("Integration", func() {
	It("Setup", Label("setup"), func() {
		// This is a dummy test to have a mechanism to run the setup of the integration tests without running
		// any actual tests, with a command like this:
		//
		// ginkgo run --label-filter setup it
		//
		// This will create the kind cluster, install the dependencies, and deploy the application.
	})
})
