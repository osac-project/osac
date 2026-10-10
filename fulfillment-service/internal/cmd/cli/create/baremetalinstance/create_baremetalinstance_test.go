package baremetalinstance_test

import (
	"context"
	"io"
	"log/slog"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/osac-project/osac/fulfillment-service/internal/cmd/cli"
	"github.com/osac-project/osac/fulfillment-service/internal/config"
	"github.com/osac-project/osac/fulfillment-service/internal/logging"
	srvtesting "github.com/osac-project/osac/fulfillment-service/internal/testing"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

type catalogItemsServer struct {
	publicv1.UnimplementedBareMetalInstanceCatalogItemsServer
}

func (catalogItemsServer) List(context.Context, *publicv1.BareMetalInstanceCatalogItemsListRequest) (*publicv1.BareMetalInstanceCatalogItemsListResponse, error) {
	return &publicv1.BareMetalInstanceCatalogItemsListResponse{
		Items: []*publicv1.BareMetalInstanceCatalogItem{{Id: "catalog-123"}},
	}, nil
}

type bareMetalInstancesServer struct {
	publicv1.UnimplementedBareMetalInstancesServer
	latestRequest *publicv1.BareMetalInstancesCreateRequest
}

func (s *bareMetalInstancesServer) Create(_ context.Context, request *publicv1.BareMetalInstancesCreateRequest) (*publicv1.BareMetalInstancesCreateResponse, error) {
	s.latestRequest = request

	return &publicv1.BareMetalInstancesCreateResponse{
		Object: &publicv1.BareMetalInstance{Id: "bmi-123"},
	}, nil
}

var _ = Describe("Creating a baremetalinstance with tenant set", func() {
	DescribeTable("should give priority to the command line parameter",
		func(tenantInSetting, tenantInCommandLine, expectedTenant string) {
			server := srvtesting.NewServer()
			DeferCleanup(server.Stop)
			publicv1.RegisterBareMetalInstanceCatalogItemsServer(server.Registrar(), catalogItemsServer{})
			instancesServer := &bareMetalInstancesServer{}
			publicv1.RegisterBareMetalInstancesServer(server.Registrar(), instancesServer)
			server.Start()

			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			configDir := GinkgoT().TempDir()
			settings, err := config.NewSettings().SetLogger(logger).SetDir(configDir).Build()
			Expect(err).NotTo(HaveOccurred())

			settings.SetAddress(server.Address())
			settings.SetPlaintext(true)

			if tenantInSetting != "" {
				settings.SetTenant(tenantInSetting)
			}

			ctx := logging.LoggerIntoContext(context.Background(), logger)
			Expect(settings.Save(ctx)).To(Succeed())

			rootCmd, err := cli.Root()
			Expect(err).NotTo(HaveOccurred())

			args := []string{
				"--config", configDir,
				"--cache", GinkgoT().TempDir(),
			}
			if tenantInCommandLine != "" {
				args = append(args, "--tenant", tenantInCommandLine)
			}
			args = append(args, "create", "baremetalinstance", "--catalog-item", "catalog-123", "--name", "example")
			rootCmd.SetArgs(args)

			Expect(rootCmd.ExecuteContext(ctx)).To(Succeed())

			request := instancesServer.latestRequest
			Expect(request).NotTo(BeNil())
			Expect(request.GetObject().GetMetadata().GetTenant()).To(Equal(expectedTenant))
		},
		Entry("with none", "", "", ""),
		Entry("with only tenant in settings", "tenant-settings", "", "tenant-settings"),
		Entry("with only tenant in command line", "", "tenant-command-line", "tenant-command-line"),
		Entry("with both", "tenant-settings", "tenant-command-line", "tenant-command-line"),
	)
})
