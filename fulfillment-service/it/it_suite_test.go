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
	"testing"

	"github.com/go-logr/logr"
	"github.com/kelseyhightower/envconfig"
	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
	"k8s.io/klog/v2"
	crlog "sigs.k8s.io/controller-runtime/pkg/log"

	privatev1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/private/v1"
	"github.com/osac-project/osac/fulfillment-service/internal/logging"
)

// Config contains configuration options for the integration tests.
type Config struct {
	// Secret is the secret used in all places where passwords or secrets are needed, such as service account
	// client secrets and user passwords. If the environment variable is set then that value will be used, otherwise
	// a random one will be generated.
	Secret string `json:"secret" envconfig:"secret" default:""`
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
		slog.String("!secret", config.Secret),
	)

	// Create and setup the tool:
	tool, err = NewTool().
		SetLogger(logger).
		SetSecret(config.Secret).
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
			Metadata: privatev1.Metadata_builder{
				Name: "default",
			}.Build(),
			Spec: privatev1.ClusterVersionSpec_builder{
				Version:   "4.17.0",
				Image:     "quay.io/openshift-release-dev/ocp-release:4.17.0-multi",
				IsDefault: new(true),
			}.Build(),
		}.Build(),
	}.Build())
	Expect(err).ToNot(HaveOccurred())
})
