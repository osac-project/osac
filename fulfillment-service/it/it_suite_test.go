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
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"k8s.io/klog/v2"
	crlog "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/osac-project/osac/fulfillment-service/internal/logging"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

// Config contains configuration options for the integration tests.
type Config struct {
	// Secret is the secret used in all places where passwords or secrets are needed, such as service account
	// client secrets and user passwords. If the environment variable is set then that value will be used, otherwise
	// a random one will be generated.
	Secret string `json:"secret" envconfig:"secret" default:""`

	// TestSuite selects the scenario exercised by a focused integration test run.
	TestSuite string `json:"test_suite" envconfig:"test_suite" default:""`
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

	// The system default is used by bare-metal cluster tests, so it must reference a
	// DiskImage. Leave both fixtures in place to support reruns against a live cluster.
	imageClient := privatev1.NewDiskImagesClient(tool.InternalView().AdminConn())
	_, err = imageClient.Create(ctx, privatev1.DiskImagesCreateRequest_builder{
		Object: privatev1.DiskImage_builder{
			Metadata: privatev1.Metadata_builder{Name: "it-default-cluster-image", Tenant: "shared"}.Build(),
			Spec: privatev1.DiskImageSpec_builder{
				SourceType:    privatev1.SourceType_SOURCE_TYPE_REGISTRY,
				SourceRef:     "quay.io/containerdisks/fedora:41",
				GuestOsFamily: privatev1.GuestOSFamily_GUEST_OS_FAMILY_LINUX,
				Architecture:  []privatev1.Architecture{privatev1.Architecture_ARCHITECTURE_AMD64},
			}.Build(),
		}.Build(),
	}.Build())
	if err != nil {
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.AlreadyExists), "BeforeSuite DiskImage create failed: %v", err)
	}
	images, err := imageClient.List(ctx, privatev1.DiskImagesListRequest_builder{
		Filter: new(`this.metadata.name == "it-default-cluster-image" && this.metadata.tenant == "shared"`),
	}.Build())
	Expect(err).NotTo(HaveOccurred())
	Expect(images.GetItems()).To(HaveLen(1))
	imageID := images.GetItems()[0].GetId()

	cvClient := privatev1.NewClusterVersionsClient(tool.InternalView().AdminConn())
	_, err = cvClient.Create(ctx, privatev1.ClusterVersionsCreateRequest_builder{
		Object: privatev1.ClusterVersion_builder{
			Metadata: privatev1.Metadata_builder{Name: "default"}.Build(),
			Spec: privatev1.ClusterVersionSpec_builder{
				Version:   "4.17.0",
				Image:     "quay.io/openshift-release-dev/ocp-release:4.17.0-multi",
				IsDefault: new(true),
				DiskImage: privatev1.DiskImageReference_builder{Id: imageID}.Build(),
			}.Build(),
		}.Build(),
	}.Build())
	if err != nil {
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.AlreadyExists), "BeforeSuite ClusterVersion create failed: %v", err)
		versions, listErr := cvClient.List(ctx, privatev1.ClusterVersionsListRequest_builder{
			Filter: new(`this.metadata.name == "default"`),
		}.Build())
		Expect(listErr).NotTo(HaveOccurred())
		Expect(versions.GetItems()).To(HaveLen(1))
		version := versions.GetItems()[0]
		if version.GetSpec().GetDiskImage().GetId() == "" && version.GetSpec().GetDiskImage().GetName() == "" {
			_, err = cvClient.Update(ctx, privatev1.ClusterVersionsUpdateRequest_builder{
				Object: privatev1.ClusterVersion_builder{
					Id: version.GetId(),
					Spec: privatev1.ClusterVersionSpec_builder{
						DiskImage: privatev1.DiskImageReference_builder{Id: imageID}.Build(),
					}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.disk_image"}},
			}.Build())
			Expect(err).NotTo(HaveOccurred())
		}
	}
})
