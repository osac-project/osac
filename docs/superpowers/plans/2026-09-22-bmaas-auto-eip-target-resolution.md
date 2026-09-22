# BMaaS Auto-EIP Target Resolution Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (\`- [ ]\`) syntax for tracking.

**Goal:** Prevent valid BMaaS auto-EIP attachments from being deleted while their BMI Kubernetes CR is still being projected, while preserving cleanup for genuinely deleted or orphaned BMIs.

**Architecture:** Keep fulfillment-service as the authoritative BMI lifecycle source. Extend the OSAC operator attachment reconciler with a fulfillment \`BareMetalInstances/Get\` lookup for the missing-CR case; an active source BMI causes a deterministic wait/requeue, source deletion or \`NotFound\` causes cleanup, and transient source errors never cause deletion.

**Tech Stack:** Go, controller-runtime, multicluster-runtime, Ginkgo v2/Gomega, gRPC generated clients, fulfillment-service private API.

**Spec:** \`docs/superpowers/specs/2026-09-22-bmaas-auto-eip-target-resolution-design.md\`

## Global Constraints

- Never delete an ExternalIPAttachment solely because a Kubernetes informer/cache lookup is empty.
- Treat fulfillment-service as the source of truth when the BMI CR is absent.
- A transient gRPC error must retain the attachment and trigger retry.
- Preserve deletion handling when a BMI CR has a deletion timestamp.
- Do not change the public API or CRD schema.
- Do not modify tests in \`osac-test-infra\`; BMaaS tests are maintained in the OSAC mono-repo.

---

### Task 1: Add failing source-of-truth resolution tests

**Files:**
- Modify: \`osac-operator/internal/controller/externalipattachment_controller_test.go\`
- Create or modify: \`osac-operator/internal/controller/baremetalinstances_client_test.go\` for a focused generated-client test double if the existing feedback-controller mock cannot express configurable Get responses.

**Interfaces:**
- Consumes: \`ExternalIPAttachmentReconciler.resolveBaremetalInstance\` behavior and \`privatev1.BareMetalInstancesClient\`.
- Produces: a configurable fake that returns an active BMI, a deleting BMI, \`codes.NotFound\`, or a transient error for the referenced BMI UUID.

- [ ] **Step 1: Add a configurable BareMetalInstances client fake.**

Implement the generated client methods required by \`privatev1.BareMetalInstancesClient\`. The fake must record the requested ID and allow the test to set either a \`BareMetalInstancesGetResponse\` or an error. Non-Get methods can return an explicit \`errors.New("not implemented")\` because these tests exercise only target resolution.

- [ ] **Step 2: Add the fake client to the test reconciler setup.**

Set the new client on \`ExternalIPAttachmentReconciler\` in \`setupReconciler\`. Existing ComputeInstance and ClusterOrder tests must continue to use the same setup without requiring a BMI lookup.

- [ ] **Step 3: Add a failing test for an active source BMI with no BMI CR.**

Construct an attachment with \`Spec.BaremetalInstance\` set, omit the BMI CR from the fake Kubernetes client, configure the fake source client to return an active BMI, reconcile after the attachment finalizer pass, and assert:

\`\`\`go
Expect(result.RequeueAfter).To(Equal(defaultPreconditionRequeueInterval))
Expect(attachmentAfter.DeletionTimestamp).To(BeNil())
Expect(sourceClient.getID).To(Equal(testBMIUUID))
\`\`\`

This test must fail against the current implementation because the current code deletes the attachment immediately.

- [ ] **Step 4: Add failing tests for source deletion outcomes.**

Cover these cases separately:

\`\`\`text
source BMI has deletion metadata -> attachment deletion is requested
source Get returns codes.NotFound -> attachment deletion is requested
source Get returns an unavailable/internal error -> error is returned and attachment remains
\`\`\`

Assert that transient errors do not set \`DeletionTimestamp\`.

- [ ] **Step 5: Run the focused test and confirm the expected failure.**

Run:

\`\`\`bash
cd osac-operator
go test ./internal/controller -run TestExternalIPAttachmentReconciler -count=1
\`\`\`

Expected result: the new missing-CR active-BMI test fails because the current resolver deletes the attachment instead of requeueing.

### Task 2: Implement authoritative BMI resolution

**Files:**
- Modify: \`osac-operator/internal/controller/externalipattachment_controller.go\`, \`ExternalIPAttachmentReconciler\` fields and \`resolveBaremetalInstance\`.

**Interfaces:**
- Consumes: the configurable \`privatev1.BareMetalInstancesClient\` from Task 1.
- Produces: a resolver that returns an active Kubernetes BMI, a requeue result for an active source-only BMI, or deletion/error behavior based on authoritative source state.

- [ ] **Step 1: Add the source client field.**

Add an optional \`bareMetalInstancesClient privatev1.BareMetalInstancesClient\` field to \`ExternalIPAttachmentReconciler\`. Keep the field injectable so unit tests do not require a live gRPC connection.

- [ ] **Step 2: Preserve the existing Kubernetes-first path.**

When the BMI list contains an object, keep the current behavior unchanged: if it has a deletion timestamp, delete the attachment; otherwise add the external-IP detach finalizer and return the BMI.

- [ ] **Step 3: Replace empty-list deletion with source resolution.**

For an empty Kubernetes BMI list, call \`BareMetalInstances/Get\` with the referenced UUID. Handle the result as follows:

\`\`\`go
switch {
case err == nil && sourceBMIIsDeleting(response.GetObject()):
    deleteAttachment()
case err == nil:
    return nil, ctrl.Result{RequeueAfter: defaultPreconditionRequeueInterval}, nil
case status.Code(err) == codes.NotFound:
    deleteAttachment()
default:
    return nil, ctrl.Result{}, err
}
\`\`\`

The production implementation must fail closed if the source client is not configured: retain the attachment and return a retryable error or requeue result, never delete based on the missing CR alone. Use generated metadata deletion timestamp/status accessors and preserve \`client.IgnoreNotFound\` around attachment deletion.

- [ ] **Step 4: Add explicit logs for each branch.**

Use distinct messages for target CR waiting, source BMI deletion, source \`NotFound\`, and source lookup errors. Include the fulfillment BMI UUID in every message. Do not log credentials or full gRPC payloads.

- [ ] **Step 5: Run the focused controller tests.**

Run:

\`\`\`bash
cd osac-operator
go test ./internal/controller -run TestExternalIPAttachmentReconciler -count=1
\`\`\`

Expected result: all new source-resolution tests and existing attachment tests pass.

### Task 3: Wire the fulfillment BMI client into production

**Files:**
- Modify: \`osac-operator/cmd/main.go\`, \`setupExternalIPAttachmentControllers\`.

**Interfaces:**
- Consumes: existing \`grpcConn *grpc.ClientConn\` passed to the attachment-controller setup function.
- Produces: an attachment reconciler with \`privatev1.NewBareMetalInstancesClient(grpcConn)\` when the private API connection is available.

- [ ] **Step 1: Initialize the generated client from the existing gRPC connection.**

After constructing \`ExternalIPAttachmentReconciler\`, assign the generated client when \`grpcConn != nil\`. Do not create a second connection and do not change existing feedback-controller wiring.

- [ ] **Step 2: Define the no-connection behavior.**

When \`grpcConn\` is nil, retain the attachment and requeue/error on a missing BMI target. The absence of the optional source client must never cause deletion.

- [ ] **Step 3: Run formatting and compile checks.**

Run:

\`\`\`bash
gofmt -w osac-operator/cmd/main.go osac-operator/internal/controller/externalipattachment_controller.go osac-operator/internal/controller/externalipattachment_controller_test.go
go test ./osac-operator/cmd ./osac-operator/internal/controller -count=1
\`\`\`

Expected result: the operator packages compile and all targeted tests pass.

### Task 4: Verify lifecycle behavior and regression coverage

**Files:**
- Verify: \`tests/e2e/bmaas/regression/networking/test_bmaas_networking.py\` in the OSAC mono-repo.
- Verify: \`osac-operator/internal/controller/externalipattachment_controller_test.go\`.
- Verify: \`fulfillment-service/internal/servers/private_baremetal_instances_server_test.go\` for idempotent BMI auto-EIP cleanup.

**Interfaces:**
- Consumes: the reconciler implementation and production wiring from Tasks 2 and 3.
- Produces: evidence that creation races wait safely and genuine deletion still cleans up.

- [ ] **Step 1: Run the operator controller test package.**

Run:

\`\`\`bash
go test ./osac-operator/internal/controller/... -count=1
\`\`\`

Expected result: no regressions in ComputeInstance, ClusterOrder, ExternalIP, or attachment controller tests.

- [ ] **Step 2: Run the relevant fulfillment-service server tests.**

Run:

\`\`\`bash
go test ./fulfillment-service/internal/servers -run BareMetal -count=1
\`\`\`

Expected result: existing auto-EIP creation and delete-cascade tests pass unchanged.

- [ ] **Step 3: Build the operator and fulfillment-service binaries.**

Run:

\`\`\`bash
go build ./osac-operator/cmd/... ./fulfillment-service/cmd/...
\`\`\`

Expected result: both binaries build successfully.

- [ ] **Step 4: Run the BMaaS networking E2E regression.**

Use the existing mono-repo E2E command and run the networking regression with the explicit disk-image fixture. Verify that \`test_05b_verify_auto_eip_on_bmi3\` observes the attachment CR, reaches Ready, and observes settled BMI attribution without a timeout increase or fixed sleep.

- [ ] **Step 5: Verify genuine deletion.**

Delete a BMI with an auto-created EIP and confirm that the attachment and EIP are removed, the assigned BMH is released, and the tenant default VNet, subnet, and security group remain untouched.

- [ ] **Step 6: Commit the implementation as a focused change.**

Use a Jira-linked commit message:

\`\`\`bash
git add osac-operator/cmd/main.go osac-operator/internal/controller/externalipattachment_controller.go osac-operator/internal/controller/externalipattachment_controller_test.go
git commit -s -m "OSAC-5505: Resolve BMaaS auto-EIP targets from fulfillment API"
\`\`\`

