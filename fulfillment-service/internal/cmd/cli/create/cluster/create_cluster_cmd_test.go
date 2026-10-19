/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package cluster

import (
	"context"
	"errors"
	"log/slog"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc"

	"github.com/osac-project/osac/fulfillment-service/internal/config"
	"github.com/osac-project/osac/fulfillment-service/internal/exit"
	"github.com/osac-project/osac/fulfillment-service/internal/logging"
	"github.com/osac-project/osac/fulfillment-service/internal/terminal"
	testutils "github.com/osac-project/osac/fulfillment-service/internal/testing"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

// mockClusterVersionsClient is a minimal mock that intercepts List calls.
type mockClusterVersionsClient struct {
	publicv1.ClusterVersionsClient
	listFunc func(ctx context.Context, req *publicv1.ClusterVersionsListRequest, opts ...grpc.CallOption) (*publicv1.ClusterVersionsListResponse, error)
}

func (m *mockClusterVersionsClient) List(ctx context.Context, req *publicv1.ClusterVersionsListRequest, opts ...grpc.CallOption) (*publicv1.ClusterVersionsListResponse, error) {
	return m.listFunc(ctx, req, opts...)
}

type mockClusterCatalogItemsServer struct {
	publicv1.UnimplementedClusterCatalogItemsServer
}

func (mockClusterCatalogItemsServer) List(context.Context, *publicv1.ClusterCatalogItemsListRequest) (*publicv1.ClusterCatalogItemsListResponse, error) {
	return publicv1.ClusterCatalogItemsListResponse_builder{
		Items: []*publicv1.ClusterCatalogItem{
			publicv1.ClusterCatalogItem_builder{Id: "catalog-item-id"}.Build(),
		},
		Size:  1,
		Total: 1,
	}.Build(), nil
}

type mockClusterTemplatesServer struct {
	publicv1.UnimplementedClusterTemplatesServer
}

func (mockClusterTemplatesServer) List(context.Context, *publicv1.ClusterTemplatesListRequest) (*publicv1.ClusterTemplatesListResponse, error) {
	return publicv1.ClusterTemplatesListResponse_builder{
		Items: []*publicv1.ClusterTemplate{
			publicv1.ClusterTemplate_builder{Id: "template-id"}.Build(),
		},
		Size:  1,
		Total: 1,
	}.Build(), nil
}

var _ = Describe("Create cluster flag registration", func() {
	It("should register --ssh-key without removing inline compatibility flags", func() {
		cmd := Cmd()
		Expect(cmd.Flags().Lookup("ssh-key")).ToNot(BeNil())
		Expect(cmd.Flags().Lookup("ssh-public-key")).ToNot(BeNil())
		Expect(cmd.Flags().Lookup("ssh-public-key-file")).ToNot(BeNil())
	})

	It("should register --catalog-item flag", func() {
		cmd := Cmd()
		cmd.SetOut(GinkgoWriter)
		cmd.SetErr(GinkgoWriter)
		flag := cmd.Flags().Lookup("catalog-item")
		Expect(flag).NotTo(BeNil())
		Expect(flag.Usage).To(ContainSubstring("Catalog item"))
	})

	It("should still register --template flag", func() {
		cmd := Cmd()
		cmd.SetOut(GinkgoWriter)
		cmd.SetErr(GinkgoWriter)
		flag := cmd.Flags().Lookup("template")
		Expect(flag).NotTo(BeNil())
	})

	It("should register --catalog-item without a short flag", func() {
		cmd := Cmd()
		cmd.SetOut(GinkgoWriter)
		cmd.SetErr(GinkgoWriter)
		flag := cmd.Flags().Lookup("catalog-item")
		Expect(flag).NotTo(BeNil())
		Expect(flag.Shorthand).To(BeEmpty())
	})

	It("should keep -t as shorthand for --template", func() {
		cmd := Cmd()
		cmd.SetOut(GinkgoWriter)
		cmd.SetErr(GinkgoWriter)
		flag := cmd.Flags().Lookup("template")
		Expect(flag).NotTo(BeNil())
		Expect(flag.Shorthand).To(Equal("t"))
	})

	It("should register --node-set flag", func() {
		cmd := Cmd()
		cmd.SetOut(GinkgoWriter)
		cmd.SetErr(GinkgoWriter)
		flag := cmd.Flags().Lookup("node-set")
		Expect(flag).NotTo(BeNil())
		Expect(flag.Usage).To(ContainSubstring("Node set configuration"))
	})
})

var _ = Describe("Create cluster SSH key flags", func() {
	runCreate := func(sourceArgs ...string) *publicv1.Cluster {
		logger := slog.New(slog.NewTextHandler(GinkgoWriter, nil))
		console, err := terminal.NewConsole().
			SetLogger(logger).
			SetStdout(GinkgoWriter).
			SetStderr(GinkgoWriter).
			Build()
		Expect(err).NotTo(HaveOccurred())

		settings, err := config.NewSettings().
			SetLogger(logger).
			SetDir(GinkgoT().TempDir()).
			Build()
		Expect(err).NotTo(HaveOccurred())
		settings.SetTenant("tenant-a")
		settings.SetPlaintext(true)

		server := testutils.NewServer()
		DeferCleanup(server.Stop)
		publicv1.RegisterClusterCatalogItemsServer(server.Registrar(), mockClusterCatalogItemsServer{})
		publicv1.RegisterClusterTemplatesServer(server.Registrar(), mockClusterTemplatesServer{})
		createRequests := make(chan *publicv1.ClustersCreateRequest, 1)
		publicv1.RegisterClustersServer(server.Registrar(), &testutils.ClustersServerFuncs{
			CreateFunc: func(_ context.Context, request *publicv1.ClustersCreateRequest) (*publicv1.ClustersCreateResponse, error) {
				createRequests <- request
				return publicv1.ClustersCreateResponse_builder{
					Object: publicv1.Cluster_builder{Id: "created-cluster"}.Build(),
				}.Build(), nil
			},
		})
		server.Start()
		settings.SetAddress(server.Address())

		ctx := logging.LoggerIntoContext(context.Background(), logger)
		ctx = terminal.ConsoleIntoContext(ctx, console)
		ctx = config.SettingsIntoContext(ctx, settings)
		ctx = config.TenantIntoContext(ctx, settings.Tenant())

		cmd := Cmd()
		cmd.SetArgs(append(sourceArgs,
			"--name", "test-cluster",
			"--ssh-key", "registered-key",
		))
		cmd.SetOut(GinkgoWriter)
		cmd.SetErr(GinkgoWriter)
		Expect(cmd.ExecuteContext(ctx)).To(Succeed())
		return (<-createRequests).GetObject()
	}

	It("sends the Secret reference through the catalog create path", func() {
		cluster := runCreate("--catalog-item", "catalog-item-id")
		Expect(cluster.GetSpec().GetCatalogItem().GetId()).To(Equal("catalog-item-id"))
		Expect(cluster.GetSpec().GetSshKey().GetName()).To(Equal("registered-key"))
	})

	It("sends the Secret reference through the template create path", func() {
		cluster := runCreate("--template", "template-id")
		Expect(cluster.GetSpec().GetTemplate().GetId()).To(Equal("template-id"))
		Expect(cluster.GetSpec().GetSshKey().GetName()).To(Equal("registered-key"))
	})

	It("adds a Secret reference while retaining an explicitly supplied inline value", func() {
		runner := &runnerContext{}
		runner.args.sshKey = "registered-key"
		builder := publicv1.ClusterSpec_builder{}

		Expect(runner.applyOptionalSpecFields(&builder, "inline-key")).To(Succeed())
		spec := builder.Build()
		Expect(spec.GetSshKey().GetName()).To(Equal("registered-key"))
		Expect(spec.GetSshPublicKey()).To(Equal("inline-key"))
	})

	It("does not read an inline key file when a Secret reference takes precedence", func() {
		runner := &runnerContext{}
		runner.args.sshKey = "registered-key"
		runner.args.sshPublicKeyFile = "/does/not/exist"

		key, err := runner.resolveSSHPublicKey()
		Expect(err).ToNot(HaveOccurred())
		Expect(key).To(BeEmpty())
	})

	It("still reports inline key file errors without a Secret reference", func() {
		runner := &runnerContext{}
		runner.args.sshPublicKeyFile = "/does/not/exist"

		_, err := runner.resolveSSHPublicKey()
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("failed to read SSH public key file"))
	})
})

