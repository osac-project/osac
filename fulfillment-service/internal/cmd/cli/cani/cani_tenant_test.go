package cani_test

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

type reviewServer struct {
	publicv1.UnimplementedSelfSubjectAccessReviewsServer
	tenant string
}

func (s *reviewServer) Create(
	_ context.Context, request *publicv1.SelfSubjectAccessReviewsCreateRequest,
) (*publicv1.SelfSubjectAccessReviewsCreateResponse, error) {
	s.tenant = request.GetObject().GetMetadata().GetTenant()
	return publicv1.SelfSubjectAccessReviewsCreateResponse_builder{
		Object: publicv1.SelfSubjectAccessReview_builder{
			Status: publicv1.SelfSubjectAccessReviewStatus_builder{Allowed: true}.Build(),
		}.Build(),
	}.Build(), nil
}

func TestCanIUsesEffectiveTenant(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	for _, tc := range []struct {
		name        string
		args        []string
		savedTenant string
		expected    string
	}{
		{name: "inline flag overrides saved tenant", args: []string{"--tenant", "tenant-command-line"}, savedTenant: "tenant-settings", expected: "tenant-command-line"},
		{name: "omitted flag uses saved tenant", args: nil, savedTenant: "tenant-settings", expected: "tenant-settings"},
		{name: "no tenant leaves the review unscoped", args: nil, savedTenant: "", expected: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			server := grpc.NewServer()
			reviews := &reviewServer{}
			publicv1.RegisterSelfSubjectAccessReviewsServer(server, reviews)
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
			args = append(args, "can-i", "list", "clusters")
			root.SetArgs(args)
			if err := root.ExecuteContext(ctx); err != nil {
				t.Fatal(err)
			}
			if reviews.tenant != tc.expected {
				t.Fatalf("can-i review tenant = %q, expected %q", reviews.tenant, tc.expected)
			}
		})
	}
}
