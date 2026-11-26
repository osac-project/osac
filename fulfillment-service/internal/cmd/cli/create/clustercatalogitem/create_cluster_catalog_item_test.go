/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package clustercatalogitem_test

import (
	"context"
	"io"
	"log/slog"
	"strings"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"

	"github.com/osac-project/osac/fulfillment-service/internal/cmd/cli"
	"github.com/osac-project/osac/fulfillment-service/internal/config"
	"github.com/osac-project/osac/fulfillment-service/internal/logging"
	testutils "github.com/osac-project/osac/fulfillment-service/internal/testing"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

type sharedTemplateServer struct {
	publicv1.UnimplementedClusterTemplatesServer
	filter string
}

func (s *sharedTemplateServer) List(
	_ context.Context, request *publicv1.ClusterTemplatesListRequest,
) (*publicv1.ClusterTemplatesListResponse, error) {
	s.filter = request.GetFilter()
	if !strings.Contains(s.filter, `"shared"`) {
		return publicv1.ClusterTemplatesListResponse_builder{Size: 0, Total: 0}.Build(), nil
	}
	template := publicv1.ClusterTemplate_builder{
		Id: "shared-template-id",
		Metadata: publicv1.Metadata_builder{
			Name:   "shared-template",
			Tenant: "shared",
		}.Build(),
	}.Build()
	return publicv1.ClusterTemplatesListResponse_builder{
		Items: []*publicv1.ClusterTemplate{template},
		Size:  1,
		Total: 1,
	}.Build(), nil
}

type catalogItemsServer struct {
	publicv1.UnimplementedClusterCatalogItemsServer
	request *publicv1.ClusterCatalogItemsCreateRequest
}

func (s *catalogItemsServer) Create(
	_ context.Context, request *publicv1.ClusterCatalogItemsCreateRequest,
) (*publicv1.ClusterCatalogItemsCreateResponse, error) {
	s.request = request
	return publicv1.ClusterCatalogItemsCreateResponse_builder{
		Object: request.GetObject(),
	}.Build(), nil
}

var _ = Describe("Create cluster catalog item", func() {
	It("resolves a shared template while creating in a selected tenant", func() {
		server := testutils.NewServer()
		DeferCleanup(server.Stop)
		templates := &sharedTemplateServer{}
		items := &catalogItemsServer{}
		publicv1.RegisterClusterTemplatesServer(server.Registrar(), templates)
		publicv1.RegisterClusterCatalogItemsServer(server.Registrar(), items)
		server.Start()

		logger := slog.New(slog.NewTextHandler(io.Discard, nil))
		configDir := GinkgoT().TempDir()
		settings, err := config.NewSettings().
			SetLogger(logger).
			SetDir(configDir).
			Build()
		Expect(err).NotTo(HaveOccurred())
		settings.SetAddress(server.Address())
		settings.SetPlaintext(true)
		ctx := logging.LoggerIntoContext(context.Background(), logger)
		Expect(settings.Save(ctx)).To(Succeed())

		root, err := cli.Root()
		Expect(err).NotTo(HaveOccurred())
		root.SetOut(io.Discard)
		root.SetErr(io.Discard)
		root.SetArgs([]string{
			"--config", configDir,
			"--cache", GinkgoT().TempDir(),
			"--tenant", "tenant-a",
			"--project", "project-a",
			"create", "clustercatalogitem",
			"--template", "shared-template",
			"--name", "tenant-item",
		})
		Expect(root.ExecuteContext(ctx)).To(Succeed())

		Expect(templates.filter).To(ContainSubstring(
			`this.metadata.tenant == "tenant-a" || this.metadata.tenant == "shared"`,
		))
		Expect(items.request).NotTo(BeNil())
		Expect(items.request.GetObject().GetMetadata().GetTenant()).To(Equal("tenant-a"))
		Expect(items.request.GetObject().GetMetadata().GetProject()).To(Equal("project-a"))
		Expect(items.request.GetObject().GetTemplate().GetId()).To(Equal("shared-template-id"))
	})
})
