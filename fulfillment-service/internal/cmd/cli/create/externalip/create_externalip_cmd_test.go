/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package externalip

import (
	"context"
	"errors"
	"io"
	"log/slog"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc"

	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"

	"github.com/osac-project/osac/fulfillment-service/internal/config"
	"github.com/osac-project/osac/fulfillment-service/internal/terminal"
)

func newEIPTestConsole() *terminal.Console {
	console, err := terminal.NewConsole().
		SetLogger(slog.New(slog.NewTextHandler(io.Discard, nil))).
		SetStdout(io.Discard).
		SetStderr(io.Discard).
		Build()
	if err != nil {
		panic("failed to build test console: " + err.Error())
	}
	return console
}

// eipFixture bundles common test resources.
type eipFixture struct {
	ctrl       *gomock.Controller
	ctx        context.Context
	poolMock   *MockExternalIPPoolsClient
	eipMock    *MockExternalIPsClient
	ciMock     *MockComputeInstancesClient
	attachMock *MockExternalIPAttachmentsClient
	runner     *runnerContext
}

func newEIPFixture(t GinkgoTInterface) *eipFixture {
	ctrl := gomock.NewController(t)
	f := &eipFixture{
		ctrl:       ctrl,
		ctx:        context.Background(),
		poolMock:   NewMockExternalIPPoolsClient(ctrl),
		eipMock:    NewMockExternalIPsClient(ctrl),
		ciMock:     NewMockComputeInstancesClient(ctrl),
		attachMock: NewMockExternalIPAttachmentsClient(ctrl),
	}
	f.runner = &runnerContext{
		poolsClient:  f.poolMock,
		eipsClient:   f.eipMock,
		ciClient:     f.ciMock,
		attachClient: f.attachMock,
		settings:     &config.Settings{},
		console:      newEIPTestConsole(),
	}
	f.runner.args.pool = "pool-abc"
	f.runner.args.timeout = 0
	return f
}

// helpers

func eipMakePool(id, name string) *publicv1.ExternalIPPool {
	return publicv1.ExternalIPPool_builder{
		Id:       id,
		Metadata: publicv1.Metadata_builder{Name: name}.Build(),
	}.Build()
}

func eipMakeEIP(id, name string, state publicv1.ExternalIPState) *publicv1.ExternalIP {
	return publicv1.ExternalIP_builder{
		Id:       id,
		Metadata: publicv1.Metadata_builder{Name: name}.Build(),
		Status:   publicv1.ExternalIPStatus_builder{State: state}.Build(),
	}.Build()
}

func eipMakeCI(id, name string, state publicv1.ComputeInstanceState) *publicv1.ComputeInstance {
	return publicv1.ComputeInstance_builder{
		Id:       id,
		Metadata: publicv1.Metadata_builder{Name: name}.Build(),
		Status:   publicv1.ComputeInstanceStatus_builder{State: state}.Build(),
	}.Build()
}

// eipMakeAttachableCI returns a compute instance that satisfies the
// attachment API preconditions: RUNNING, READY condition, and a reported
// network attachment address.
func eipMakeAttachableCI(id, name string) *publicv1.ComputeInstance {
	return publicv1.ComputeInstance_builder{
		Id:       id,
		Metadata: publicv1.Metadata_builder{Name: name}.Build(),
		Status: publicv1.ComputeInstanceStatus_builder{
			State: publicv1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING,
			Conditions: []*publicv1.ComputeInstanceCondition{
				publicv1.ComputeInstanceCondition_builder{
					Type:   publicv1.ComputeInstanceConditionType_COMPUTE_INSTANCE_CONDITION_TYPE_READY,
					Status: publicv1.ConditionStatus_CONDITION_STATUS_TRUE,
				}.Build(),
			},
			ComputeNetworkAttachmentStatuses: []*publicv1.ComputeNetworkAttachmentStatus{
				publicv1.ComputeNetworkAttachmentStatus_builder{
					IpAddress: "192.0.2.10",
				}.Build(),
			},
		}.Build(),
	}.Build()
}

func eipMakeAttachment(id string) *publicv1.ExternalIPAttachment {
	return publicv1.ExternalIPAttachment_builder{Id: id}.Build()
}

