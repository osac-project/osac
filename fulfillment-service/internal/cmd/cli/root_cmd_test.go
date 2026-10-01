package cli

import (
	"context"
	"io"
	"log/slog"
	"net"
	"path/filepath"
	"testing"

	"google.golang.org/grpc"

	"github.com/osac-project/osac/fulfillment-service/internal/config"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

type rootCatalogItemsServer struct {
	publicv1.UnimplementedBareMetalInstanceCatalogItemsServer
}

func (rootCatalogItemsServer) List(context.Context, *publicv1.BareMetalInstanceCatalogItemsListRequest) (*publicv1.BareMetalInstanceCatalogItemsListResponse, error) {
	return &publicv1.BareMetalInstanceCatalogItemsListResponse{
		Items: []*publicv1.BareMetalInstanceCatalogItem{{Id: "catalog-123"}},
	}, nil
}

type rootBareMetalInstancesServer struct {
	publicv1.UnimplementedBareMetalInstancesServer
	createdTenants chan<- string
}

func (server rootBareMetalInstancesServer) Create(_ context.Context, request *publicv1.BareMetalInstancesCreateRequest) (*publicv1.BareMetalInstancesCreateResponse, error) {
	server.createdTenants <- request.GetObject().GetMetadata().GetTenant()
	return &publicv1.BareMetalInstancesCreateResponse{
		Object: &publicv1.BareMetalInstance{Id: "bmi-123"},
	}, nil
}

func TestRootBareMetalInstanceTenant(t *testing.T) {
	for _, testCase := range []struct {
		name        string
		savedTenant string
		flag        string
		wantTenant  string
	}{
		{name: "saved tenant", savedTenant: "saved", wantTenant: "saved"},
		{name: "flag overrides saved tenant", savedTenant: "saved", flag: "--tenant=selected", wantTenant: "selected"},
		{name: "flag selects tenant without saved setting", flag: "--tenant=selected", wantTenant: "selected"},
		{name: "empty flag clears saved tenant", savedTenant: "saved", flag: "--tenant=", wantTenant: ""},
		{name: "no tenant selected", wantTenant: ""},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			createdTenants := make(chan string, 1)
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			server := grpc.NewServer()
			t.Cleanup(server.Stop)
			publicv1.RegisterBareMetalInstanceCatalogItemsServer(server, rootCatalogItemsServer{})
			publicv1.RegisterBareMetalInstancesServer(server, rootBareMetalInstancesServer{createdTenants: createdTenants})
			go func() {
				if err := server.Serve(listener); err != nil {
					t.Errorf("serve: %v", err)
				}
			}()

			configDir := filepath.Join(t.TempDir(), "config")
			settings, err := config.NewSettings().SetLogger(slog.New(slog.NewTextHandler(io.Discard, nil))).SetDir(configDir).Build()
			if err != nil {
				t.Fatal(err)
			}
			settings.SetAddress(listener.Addr().String())
			settings.SetPlaintext(true)
			settings.SetTenant(testCase.savedTenant)
			if err := settings.Save(context.Background()); err != nil {
				t.Fatal(err)
			}

			command, err := Root()
			if err != nil {
				t.Fatal(err)
			}
			args := []string{"--config", configDir, "--cache", t.TempDir()}
			if testCase.flag != "" {
				args = append(args, testCase.flag)
			}
			args = append(args, "create", "baremetalinstance", "--catalog-item", "catalog-123", "--name", "example")
			command.SetArgs(args)
			if err := command.Execute(); err != nil {
				t.Fatal(err)
			}
			if tenant := <-createdTenants; tenant != testCase.wantTenant {
				t.Fatalf("create tenant = %q, want %q", tenant, testCase.wantTenant)
			}
		})
	}
}
