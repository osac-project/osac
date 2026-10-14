module github.com/osac-project/osac/osac-csi-driver

go 1.26.3

require (
	github.com/container-storage-interface/spec v1.13.0
	github.com/kubernetes-csi/csi-test/v5 v5.6.0
	github.com/osac-project/osac/proto v0.0.0-00010101000000-000000000000
	go.yaml.in/yaml/v3 v3.0.5
	golang.org/x/oauth2 v0.37.0
	google.golang.org/grpc v1.84.0
	google.golang.org/protobuf v1.36.12
	k8s.io/klog/v2 v2.140.0
)

require (
	buf.build/gen/go/bufbuild/protovalidate/protocolbuffers/go v1.36.12-20260825204119-511051f7f437.2 // indirect
	cloud.google.com/go/compute/metadata v0.10.0 // indirect
	github.com/Masterminds/semver/v3 v3.5.0 // indirect
	github.com/go-logr/logr v1.4.4 // indirect
	github.com/go-task/slim-sprig/v3 v3.0.0 // indirect
	github.com/google/go-cmp v0.7.0 // indirect
	github.com/google/pprof v0.0.0-20261012231050-2af12c3f4d87 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/grpc-ecosystem/grpc-gateway/v2 v2.31.0 // indirect
	github.com/onsi/ginkgo/v2 v2.33.1 // indirect
	github.com/onsi/gomega v1.44.0 // indirect
	github.com/rogpeppe/go-internal v1.16.0 // indirect
	github.com/stretchr/testify v1.12.1 // indirect
	go.uber.org/mock v0.6.0 // indirect
	golang.org/x/mod v0.42.0 // indirect
	golang.org/x/net v0.61.0 // indirect
	golang.org/x/sync v0.24.0 // indirect
	golang.org/x/sys v0.49.0 // indirect
	golang.org/x/text v0.43.0 // indirect
	golang.org/x/tools v0.52.0 // indirect
	google.golang.org/genproto/googleapis/api v0.0.0-20261012220534-4341edf3f941 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20261012220534-4341edf3f941 // indirect
	gopkg.in/check.v1 v1.0.0-20201130134442-10cb98267c6c // indirect
	gopkg.in/yaml.v2 v2.4.0 // indirect
)

replace github.com/osac-project/osac/proto => ../proto
