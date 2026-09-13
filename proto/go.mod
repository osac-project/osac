module github.com/osac-project/osac/proto

go 1.26.3

// Direct requires for the generated code under gen/. Versions are pinned to
// match fulfillment-service/go.mod (the previous owner of this generated code).
// Run `go mod tidy` in this directory to populate indirect requires and go.sum.
require (
	buf.build/gen/go/bufbuild/protovalidate/protocolbuffers/go v1.36.12-20260825204119-511051f7f437.1
	github.com/grpc-ecosystem/grpc-gateway/v2 v2.29.0
	google.golang.org/genproto/googleapis/api v0.0.0-20260825221802-da73d73af1c5
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260825221802-da73d73af1c5 // indirect
	google.golang.org/grpc v1.83.2
	google.golang.org/protobuf v1.36.12
)

require (
	github.com/go-logr/logr v1.4.4 // indirect
	golang.org/x/net v0.58.0 // indirect
	golang.org/x/sys v0.48.0 // indirect
	golang.org/x/text v0.41.0 // indirect
)
