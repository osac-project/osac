/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package servers

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/database"
	"github.com/osac-project/osac/fulfillment-service/internal/validation"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

func architectureTypeFixture(architecture string) *privatev1.BareMetalInstanceType {
	return privatev1.BareMetalInstanceType_builder{
		Id: "architecture-type",
		Metadata: privatev1.Metadata_builder{
			Name: "architecture-type", Tenant: auth.SharedTenant, Creator: "system",
			Labels:      map[string]string{"profile": "compute"},
			Annotations: map[string]string{"example.test/note": "preserve"},
		}.Build(),
		Spec: privatev1.BareMetalInstanceTypeSpec_builder{
			Description:       "Catalog architecture fixture",
			HostLabelSelector: privatev1.BareMetalLabelSelector_builder{MatchLabels: map[string]string{"profile": "compute"}}.Build(),
			Hardware: privatev1.BareMetalHardwareSpec_builder{
				Cpu:          privatev1.BareMetalCPUSpec_builder{Cores: 8, ThreadsPerCore: 2, Architecture: architecture, Model: "fixture-cpu"}.Build(),
				Memory:       privatev1.BareMetalMemorySpec_builder{TotalGb: 32, Type: "DDR5"}.Build(),
				Disks:        []*privatev1.BareMetalDiskSpec{privatev1.BareMetalDiskSpec_builder{Type: "SSD", CapacityGb: 512, Interface: "SATA"}.Build()},
				Accelerators: []*privatev1.BareMetalAcceleratorSpec{privatev1.BareMetalAcceleratorSpec_builder{Type: "GPU", Model: "fixture-gpu"}.Build()},
				NetworkPorts: []*privatev1.BareMetalNetworkPortSpec{privatev1.BareMetalNetworkPortSpec_builder{Name: "data-0", Role: "fabric", Type: "Ethernet", Speed: "25Gbps"}.Build()},
				Capabilities: map[string]string{"boot": "uefi"},
			}.Build(),
		}.Build(),
	}.Build()
}

var _ = Describe("Bare metal type architecture Create", func() {
	DescribeTable("accepts canonical inputs and rejects variants without invalid or dry-run persistence",
		func(architecture string, accepted bool, dryRun bool) {
			Expect(suiteTx.End(ctx)).To(Succeed())
			suiteTx = nil
			ctx = database.TxManagerIntoContext(context.Background(), tm)
			service, err := NewPrivateBareMetalInstanceTypesServer().SetLogger(logger).
				SetAttributionLogic(attribution).SetTenancyLogic(tenancy).Build()
			Expect(err).ToNot(HaveOccurred())
			validator, err := validation.NewProtovalidateInterceptor().SetLogger(logger).Build()
			Expect(err).ToNot(HaveOccurred())
			object := architectureTypeFixture(architecture)
			operationCtx := ctx
			if dryRun {
				operationCtx = metadata.NewIncomingContext(operationCtx, metadata.Pairs(DryRunMetadataKey, "true"))
			}
			response, err := database.WithNewTx(operationCtx, func(txCtx context.Context) (*privatev1.BareMetalInstanceTypesCreateResponse, error) {
				request := privatev1.BareMetalInstanceTypesCreateRequest_builder{Object: object}.Build()
				result, err := validator.UnaryServer(txCtx, request, &grpc.UnaryServerInfo{},
					func(handlerCtx context.Context, _ any) (any, error) { return service.Create(handlerCtx, request) })
				if err != nil {
					return nil, err
				}
				return result.(*privatev1.BareMetalInstanceTypesCreateResponse), nil
			})
			if accepted {
				Expect(err).ToNot(HaveOccurred())
				Expect(response.GetObject().GetSpec().GetHardware().GetCpu().GetArchitecture()).To(Equal(architecture))
			} else {
				Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
				Expect(err.Error()).To(And(ContainSubstring("architecture"), ContainSubstring("amd64"), ContainSubstring("arm64"), ContainSubstring("s390x")))
			}
			stored, readErr := database.WithNewTx(ctx, func(txCtx context.Context) (*privatev1.BareMetalInstanceTypesGetResponse, error) {
				return service.Get(txCtx, privatev1.BareMetalInstanceTypesGetRequest_builder{Id: object.GetId()}.Build())
			})
			if accepted && !dryRun {
				Expect(readErr).ToNot(HaveOccurred())
				Expect(stored.GetObject().GetId()).To(Equal(object.GetId()))
				Expect(stored.GetObject().GetSpec().GetHardware().GetCpu().GetArchitecture()).To(Equal(architecture))
			} else {
				Expect(status.Code(readErr)).To(Equal(codes.NotFound))
			}
		},
		Entry("amd64", "amd64", true, false),
		Entry("amd64 dry run", "amd64", true, true),
		Entry("arm64", "arm64", true, false),
		Entry("arm64 dry run", "arm64", true, true),
		Entry("s390x", "s390x", true, false),
		Entry("s390x dry run", "s390x", true, true),
		Entry("x86 alias", "x86_64", false, false),
		Entry("x86 alias dry run", "x86_64", false, true),
		Entry("arm alias", "aarch64", false, false),
		Entry("arm alias dry run", "aarch64", false, true),
		Entry("uppercase x86", "AMD64", false, false),
		Entry("uppercase x86 dry run", "AMD64", false, true),
		Entry("uppercase arm", "ARM64", false, false),
		Entry("uppercase arm dry run", "ARM64", false, true),
		Entry("uppercase s390x", "S390X", false, false),
		Entry("uppercase s390x dry run", "S390X", false, true),
		Entry("mixed case", "Amd64", false, false),
		Entry("mixed case dry run", "Amd64", false, true),
		Entry("leading space", " amd64", false, false),
		Entry("leading space dry run", " amd64", false, true),
		Entry("trailing space", "amd64 ", false, false),
		Entry("trailing space dry run", "amd64 ", false, true),
		Entry("arm whitespace", " arm64 ", false, false),
		Entry("arm whitespace dry run", " arm64 ", false, true),
		Entry("s390x whitespace", " s390x ", false, false),
		Entry("s390x whitespace dry run", " s390x ", false, true),
		Entry("newline", "amd64\n", false, false),
		Entry("newline dry run", "amd64\n", false, true),
		Entry("empty", "", false, false),
		Entry("empty dry run", "", false, true),
		Entry("unknown", "ppc64le", false, false),
		Entry("unknown dry run", "ppc64le", false, true),
	)
})