var _ = Describe("createExternalIP", func() {
	var f *eipFixture

	BeforeEach(func() {
		f = newEIPFixture(GinkgoT())
	})

	AfterEach(func() { f.ctrl.Finish() })

	It("should create an EIP and return when --compute-instance is not set", func() {
		pool := eipMakePool("pool-1", "pool-abc")
		createdEIP := eipMakeEIP("eip-1", "my-ip", publicv1.ExternalIPState_EXTERNAL_IP_STATE_PENDING)

		f.poolMock.EXPECT().List(gomock.Any(), gomock.Any()).Return(
			publicv1.ExternalIPPoolsListResponse_builder{Items: []*publicv1.ExternalIPPool{pool}}.Build(), nil)
		f.eipMock.EXPECT().Create(gomock.Any(), gomock.Any()).Return(
			publicv1.ExternalIPsCreateResponse_builder{Object: createdEIP}.Build(), nil)

		// No computeInstance set — no wait / attach calls expected.
		Expect(f.runner.createExternalIP(f.ctx)).To(Succeed())
	})

	It("should create EIP, wait for ALLOCATED and CI readiness, and create attachment", func() {
		f.runner.args.computeInstance = "my-vm"

		pool := eipMakePool("pool-1", "pool-abc")
		createdEIP := eipMakeEIP("eip-1", "my-ip", publicv1.ExternalIPState_EXTERNAL_IP_STATE_PENDING)
		allocatedEIP := eipMakeEIP("eip-1", "my-ip", publicv1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED)
		ci := eipMakeAttachableCI("ci-001", "my-vm")
		attachment := eipMakeAttachment("attach-1")

		f.poolMock.EXPECT().List(gomock.Any(), gomock.Any()).Return(
			publicv1.ExternalIPPoolsListResponse_builder{Items: []*publicv1.ExternalIPPool{pool}}.Build(), nil)
		f.ciMock.EXPECT().List(gomock.Any(), gomock.Any()).Return(
			publicv1.ComputeInstancesListResponse_builder{Items: []*publicv1.ComputeInstance{ci}}.Build(), nil)
		f.eipMock.EXPECT().Create(gomock.Any(), gomock.Any()).Return(
			publicv1.ExternalIPsCreateResponse_builder{Object: createdEIP}.Build(), nil)
		// EIP poll — ALLOCATED on first try.
		f.eipMock.EXPECT().Get(gomock.Any(), gomock.Any()).Return(
			publicv1.ExternalIPsGetResponse_builder{Object: allocatedEIP}.Build(), nil)
		// CI readiness poll — attachable on first try.
		f.ciMock.EXPECT().Get(gomock.Any(), gomock.Any()).Return(
			publicv1.ComputeInstancesGetResponse_builder{Object: ci}.Build(), nil)
		// Attachment, named after the EIP.
		f.attachMock.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, req *publicv1.ExternalIPAttachmentsCreateRequest, _ ...grpc.CallOption) (*publicv1.ExternalIPAttachmentsCreateResponse, error) {
				Expect(req.GetObject().GetMetadata().GetName()).To(Equal("my-ip-attachment"))
				return publicv1.ExternalIPAttachmentsCreateResponse_builder{Object: attachment}.Build(), nil
			})

		Expect(f.runner.createExternalIP(f.ctx)).To(Succeed())
	})

	It("should return error when pool lookup fails (empty list)", func() {
		f.poolMock.EXPECT().List(gomock.Any(), gomock.Any()).Return(
			publicv1.ExternalIPPoolsListResponse_builder{Items: []*publicv1.ExternalIPPool{}}.Build(), nil)

		err := f.runner.createExternalIP(f.ctx)
		Expect(err).To(HaveOccurred())
	})

	It("should return error when pool lookup RPC fails", func() {
		f.poolMock.EXPECT().List(gomock.Any(), gomock.Any()).Return(nil, errors.New("rpc error"))

		err := f.runner.createExternalIP(f.ctx)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("failed to resolve external IP pool"))
	})

	It("should return error when EIP creation fails", func() {
		pool := eipMakePool("pool-1", "pool-abc")
		f.poolMock.EXPECT().List(gomock.Any(), gomock.Any()).Return(
			publicv1.ExternalIPPoolsListResponse_builder{Items: []*publicv1.ExternalIPPool{pool}}.Build(), nil)
		f.eipMock.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil, errors.New("quota exceeded"))

		err := f.runner.createExternalIP(f.ctx)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("failed to create external IP"))
	})

	It("should return error when EIP fails to reach ALLOCATED (terminal FAILED state)", func() {
		f.runner.args.computeInstance = "my-vm"

		pool := eipMakePool("pool-1", "pool-abc")
		createdEIP := eipMakeEIP("eip-1", "my-ip", publicv1.ExternalIPState_EXTERNAL_IP_STATE_PENDING)
		failedEIP := eipMakeEIP("eip-1", "my-ip", publicv1.ExternalIPState_EXTERNAL_IP_STATE_FAILED)
		ci := eipMakeAttachableCI("ci-001", "my-vm")

		f.poolMock.EXPECT().List(gomock.Any(), gomock.Any()).Return(
			publicv1.ExternalIPPoolsListResponse_builder{Items: []*publicv1.ExternalIPPool{pool}}.Build(), nil)
		f.ciMock.EXPECT().List(gomock.Any(), gomock.Any()).Return(
			publicv1.ComputeInstancesListResponse_builder{Items: []*publicv1.ComputeInstance{ci}}.Build(), nil)
		f.eipMock.EXPECT().Create(gomock.Any(), gomock.Any()).Return(
			publicv1.ExternalIPsCreateResponse_builder{Object: createdEIP}.Build(), nil)
		f.eipMock.EXPECT().Get(gomock.Any(), gomock.Any()).Return(
			publicv1.ExternalIPsGetResponse_builder{Object: failedEIP}.Build(), nil)

		err := f.runner.createExternalIP(f.ctx)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("did not reach ALLOCATED"))
	})

	It("should return error when CI lookup fails (not found)", func() {
		f.runner.args.computeInstance = "missing-vm"

		pool := eipMakePool("pool-1", "pool-abc")

		f.poolMock.EXPECT().List(gomock.Any(), gomock.Any()).Return(
			publicv1.ExternalIPPoolsListResponse_builder{Items: []*publicv1.ExternalIPPool{pool}}.Build(), nil)
		// CI list returns empty; no EIP should be created.
		f.ciMock.EXPECT().List(gomock.Any(), gomock.Any()).Return(
			publicv1.ComputeInstancesListResponse_builder{Items: []*publicv1.ComputeInstance{}}.Build(), nil)

		err := f.runner.createExternalIP(f.ctx)
		Expect(err).To(HaveOccurred())
	})

	It("should return error when the CI never becomes attachable (terminal STOPPED state)", func() {
		f.runner.args.computeInstance = "my-vm"

		pool := eipMakePool("pool-1", "pool-abc")
		createdEIP := eipMakeEIP("eip-1", "my-ip", publicv1.ExternalIPState_EXTERNAL_IP_STATE_PENDING)
		allocatedEIP := eipMakeEIP("eip-1", "my-ip", publicv1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED)
		runningCI := eipMakeCI("ci-001", "my-vm", publicv1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING)
		stoppedCI := eipMakeCI("ci-001", "my-vm", publicv1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STOPPED)

		f.poolMock.EXPECT().List(gomock.Any(), gomock.Any()).Return(
			publicv1.ExternalIPPoolsListResponse_builder{Items: []*publicv1.ExternalIPPool{pool}}.Build(), nil)
		f.ciMock.EXPECT().List(gomock.Any(), gomock.Any()).Return(
			publicv1.ComputeInstancesListResponse_builder{Items: []*publicv1.ComputeInstance{runningCI}}.Build(), nil)
		f.eipMock.EXPECT().Create(gomock.Any(), gomock.Any()).Return(
			publicv1.ExternalIPsCreateResponse_builder{Object: createdEIP}.Build(), nil)
		f.eipMock.EXPECT().Get(gomock.Any(), gomock.Any()).Return(
			publicv1.ExternalIPsGetResponse_builder{Object: allocatedEIP}.Build(), nil)
		// CI readiness poll transitions to a terminal state.
		f.ciMock.EXPECT().Get(gomock.Any(), gomock.Any()).Return(
			publicv1.ComputeInstancesGetResponse_builder{Object: stoppedCI}.Build(), nil)

		err := f.runner.createExternalIP(f.ctx)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("did not become ready for attachment"))
	})

	It("should return error when attachment creation fails", func() {
		f.runner.args.computeInstance = "my-vm"

		pool := eipMakePool("pool-1", "pool-abc")
		createdEIP := eipMakeEIP("eip-1", "my-ip", publicv1.ExternalIPState_EXTERNAL_IP_STATE_PENDING)
		allocatedEIP := eipMakeEIP("eip-1", "my-ip", publicv1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED)
		ci := eipMakeAttachableCI("ci-001", "my-vm")

		f.poolMock.EXPECT().List(gomock.Any(), gomock.Any()).Return(
			publicv1.ExternalIPPoolsListResponse_builder{Items: []*publicv1.ExternalIPPool{pool}}.Build(), nil)
		f.eipMock.EXPECT().Create(gomock.Any(), gomock.Any()).Return(
			publicv1.ExternalIPsCreateResponse_builder{Object: createdEIP}.Build(), nil)
		f.eipMock.EXPECT().Get(gomock.Any(), gomock.Any()).Return(
			publicv1.ExternalIPsGetResponse_builder{Object: allocatedEIP}.Build(), nil)
		f.ciMock.EXPECT().List(gomock.Any(), gomock.Any()).Return(
			publicv1.ComputeInstancesListResponse_builder{Items: []*publicv1.ComputeInstance{ci}}.Build(), nil)
		// CI readiness poll — attachable on first try.
		f.ciMock.EXPECT().Get(gomock.Any(), gomock.Any()).Return(
			publicv1.ComputeInstancesGetResponse_builder{Object: ci}.Build(), nil)
		f.attachMock.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil, errors.New("permission denied"))

		err := f.runner.createExternalIP(f.ctx)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("attachment to compute instance"))
	})
})
