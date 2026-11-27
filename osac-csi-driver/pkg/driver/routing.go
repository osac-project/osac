package driver

import (
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// vendorRoute returns the vendor endpoint for a backend and protocol from m,
// a map of vendor sockets or vendor controllers.
//
// Keys are "<backend>/<protocol>" with a lowercase protocol, or a bare
// "<backend>" that serves every protocol. The protocol-specific key wins;
// the bare key is the fallback for any protocol, including an empty one.
//
// Because of that fallback, a provider whose NFS needs no attach but whose
// block does must set "<provider>/nfs=none" explicitly. Otherwise NFS
// requests fall through to the bare "<provider>" endpoint.
//
// An empty backend returns InvalidArgument. A miss on both keys returns
// NotFound naming both.
func vendorRoute(m map[string]string, backend, protocol string) (string, error) {
	if backend == "" {
		return "", status.Error(codes.InvalidArgument,
			"volume context missing required key \"osac.backend\"")
	}

	protocol = strings.ToLower(protocol)
	if protocol != "" {
		key := backend + "/" + protocol
		if endpoint, ok := m[key]; ok {
			return endpoint, nil
		}
		if endpoint, ok := m[backend]; ok {
			return endpoint, nil
		}
		return "", status.Errorf(codes.NotFound,
			"no vendor endpoint configured for %q or %q", key, backend)
	}

	if endpoint, ok := m[backend]; ok {
		return endpoint, nil
	}
	return "", status.Errorf(codes.NotFound,
		"no vendor endpoint configured for %q", backend)
}
