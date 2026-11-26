package volumes_test

import (
	"context"
	"io"
	"log/slog"
	"net"
	"testing"

	"google.golang.org/grpc"

	"github.com/osac-project/osac/fulfillment-service/internal/cmd/cli"
	"github.com/osac-project/osac/fulfillment-service/internal/config"
	"github.com/osac-project/osac/fulfillment-service/internal/logging"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

type volumesServer struct {
	publicv1.UnimplementedVolumesServer
	filter        string
	createdTenant string
}

func (s *volumesServer) List(
	_ context.Context, request *publicv1.VolumesListRequest,
) (*publicv1.VolumesListResponse, error) {
	s.filter = request.GetFilter()
	return publicv1.VolumesListResponse_builder{Size: 0, Total: 0}.Build(), nil
}

func (s *volumesServer) Create(
	_ context.Context, request *publicv1.VolumesCreateRequest,
) (*publicv1.VolumesCreateResponse, error) {
	s.createdTenant = request.GetObject().GetMetadata().GetTenant()
	return publicv1.VolumesCreateResponse_builder{
		Object: publicv1.Volume_builder{Id: "volume-id"}.Build(),
	}.Build(), nil
}

func TestVolumesListUsesEffectiveTenant(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	for _, tc := range []struct {
		name        string
		args        []string
		savedTenant string
		expected    string
	}{
		{
			name:        "inline flag scopes list to selected tenant",
			args:        []string{"--tenant", "tenant-command-line"},
			savedTenant: "tenant-settings",
			expected:    `this.metadata.tenant == "tenant-command-line"`,
		},
		{
			name:        "omitted flag uses saved tenant",
			args:        nil,
			savedTenant: "tenant-settings",
			expected:    `this.metadata.tenant == "tenant-settings"`,
		},
		{
			name:        "no tenant leaves the list unscoped",
			args:        nil,
			savedTenant: "",
			expected:    "",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			server := grpc.NewServer()
			volumes := &volumesServer{}
			publicv1.RegisterVolumesServer(server, volumes)
			go func() { _ = server.Serve(listener) }()
			defer server.Stop()

			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			configDir := t.TempDir()
			settings, err := config.NewSettings().SetLogger(logger).SetDir(configDir).Build()
			if err != nil {
				t.Fatal(err)
			}
			settings.SetAddress(listener.Addr().String())
			settings.SetPlaintext(true)
			settings.SetTenant(tc.savedTenant)
			ctx := logging.LoggerIntoContext(context.Background(), logger)
			if err := settings.Save(ctx); err != nil {
				t.Fatal(err)
			}

			root, err := cli.Root()
			if err != nil {
				t.Fatal(err)
			}
			root.SetOut(io.Discard)
			root.SetErr(io.Discard)
			args := append([]string{"--config", configDir, "--cache", t.TempDir()}, tc.args...)
			args = append(args, "volumes", "list")
			root.SetArgs(args)
			if err := root.ExecuteContext(ctx); err != nil {
				t.Fatal(err)
			}
			if volumes.filter != tc.expected {
				t.Fatalf("volumes list filter = %q, expected %q", volumes.filter, tc.expected)
			}
		})
	}
}

func TestVolumesCreateUsesEffectiveTenant(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	volumes := &volumesServer{}
	publicv1.RegisterVolumesServer(server, volumes)
	go func() { _ = server.Serve(listener) }()
	defer server.Stop()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	configDir := t.TempDir()
	settings, err := config.NewSettings().SetLogger(logger).SetDir(configDir).Build()
	if err != nil {
		t.Fatal(err)
	}
	settings.SetAddress(listener.Addr().String())
	settings.SetPlaintext(true)
	settings.SetTenant("tenant-settings")
	ctx := logging.LoggerIntoContext(context.Background(), logger)
	if err := settings.Save(ctx); err != nil {
		t.Fatal(err)
	}

	root, err := cli.Root()
	if err != nil {
		t.Fatal(err)
	}
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	root.SetArgs([]string{
		"--config", configDir,
		"--cache", t.TempDir(),
		"--tenant", "tenant-command-line",
		"volumes", "create",
		"--name", "tenant-volume",
		"--storage-tier", "gold",
		"--size-gib", "1",
	})
	if err := root.ExecuteContext(ctx); err != nil {
		t.Fatal(err)
	}
	if volumes.createdTenant != "tenant-command-line" {
		t.Fatalf("volume create tenant = %q, expected %q", volumes.createdTenant, "tenant-command-line")
	}
}

func TestVolumesListCombinesTenantAndProject(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	volumes := &volumesServer{}
	publicv1.RegisterVolumesServer(server, volumes)
	go func() { _ = server.Serve(listener) }()
	defer server.Stop()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	configDir := t.TempDir()
	settings, err := config.NewSettings().SetLogger(logger).SetDir(configDir).Build()
	if err != nil {
		t.Fatal(err)
	}
	settings.SetAddress(listener.Addr().String())
	settings.SetPlaintext(true)
	settings.SetTenant("tenant-settings")
	ctx := logging.LoggerIntoContext(context.Background(), logger)
	if err := settings.Save(ctx); err != nil {
		t.Fatal(err)
	}

	root, err := cli.Root()
	if err != nil {
		t.Fatal(err)
	}
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	root.SetArgs([]string{
		"--config", configDir,
		"--cache", t.TempDir(),
		"--tenant", "tenant-command-line",
		"volumes", "list",
		"--project", "team-a",
	})
	if err := root.ExecuteContext(ctx); err != nil {
		t.Fatal(err)
	}
	expected := `this.metadata.tenant == "tenant-command-line" && this.metadata.project == "team-a"`
	if volumes.filter != expected {
		t.Fatalf("volumes list filter = %q, expected %q", volumes.filter, expected)
	}
}