var _ = Describe("Parse cluster node set flag", func() {
	It("should parse comma-separated key=value assignments", func() {
		name, ns, err := parseClusterNodeSetFlag("name=workers,size=4,baremetal-instance-type=ci-worker-bm")
		Expect(err).NotTo(HaveOccurred())
		Expect(name).To(Equal("workers"))
		Expect(ns.GetSize()).To(Equal(int32(4)))
		Expect(ns.GetBaremetalInstanceType().GetName()).To(Equal("ci-worker-bm"))
	})

	It("should require an explicit node set name", func() {
		_, _, err := parseClusterNodeSetFlag("size=2,baremetal-instance-type=ci-worker-bm")
		Expect(err).To(MatchError("--node-set name is required"))
	})

	It("should reject the underscore spelling of the bare metal instance type key", func() {
		_, _, err := parseClusterNodeSetFlag("name=workers,baremetal_instance_type=ci-worker-bm")
		Expect(err).To(HaveOccurred())
	})

	It("should reject a misspelled node set key", func() {
		_, _, err := parseClusterNodeSetFlag("name=workers,szie=4")
		Expect(err).To(HaveOccurred())
	})

	It("should reject a node set assignment without a value", func() {
		_, _, err := parseClusterNodeSetFlag("name=workers,size=")
		Expect(err).To(HaveOccurred())
	})

	It("should reject a non-integer node set size", func() {
		_, _, err := parseClusterNodeSetFlag("name=workers,size=two")
		Expect(err).To(HaveOccurred())
	})

	It("should reject a node set size outside the 32-bit range", func() {
		_, _, err := parseClusterNodeSetFlag("name=workers,size=2147483648")
		Expect(err).To(HaveOccurred())
	})
})

