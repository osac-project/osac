package driver

import (
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestVendorRoute(t *testing.T) {
	perProtocol := map[string]string{
		"vast/nfs":   "/sockets/vast-nfs.sock",
		"vast/block": "/sockets/vast-block.sock",
	}
	bareVast := map[string]string{"vast": "/sockets/vast.sock"}
	bareVendors := map[string]string{
		"netapp": "/sockets/netapp.sock",
		"pure":   "/sockets/pure.sock",
	}

	tests := []struct {
		name     string
		m        map[string]string
		backend  string
		protocol string
		want     string
		wantCode codes.Code
		wantMsg  []string
	}{
		{name: "vast nfs resolves vast/nfs", m: perProtocol,
			backend: "vast", protocol: "nfs", want: "/sockets/vast-nfs.sock"},
		{name: "protocol is case-insensitive", m: perProtocol,
			backend: "vast", protocol: "NFS", want: "/sockets/vast-nfs.sock"},
		{name: "vast block resolves vast/block", m: perProtocol,
			backend: "vast", protocol: "block", want: "/sockets/vast-block.sock"},
		{name: "vast block falls back to bare vast", m: bareVast,
			backend: "vast", protocol: "block", want: "/sockets/vast.sock"},
		{name: "vast nfs falls back to bare vast", m: bareVast,
			backend: "vast", protocol: "nfs", want: "/sockets/vast.sock"},
		{name: "empty protocol resolves bare backend", m: bareVast,
			backend: "vast", protocol: "", want: "/sockets/vast.sock"},
		{name: "netapp nfs resolves bare netapp", m: bareVendors,
			backend: "netapp", protocol: "nfs", want: "/sockets/netapp.sock"},
		{name: "netapp block resolves bare netapp", m: bareVendors,
			backend: "netapp", protocol: "block", want: "/sockets/netapp.sock"},
		{name: "netapp without protocol resolves bare netapp", m: bareVendors,
			backend: "netapp", protocol: "", want: "/sockets/netapp.sock"},
		{name: "pure nfs resolves bare pure", m: bareVendors,
			backend: "pure", protocol: "nfs", want: "/sockets/pure.sock"},
		{name: "pure block resolves bare pure", m: bareVendors,
			backend: "pure", protocol: "block", want: "/sockets/pure.sock"},
		{name: "pure without protocol resolves bare pure", m: bareVendors,
			backend: "pure", protocol: "", want: "/sockets/pure.sock"},
		{name: "empty backend is invalid", m: perProtocol,
			backend: "", protocol: "nfs", wantCode: codes.InvalidArgument},
		{name: "empty backend with nil map is invalid", m: nil,
			backend: "", protocol: "", wantCode: codes.InvalidArgument},
		{name: "miss names both keys", m: map[string]string{"netapp": "/sockets/netapp.sock"},
			backend: "vast", protocol: "nfs", wantCode: codes.NotFound,
			wantMsg: []string{`"vast/nfs"`, `"vast"`}},
		{name: "unknown backend is not found", m: bareVendors,
			backend: "unknown", protocol: "", wantCode: codes.NotFound},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := vendorRoute(tt.m, tt.backend, tt.protocol)
			if tt.wantCode != codes.OK {
				assertGRPCCode(t, err, tt.wantCode)
				for _, part := range tt.wantMsg {
					if !strings.Contains(status.Convert(err).Message(), part) {
						t.Errorf("error %q does not mention %s", status.Convert(err).Message(), part)
					}
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("vendorRoute(%q, %q) = %q, want %q", tt.backend, tt.protocol, got, tt.want)
			}
		})
	}
}
