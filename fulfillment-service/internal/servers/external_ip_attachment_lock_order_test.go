/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package servers

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/osac-project/osac/fulfillment-service/internal/controllers/finalizers"
	"github.com/osac-project/osac/fulfillment-service/internal/database"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

var _ = Describe("ExternalIPAttachment target lock ordering", func() {
	attachmentState := func(ready bool) privatev1.ExternalIPAttachmentState {
		if ready {
			return privatev1.ExternalIPAttachmentState_EXTERNAL_IP_ATTACHMENT_STATE_READY
		}
		return privatev1.ExternalIPAttachmentState_EXTERNAL_IP_ATTACHMENT_STATE_PENDING
	}

	runParentDeleteRace := func(deleteAttachment bool) {
		// The normal suite transaction owns its fixtures. Commit it so independent
		// transactions can contend on the same rows.
		Expect(suiteTx.End(ctx)).To(Succeed())
		suiteTx = nil

		setupTx, err := tm.Begin(context.Background())
		Expect(err).ToNot(HaveOccurred())
		setupCtx := database.TxIntoContext(context.Background(), setupTx)
		computeDAO, err := dao.NewGenericDAO[*privatev1.ComputeInstance]().
			SetLogger(logger).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())
		externalIPDAO, err := dao.NewGenericDAO[*privatev1.ExternalIP]().
			SetLogger(logger).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())
		poolDAO, err := dao.NewGenericDAO[*privatev1.ExternalIPPool]().
			SetLogger(logger).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())
		attachmentDAO, err := dao.NewGenericDAO[*privatev1.ExternalIPAttachment]().
			SetLogger(logger).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())

		ciID := uuid.NewString()
		computeInstance := privatev1.ComputeInstance_builder{
			Id: ciID,
			Metadata: privatev1.Metadata_builder{
				Tenant:     testTenant,
				Name:       "lock-order-" + ciID,
				Finalizers: []string{finalizers.Controller},
			}.Build(),
			Spec: privatev1.ComputeInstanceSpec_builder{
				AutoExternalIpAttachment: proto.Bool(true),
			}.Build(),
		}.Build()
		_, err = computeDAO.Create().SetObject(computeInstance).Do(setupCtx)
		Expect(err).ToNot(HaveOccurred())

		poolID := uuid.NewString()
		_, err = poolDAO.Create().SetObject(privatev1.ExternalIPPool_builder{
			Id: poolID,
			Metadata: privatev1.Metadata_builder{
				Tenant: testTenant,
				Name:   "lock-order-" + poolID,
			}.Build(),
			Spec: privatev1.ExternalIPPoolSpec_builder{
				IpFamily: privatev1.IPFamily_IP_FAMILY_IPV4,
			}.Build(),
			Status: privatev1.ExternalIPPoolStatus_builder{
				State:     privatev1.ExternalIPPoolState_EXTERNAL_IP_POOL_STATE_READY,
				Total:     5,
				Available: 5,
			}.Build(),
		}.Build()).Do(setupCtx)
		Expect(err).ToNot(HaveOccurred())

		externalIPID := uuid.NewString()
		externalIP := privatev1.ExternalIP_builder{
			Id: externalIPID,
			Metadata: privatev1.Metadata_builder{
				Tenant:  testTenant,
				Name:    "lock-order-" + externalIPID,
				Creator: systemCreator,
				Labels: map[string]string{
					autoCreatedLabel:     "true",
					autoCreatedForLabel:  ciID,
					autoCreatedKindLabel: "compute_instance",
				},
				Annotations: map[string]string{
					ownerReferenceAnnotation: ciID,
					tenantAnnotation:         testTenant,
				},
			}.Build(),
			Spec: privatev1.ExternalIPSpec_builder{
				Pool: privatev1.ExternalIPPoolReference_builder{Id: poolID}.Build(),
			}.Build(),
			Status: privatev1.ExternalIPStatus_builder{
				State:    privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED,
				Address:  "203.0.113.42",
				Hub:      "network-hub-a",
				Attached: deleteAttachment,
			}.Build(),
		}.Build()
		_, err = externalIPDAO.Create().SetObject(externalIP).Do(setupCtx)
		Expect(err).ToNot(HaveOccurred())
		Expect(UpdatePoolCapacity(setupCtx, poolDAO, poolID, 1)).To(Succeed())

		attachmentID := uuid.NewString()
		attachment := privatev1.ExternalIPAttachment_builder{
			Id: attachmentID,
			Metadata: privatev1.Metadata_builder{
				Tenant:  testTenant,
				Name:    "lock-order-" + attachmentID,
				Creator: systemCreator,
				Labels: map[string]string{
					autoCreatedLabel:     "true",
					autoCreatedForLabel:  ciID,
					autoCreatedKindLabel: "compute_instance",
				},
				Annotations: map[string]string{
					ownerReferenceAnnotation: ciID,
					tenantAnnotation:         testTenant,
				},
			}.Build(),
			Spec: privatev1.ExternalIPAttachmentSpec_builder{
				ExternalIp:      privatev1.ExternalIPLocalReference_builder{Id: externalIPID}.Build(),
				ComputeInstance: privatev1.ComputeInstanceLocalReference_builder{Id: ciID}.Build(),
			}.Build(),
			Status: privatev1.ExternalIPAttachmentStatus_builder{
				State:               attachmentState(deleteAttachment),
				ExternalIpAddress:   "203.0.113.42",
				Hub:                 "network-hub-a",
				StateTransitionTime: timestamppb.Now(),
			}.Build(),
		}.Build()
		_, err = attachmentDAO.Create().SetObject(attachment).Do(setupCtx)
		Expect(err).ToNot(HaveOccurred())
		Expect(setupTx.End(setupCtx)).To(Succeed())

		computeServer, err := NewPrivateComputeInstancesServer().
			SetLogger(logger).
			SetAttributionLogic(attribution).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())
		attachmentServer, err := NewPrivateExternalIPAttachmentsServer().
			SetLogger(logger).
			SetAttributionLogic(attribution).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())

		parentTx, err := tm.Begin(context.Background())
		Expect(err).ToNot(HaveOccurred())
		parentCtx := database.TxIntoContext(context.Background(), parentTx)
		_, err = computeDAO.Get().SetId(ciID).SetLock(true).Do(parentCtx)
		Expect(err).ToNot(HaveOccurred())

		attachmentTx, err := tm.Begin(context.Background())
		Expect(err).ToNot(HaveOccurred())
		attachmentCtx := database.TxIntoContext(context.Background(), attachmentTx)
		var attachmentPID int
		Expect(attachmentTx.QueryRow(attachmentCtx, "select pg_backend_pid()").Scan(&attachmentPID)).To(Succeed())

		type attachmentOperationResult struct {
			operationErr   error
			transactionErr error
		}
		operationResults := make(chan attachmentOperationResult, 1)
		go func() {
			var operationErr error
			if deleteAttachment {
				_, operationErr = attachmentServer.Delete(attachmentCtx, privatev1.ExternalIPAttachmentsDeleteRequest_builder{
					Id: attachmentID,
				}.Build())
			} else {
				updated := proto.Clone(attachment).(*privatev1.ExternalIPAttachment)
				updated.GetStatus().SetState(privatev1.ExternalIPAttachmentState_EXTERNAL_IP_ATTACHMENT_STATE_READY)
				updated.GetStatus().SetStateTransitionTime(timestamppb.Now())
				_, operationErr = attachmentServer.Update(attachmentCtx, privatev1.ExternalIPAttachmentsUpdateRequest_builder{
					Object: updated,
					UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{
						"status.state",
						"status.state_transition_time",
					}},
				}.Build())
			}
			endErr := attachmentTx.End(attachmentCtx)
			operationResults <- attachmentOperationResult{operationErr: operationErr, transactionErr: endErr}
		}()

		// Wait until the attachment transaction is blocked acquiring its target row.
		// This confirms it has completed the preceding locks before parent cleanup runs.
		monitorTx, err := tm.Begin(context.Background())
		Expect(err).ToNot(HaveOccurred())
		monitorCtx := database.TxIntoContext(context.Background(), monitorTx)
		Eventually(func() string {
			var waitEvent, query string
			queryErr := monitorTx.QueryRow(monitorCtx,
				"select coalesce(wait_event_type, ''), coalesce(query, '') from pg_stat_activity where pid = $1",
				attachmentPID,
			).Scan(&waitEvent, &query)
			if queryErr != nil {
				return ""
			}
			return strings.ToLower(waitEvent + " " + query)
		}, 10*time.Second, 10*time.Millisecond).Should(SatisfyAll(
			ContainSubstring("lock"),
			ContainSubstring("compute_instances"),
			ContainSubstring("for update"),
		))
		Expect(monitorTx.End(monitorCtx)).To(Succeed())

		// Run the real parent Delete method with the target lock already held by
		// this transaction. Network cleanup is left to the parent reconciler.
		deleteRequest := privatev1.ComputeInstancesDeleteRequest_builder{Id: ciID}.Build()
		_, parentErr := computeServer.Delete(parentCtx, deleteRequest)
		parentTx.ReportError(&parentErr)
		parentEndErr := parentTx.End(parentCtx)
		Expect(parentErr).ToNot(HaveOccurred())
		Expect(parentEndErr).ToNot(HaveOccurred())

		result := <-operationResults
		if deleteAttachment {
			Expect(result.operationErr).ToNot(HaveOccurred())
		} else {
			Expect(grpcstatus.Code(result.operationErr)).To(Equal(grpccodes.FailedPrecondition))
		}
		Expect(result.transactionErr).ToNot(HaveOccurred())

		verifyTx, err := tm.Begin(context.Background())
		Expect(err).ToNot(HaveOccurred())
		verifyCtx := database.TxIntoContext(context.Background(), verifyTx)
		DeferCleanup(func() {
			Expect(verifyTx.End(verifyCtx)).To(Succeed())
		})
		deletedParent, err := computeDAO.Get().SetId(ciID).Do(verifyCtx)
		Expect(err).ToNot(HaveOccurred())
		Expect(deletedParent.GetObject().GetMetadata().GetDeletionTimestamp()).ToNot(BeNil())
		Expect(deletedParent.GetObject().GetMetadata().GetFinalizers()).To(ContainElement(finalizers.Controller))
		_, err = externalIPDAO.Get().SetId(externalIPID).Do(verifyCtx)
		Expect(err).ToNot(HaveOccurred())
		attachmentList, err := attachmentDAO.List().SetFilter("this.id == '" + attachmentID + "'").Do(verifyCtx)
		Expect(err).ToNot(HaveOccurred())
		if deleteAttachment {
			Expect(attachmentList.GetItems()).To(BeEmpty())
		} else {
			Expect(attachmentList.GetItems()).To(HaveLen(1))
		}
	}

	It("does not deadlock when parent Delete races attachment Update", func() {
		runParentDeleteRace(false)
	})

	It("does not deadlock when parent Delete races attachment Delete", func() {
		runParentDeleteRace(true)
	})
})