var _ = Describe("Create cluster flag validation", func() {
	It("should return error when both --catalog-item and --template are set", func() {
		cmd := Cmd()
		cmd.SetOut(GinkgoWriter)
		cmd.SetErr(GinkgoWriter)
		cmd.SetArgs([]string{"--catalog-item", "cat-001", "--template", "tpl-001", "--name", "test"})
		err := cmd.Execute()
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("if any flags in the group"))
		Expect(err.Error()).To(ContainSubstring("catalog-item"))
		Expect(err.Error()).To(ContainSubstring("template"))
	})

	It("should return error when neither --catalog-item nor --template is set", func() {
		cmd := Cmd()
		cmd.SetOut(GinkgoWriter)
		cmd.SetErr(GinkgoWriter)
		cmd.SetArgs([]string{"--name", "test"})
		err := cmd.Execute()
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("at least one of the flags"))
		Expect(err.Error()).To(ContainSubstring("catalog-item"))
		Expect(err.Error()).To(ContainSubstring("template"))
	})
})

var _ = Describe("findVersion", func() {
	var (
		ctx     context.Context
		console *terminal.Console
		logger  *slog.Logger
	)

	BeforeEach(func() {
		ctx = context.Background()
		logger = slog.New(slog.NewTextHandler(GinkgoWriter, nil))
		var err error
		console, err = terminal.NewConsole().
			SetLogger(logger).
			SetStdout(GinkgoWriter).
			SetStderr(GinkgoWriter).
			Build()
		Expect(err).ToNot(HaveOccurred())
		err = console.AddTemplates(templatesFS, "templates")
		Expect(err).ToNot(HaveOccurred())
	})

	makeVersion := func(name, version string) *publicv1.ClusterVersion {
		return publicv1.ClusterVersion_builder{
			Metadata: publicv1.Metadata_builder{
				Name: name,
			}.Build(),
			Spec: publicv1.ClusterVersionSpec_builder{
				Version: version,
			}.Build(),
		}.Build()
	}

	It("returns the version when exactly one matches by metadata.name", func() {
		mock := &mockClusterVersionsClient{
			listFunc: func(_ context.Context, _ *publicv1.ClusterVersionsListRequest, _ ...grpc.CallOption) (*publicv1.ClusterVersionsListResponse, error) {
				return publicv1.ClusterVersionsListResponse_builder{
					Items: []*publicv1.ClusterVersion{makeVersion("4-17-0", "4.17.0")},
					Size:  1,
					Total: 1,
				}.Build(), nil
			},
		}
		runner := &runnerContext{
			console:               console,
			logger:                logger,
			clusterVersionsClient: mock,
		}
		runner.args.version = "4-17-0"
		result, err := runner.findVersion(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(result.GetMetadata().GetName()).To(Equal("4-17-0"))
	})

	It("returns the version when exactly one matches by spec.version", func() {
		mock := &mockClusterVersionsClient{
			listFunc: func(_ context.Context, _ *publicv1.ClusterVersionsListRequest, _ ...grpc.CallOption) (*publicv1.ClusterVersionsListResponse, error) {
				return publicv1.ClusterVersionsListResponse_builder{
					Items: []*publicv1.ClusterVersion{makeVersion("4-17-0-a1b2", "4.17.0")},
					Size:  1,
					Total: 1,
				}.Build(), nil
			},
		}
		runner := &runnerContext{
			console:               console,
			logger:                logger,
			clusterVersionsClient: mock,
		}
		runner.args.version = "4.17.0"
		result, err := runner.findVersion(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(result.GetMetadata().GetName()).To(Equal("4-17-0-a1b2"))
	})

	It("prefers metadata.name match when two versions match", func() {
		nameMatch := makeVersion("4-17-0", "4.17.0")
		versionMatch := makeVersion("other-name", "4-17-0")
		mock := &mockClusterVersionsClient{
			listFunc: func(_ context.Context, _ *publicv1.ClusterVersionsListRequest, _ ...grpc.CallOption) (*publicv1.ClusterVersionsListResponse, error) {
				return publicv1.ClusterVersionsListResponse_builder{
					Items: []*publicv1.ClusterVersion{versionMatch, nameMatch},
					Size:  2,
					Total: 2,
				}.Build(), nil
			},
		}
		runner := &runnerContext{
			console:               console,
			logger:                logger,
			clusterVersionsClient: mock,
		}
		runner.args.version = "4-17-0"
		result, err := runner.findVersion(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(result.GetMetadata().GetName()).To(Equal("4-17-0"))
	})

	It("returns exit error when no versions match", func() {
		callCount := 0
		mock := &mockClusterVersionsClient{
			listFunc: func(_ context.Context, _ *publicv1.ClusterVersionsListRequest, _ ...grpc.CallOption) (*publicv1.ClusterVersionsListResponse, error) {
				callCount++
				if callCount == 1 {
					// Filtered query: no matches
					return publicv1.ClusterVersionsListResponse_builder{}.Build(), nil
				}
				// Examples query:
				return publicv1.ClusterVersionsListResponse_builder{
					Items: []*publicv1.ClusterVersion{makeVersion("4-17-0", "4.17.0")},
					Size:  1,
					Total: 1,
				}.Build(), nil
			},
		}
		runner := &runnerContext{
			console:               console,
			logger:                logger,
			clusterVersionsClient: mock,
		}
		runner.args.version = "nonexistent"
		result, err := runner.findVersion(ctx)
		Expect(result).To(BeNil())
		Expect(err).To(HaveOccurred())
		var exitErr exit.Error
		Expect(errors.As(err, &exitErr)).To(BeTrue())
	})

	It("propagates gRPC errors from the list call", func() {
		mock := &mockClusterVersionsClient{
			listFunc: func(_ context.Context, _ *publicv1.ClusterVersionsListRequest, _ ...grpc.CallOption) (*publicv1.ClusterVersionsListResponse, error) {
				return nil, errors.New("connection refused")
			},
		}
		runner := &runnerContext{
			console:               console,
			logger:                logger,
			clusterVersionsClient: mock,
		}
		runner.args.version = "4.17.0"
		result, err := runner.findVersion(ctx)
		Expect(result).To(BeNil())
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("connection refused"))
		var exitErr exit.Error
		Expect(errors.As(err, &exitErr)).To(BeFalse())
	})
})

var _ = Describe("Create cluster pull secret flags", func() {
	It("should register --pull-secret flag", func() {
		cmd := Cmd()
		cmd.SetOut(GinkgoWriter)
		cmd.SetErr(GinkgoWriter)
		flag := cmd.Flags().Lookup("pull-secret")
		Expect(flag).NotTo(BeNil())
		Expect(flag.DefValue).To(Equal(""))
	})

})

var _ = Describe("Create cluster networking flags", func() {
	It("should register --network-attachment flag", func() {
		cmd := Cmd()
		flag := cmd.Flags().Lookup("network-attachment")
		Expect(flag).NotTo(BeNil())
		Expect(flag.DefValue).To(Equal(""))
	})

	It("should register --external-ip-attachment flag", func() {
		cmd := Cmd()
		flag := cmd.Flags().Lookup("external-ip-attachment")
		Expect(flag).NotTo(BeNil())
		Expect(flag.Value.Type()).To(Equal("bool"))
		Expect(flag.DefValue).To(Equal("false"))
	})
})

var _ = Describe("parseClusterNetworkAttachmentFlag", func() {
	It("returns error for empty string", func() {
		_, err := parseClusterNetworkAttachmentFlag("")
		Expect(err).To(HaveOccurred())
	})

	It("parses a bare subnet name", func() {
		na, err := parseClusterNetworkAttachmentFlag("my-subnet")
		Expect(err).NotTo(HaveOccurred())
		Expect(na.GetSubnet().GetName()).To(Equal("my-subnet"))
		Expect(na.GetSecurityGroups()).To(BeEmpty())
	})

	It("parses subnet=<name> explicit key", func() {
		na, err := parseClusterNetworkAttachmentFlag("subnet=my-subnet")
		Expect(err).NotTo(HaveOccurred())
		Expect(na.GetSubnet().GetName()).To(Equal("my-subnet"))
		Expect(na.GetSecurityGroups()).To(BeEmpty())
	})

	It("parses subnet with one security group", func() {
		na, err := parseClusterNetworkAttachmentFlag("subnet=my-subnet,security-groups=sg1")
		Expect(err).NotTo(HaveOccurred())
		Expect(na.GetSubnet().GetName()).To(Equal("my-subnet"))
		Expect(na.GetSecurityGroups()).To(HaveLen(1))
		Expect(na.GetSecurityGroups()[0].GetName()).To(Equal("sg1"))
	})

	It("parses subnet with multiple security groups", func() {
		na, err := parseClusterNetworkAttachmentFlag("subnet=my-subnet,security-groups=sg1,sg2")
		Expect(err).NotTo(HaveOccurred())
		Expect(na.GetSubnet().GetName()).To(Equal("my-subnet"))
		Expect(na.GetSecurityGroups()).To(HaveLen(2))
		Expect(na.GetSecurityGroups()[0].GetName()).To(Equal("sg1"))
		Expect(na.GetSecurityGroups()[1].GetName()).To(Equal("sg2"))
	})

	It("returns error when security-groups present but subnet missing", func() {
		_, err := parseClusterNetworkAttachmentFlag(",security-groups=sg1")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("subnet"))
	})

	It("returns error when subnet= key is present but value is empty", func() {
		_, err := parseClusterNetworkAttachmentFlag("subnet=,security-groups=sg1")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("empty"))
	})

	It("returns error for unknown key=value fragment", func() {
		_, err := parseClusterNetworkAttachmentFlag("foo=bar")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("unknown key"))
	})

	It("uses Name (not Id) on SubnetLocalReference", func() {
		na, err := parseClusterNetworkAttachmentFlag("subnet=named-subnet")
		Expect(err).NotTo(HaveOccurred())
		Expect(na.GetSubnet().GetName()).To(Equal("named-subnet"))
		Expect(na.GetSubnet().GetId()).To(BeEmpty())
	})

	It("uses Name (not Id) on SecurityGroupLocalReference", func() {
		na, err := parseClusterNetworkAttachmentFlag("subnet=s,security-groups=named-sg")
		Expect(err).NotTo(HaveOccurred())
		Expect(na.GetSecurityGroups()[0].GetName()).To(Equal("named-sg"))
		Expect(na.GetSecurityGroups()[0].GetId()).To(BeEmpty())
	})
})

