module github.com/osac-project/osac/proto

go 1.26.3

// Direct requires for the generated code under gen/. Versions are pinned to
// match fulfillment-service/go.mod (the previous owner of this generated code).
// Run `go mod tidy` in this directory to populate indirect requires and go.sum.
require (
	buf.build/gen/go/bufbuild/protovalidate/protocolbuffers/go v1.36.12-20260825204119-511051f7f437.2
	github.com/grpc-ecosystem/grpc-gateway/v2 v2.31.0
	google.golang.org/genproto/googleapis/api v0.0.0-20260921155816-b14227669459
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260918162117-cecb64721679 // indirect
	google.golang.org/grpc v1.84.0
	google.golang.org/protobuf v1.36.12
)

require (
	golang.org/x/net v0.59.0 // indirect
	golang.org/x/sys v0.48.0 // indirect
	golang.org/x/text v0.42.0 // indirect
)
