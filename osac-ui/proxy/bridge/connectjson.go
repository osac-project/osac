package bridge

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"time"

	log "github.com/sirupsen/logrus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	reflectpb "google.golang.org/grpc/reflection/grpc_reflection_v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"

	// Registers well-known types into protoregistry.GlobalTypes. vanguard's per-service type
	// resolver (see resolverForFile in connectrpc.com/vanguard's type_resolver.go) is built
	// purely from reflection-discovered file descriptors, which never include the .proto files
	// defining these types — Any is opaque, so no .proto file "imports" google/protobuf/{wrappers,
	// struct}.proto just by declaring a google.protobuf.Any field. vanguard falls back to
	// protoregistry.GlobalTypes specifically to resolve Any-packed well-known types, but that
	// fallback is empty unless something in this binary references the corresponding Go package.
	//
	// ClusterTemplateParameterDefinition.default (google.protobuf.Any) is documented
	// (cluster_template_type.proto) to pack one of: BoolValue, Int32Value, Int64Value,
	// FloatValue, DoubleValue, StringValue, BytesValue (wrapperspb), Timestamp, Duration
	// (already linked transitively via anypb/timestamppb/durationpb), or Value (structpb, for
	// "any JSON value"). Without these imports, a response containing one of the unlinked types
	// fails to marshal with e.g. "unable to resolve type.googleapis.com/google.protobuf.Value:
	// not found" (or .StringValue, .BoolValue, ... — whichever type wasn't yet linked).
	_ "google.golang.org/protobuf/types/known/structpb"
	_ "google.golang.org/protobuf/types/known/wrapperspb"

	"connectrpc.com/vanguard"
)

// NewConnectJSONProxy creates an http.Handler that accepts Connect protocol
// requests (JSON) and forwards them as native gRPC. Proto schemas are
// discovered dynamically via gRPC server reflection — no generated stubs needed.
func NewConnectJSONProxy(grpcURL string, tlsConfig *tls.Config) (http.Handler, error) {
	var creds credentials.TransportCredentials
	if tlsConfig != nil {
		creds = credentials.NewTLS(tlsConfig)
	} else {
		creds = insecure.NewCredentials()
	}

	conn, err := grpc.NewClient(grpcURL, grpc.WithTransportCredentials(creds))
	if err != nil {
		return nil, fmt.Errorf("grpc dial: %w", err)
	}
	defer func() { _ = conn.Close() }()

	files, err := discoverServices(conn)
	if err != nil {
		return nil, fmt.Errorf("grpc reflection: %w", err)
	}

	proxy := newGRPCReverseProxy(grpcURL, tlsConfig)

	var services []*vanguard.Service
	files.RangeFiles(func(fd protoreflect.FileDescriptor) bool {
		sds := fd.Services()
		for i := range sds.Len() {
			sd := sds.Get(i)
			services = append(services, vanguard.NewServiceWithSchema(
				sd,
				proxy,
				vanguard.WithTargetProtocols(vanguard.ProtocolGRPC),
				vanguard.WithTargetCodecs(vanguard.CodecProto),
			))
		}
		return true
	})

	if len(services) == 0 {
		return nil, fmt.Errorf("no services discovered via reflection")
	}

	log.Infof("gRPC reflection discovered %d services", len(services))

	transcoder, err := vanguard.NewTranscoder(services)
	if err != nil {
		return nil, fmt.Errorf("vanguard transcoder: %w", err)
	}

	return transcoder, nil
}

func discoverServices(conn *grpc.ClientConn) (*protoregistry.Files, error) {
	client := reflectpb.NewServerReflectionClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	stream, err := client.ServerReflectionInfo(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = stream.CloseSend() }()

	if err := stream.Send(&reflectpb.ServerReflectionRequest{
		MessageRequest: &reflectpb.ServerReflectionRequest_ListServices{ListServices: ""},
	}); err != nil {
		return nil, err
	}
	listResp, err := stream.Recv()
	if err != nil {
		return nil, err
	}

	seen := make(map[string]bool)
	var allFiles []*descriptorpb.FileDescriptorProto

	for _, svc := range listResp.GetListServicesResponse().GetService() {
		if err := stream.Send(&reflectpb.ServerReflectionRequest{
			MessageRequest: &reflectpb.ServerReflectionRequest_FileContainingSymbol{
				FileContainingSymbol: svc.GetName(),
			},
		}); err != nil {
			return nil, err
		}
		resp, err := stream.Recv()
		if err != nil {
			return nil, err
		}
		for _, fdBytes := range resp.GetFileDescriptorResponse().GetFileDescriptorProto() {
			fdProto := &descriptorpb.FileDescriptorProto{}
			if err := proto.Unmarshal(fdBytes, fdProto); err != nil {
				continue
			}
			name := fdProto.GetName()
			if seen[name] {
				continue
			}
			seen[name] = true
			allFiles = append(allFiles, fdProto)
		}
	}

	resolved, err := protodesc.NewFiles(&descriptorpb.FileDescriptorSet{File: allFiles})
	if err != nil {
		return nil, fmt.Errorf("protodesc.NewFiles: %w", err)
	}

	count := 0
	resolved.RangeFiles(func(_ protoreflect.FileDescriptor) bool { count++; return true })
	log.Infof("gRPC reflection resolved %d proto files", count)
	return resolved, nil
}

func newGRPCReverseProxy(grpcURL string, tlsConfig *tls.Config) *httputil.ReverseProxy {
	scheme := "https"
	if tlsConfig == nil {
		scheme = "http"
	}
	target, _ := url.Parse(scheme + "://" + grpcURL)

	return &httputil.ReverseProxy{
		Rewrite:   func(r *httputil.ProxyRequest) { r.SetURL(target) },
		Transport: newGRPCTransport(tlsConfig),
	}
}

func newGRPCTransport(tlsConfig *tls.Config) http.RoundTripper {
	if tlsConfig != nil {
		// Clone: ForceAttemptHTTP2 makes net/http mutate TLSClientConfig.NextProtos in
		// place (via http2.ConfigureTransports) to add "h2". main.go passes this same
		// *tls.Config to other transports too (FulfillmentHTTPClient,
		// NewConsoleWebSocketProxy) that never opt into HTTP/2 — mutating the shared
		// original would make them silently offer ALPN "h2" while still speaking
		// HTTP/1.1, so Envoy resets their connections expecting an HTTP/2 preface it
		// never gets (OSAC-3081).
		return &http.Transport{
			TLSClientConfig:   tlsConfig.Clone(),
			ForceAttemptHTTP2: true,
		}
	}
	// http2.Transport (golang.org/x/net/http2) is deprecated in favor of
	// net/http.Transport's own Protocols field, which now supports h2c
	// (unencrypted HTTP/2) natively -- UnencryptedHTTP2 without HTTP1 makes
	// this transport speak HTTP/2 in cleartext for http:// requests instead
	// of falling back to HTTP/1.1.
	protocols := new(http.Protocols)
	protocols.SetUnencryptedHTTP2(true)
	return &http.Transport{
		Protocols: protocols,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, network, addr)
		},
	}
}