var _ = Describe("Create cluster project propagation", func() {
	It("sets metadata.project from the context when a project is selected", func() {
		logger := slog.New(slog.NewTextHandler(GinkgoWriter, nil))
		console, err := terminal.NewConsole().
			SetLogger(logger).
			SetStdout(GinkgoWriter).
			SetStderr(GinkgoWriter).
			Build()
		Expect(err).NotTo(HaveOccurred())

		settings, err := config.NewSettings().
			SetLogger(logger).
			SetDir(GinkgoT().TempDir()).
			Build()
		Expect(err).NotTo(HaveOccurred())
		settings.SetTenant("tenant-a")
		settings.SetPlaintext(true)

		server := testutils.NewServer()
		DeferCleanup(server.Stop)
		publicv1.RegisterClusterCatalogItemsServer(server.Registrar(), mockClusterCatalogItemsServer{})
		createRequests := make(chan *publicv1.ClustersCreateRequest, 1)
		publicv1.RegisterClustersServer(server.Registrar(), &testutils.ClustersServerFuncs{
			CreateFunc: func(_ context.Context, request *publicv1.ClustersCreateRequest) (*publicv1.ClustersCreateResponse, error) {
				createRequests <- request
				return publicv1.ClustersCreateResponse_builder{
					Object: publicv1.Cluster_builder{Id: "created-cluster"}.Build(),
				}.Build(), nil
			},
		})
		server.Start()
		settings.SetAddress(server.Address())

		ctx := logging.LoggerIntoContext(context.Background(), logger)
		ctx = terminal.ConsoleIntoContext(ctx, console)
		ctx = config.SettingsIntoContext(ctx, settings)
		ctx = config.TenantIntoContext(ctx, settings.Tenant())
		ctx = config.ProjectIntoContext(ctx, "team-a.frontend")

		cmd := Cmd()
		cmd.SetArgs([]string{
			"--catalog-item", "catalog-item-id",
			"--name", "test-cluster",
		})
		cmd.SetOut(GinkgoWriter)
		cmd.SetErr(GinkgoWriter)
		Expect(cmd.ExecuteContext(ctx)).To(Succeed())

		request := <-createRequests
		cluster := request.GetObject()
		Expect(cluster.GetMetadata().GetProject()).To(Equal("team-a.frontend"))
		Expect(cluster.GetMetadata().GetTenant()).To(Equal("tenant-a"))
		Expect(cluster.GetMetadata().GetName()).To(Equal("test-cluster"))
	})
})

var _ = Describe("Create cluster node sets", func() {
	It("creates a cluster with multiple explicitly named node sets", func() {
		logger := slog.New(slog.NewTextHandler(GinkgoWriter, nil))
		console, err := terminal.NewConsole().
			SetLogger(logger).
			SetStdout(GinkgoWriter).
			SetStderr(GinkgoWriter).
			Build()
		Expect(err).NotTo(HaveOccurred())

		settings, err := config.NewSettings().
			SetLogger(logger).
			SetDir(GinkgoT().TempDir()).
			Build()
		Expect(err).NotTo(HaveOccurred())
		settings.SetTenant("tenant-a")
		settings.SetPlaintext(true)

		server := testutils.NewServer()
		DeferCleanup(server.Stop)
		publicv1.RegisterClusterCatalogItemsServer(server.Registrar(), mockClusterCatalogItemsServer{})
		createRequests := make(chan *publicv1.ClustersCreateRequest, 1)
		publicv1.RegisterClustersServer(server.Registrar(), &testutils.ClustersServerFuncs{
			CreateFunc: func(_ context.Context, request *publicv1.ClustersCreateRequest) (*publicv1.ClustersCreateResponse, error) {
				createRequests <- request
				return publicv1.ClustersCreateResponse_builder{
					Object: publicv1.Cluster_builder{Id: "created-cluster"}.Build(),
				}.Build(), nil
			},
		})
		server.Start()
		settings.SetAddress(server.Address())

		ctx := logging.LoggerIntoContext(context.Background(), logger)
		ctx = terminal.ConsoleIntoContext(ctx, console)
		ctx = config.SettingsIntoContext(ctx, settings)
		ctx = config.TenantIntoContext(ctx, settings.Tenant())

		cmd := Cmd()
		cmd.SetArgs([]string{
			"--catalog-item", "catalog-item-id",
			"--name", "test-cluster",
			"--node-set", "name=compute,size=2,baremetal-instance-type=compute-bm",
			"--node-set", "name=accelerator,size=1,baremetal-instance-type=accelerator-bm",
		})
		cmd.SetOut(GinkgoWriter)
		cmd.SetErr(GinkgoWriter)
		Expect(cmd.ExecuteContext(ctx)).To(Succeed())

		request := <-createRequests
		cluster := request.GetObject()
		Expect(cluster.GetMetadata().GetName()).To(Equal("test-cluster"))
		Expect(cluster.GetMetadata().GetTenant()).To(Equal("tenant-a"))
		Expect(cluster.GetSpec().GetCatalogItem().GetId()).To(Equal("catalog-item-id"))

		nodeSets := cluster.GetSpec().GetNodeSets()
		Expect(nodeSets).To(HaveLen(2))
		Expect(nodeSets).To(HaveKey("compute"))
		Expect(nodeSets).To(HaveKey("accelerator"))
		Expect(nodeSets["compute"].GetSize()).To(Equal(int32(2)))
		Expect(nodeSets["compute"].GetBaremetalInstanceType().GetName()).To(Equal("compute-bm"))
		Expect(nodeSets["accelerator"].GetSize()).To(Equal(int32(1)))
		Expect(nodeSets["accelerator"].GetBaremetalInstanceType().GetName()).To(Equal("accelerator-bm"))
	})
})
