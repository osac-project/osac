# BMaaS Auto-EIP Target Resolution Design

## Status

Draft for review.

## Problem

When a BareMetalInstance is created with `auto_external_ip_attachment=true`,
fulfillment-service creates the BMI, ExternalIP, and ExternalIPAttachment in
the fulfillment database. Kubernetes resources are projected asynchronously by
separate controllers.

The ExternalIPAttachment controller currently treats an empty cached
BareMetalInstance lookup as proof that the BMI was deleted and immediately
deletes the attachment. During normal creation, the attachment event can be
reconciled before the BMI CR has been created or observed by the operator. The
valid attachment is therefore deleted even though no deletion was requested.

The disk-image creation path made this ordering window reproducible, but the
underlying defect is the interpretation of an eventually consistent
Kubernetes lookup.

## Goals

- Make attachment creation deterministic with respect to the fulfillment API
  source of truth.
- Never delete an attachment solely because the BMI CR is temporarily absent.
- Preserve automatic cleanup when the BMI is genuinely being deleted or does
  not exist in fulfillment-service.
- Keep fulfillment-service as the owner of API resource intent and the
  operator as the Kubernetes projection and reconciliation layer.
- Avoid a fixed sleep or a larger E2E polling timeout as the primary fix.

## Non-goals

- Moving auto-EIP creation into the bare-metal Kubernetes operator.
- Changing the public API contract for BMI creation.
- Adding a new persistent resource or database table.
- Changing the existing EIP allocation or AAP provisioning workflow.

## Decision

Use fulfillment-service as the source of truth when the target BMI CR is not
found.

The ExternalIPAttachment reconciler resolves the target in this order:

1. Look up the BMI CR in Kubernetes.
2. If the CR exists and is not being deleted, continue normal attachment
   reconciliation.
3. If the CR exists with a deletion timestamp, delete the attachment and allow
   normal finalizer processing to detach the EIP.
4. If the CR is absent, call the private `BareMetalInstances/Get` API using the
   referenced BMI UUID:
   - BMI exists and is active: retain the attachment and requeue after the
     standard precondition interval.
   - BMI exists and is being deleted: delete the attachment.
   - API returns `NotFound`: delete the attachment as an orphan.
   - Any other API error: return the error and retain the attachment for retry.

The controller must not make a deletion decision from an informer-cache miss.
Using `APIReader` can reduce cache staleness, but is not sufficient because the
BMI CR may not yet exist in the Kubernetes API server. The fulfillment lookup
distinguishes not-yet-projected from deleted.

## Reconciliation state machine

```text
ExternalIPAttachment exists
            |
            v
       BMI CR found?
        /          \\
      yes           no
       |             |
 BMI deleting?   Fulfillment BMI Get
    /      \\       /      |       \\
  yes      no   active  deleting  NotFound
   |        |      |       |         |
 delete   proceed wait/requeue delete delete
```

The waiting path is intentionally not bounded by a client-side timeout. If
the BMI exists in fulfillment-service but its CR is never projected, deleting
the attachment would lose valid user intent. Projection failure should remain
visible through logs and metrics and be repaired by the responsible
controller. Deleting the BMI through fulfillment-service remains the
authoritative cleanup operation and is already idempotent.

## Component changes

### osac-operator

- Inject a `privatev1.BareMetalInstancesClient` into
  `ExternalIPAttachmentReconciler`.
- Add a source-of-truth helper for the missing-BMI case.
- Replace immediate deletion on an empty BMI list with the state machine above.
- Log the distinction between waiting for projection, target deletion, and
  source-of-truth NotFound.
- Return the standard precondition requeue interval for an active BMI whose CR
  is not yet present.

No API or CRD schema change is required.

### fulfillment-service

No functional change is required for the initial implementation. The existing
BMI delete cascade remains the authoritative cleanup path for auto-created EIP
and attachment resources. Its behavior should be verified as idempotent when
the operator also observes deletion.

## Error and race handling

- A transient gRPC failure must never be interpreted as `NotFound`.
- The source lookup must inspect BMI deletion metadata/status when an object is
  returned, not only object existence.
- Source lookup and Kubernetes reconciliation are expected to race; both paths
  must be safe to repeat.
- Attachment deletion remains delegated to the existing finalizer and
  provisioning lifecycle.
- A BMI CR appearing after one or more waiting reconciles follows the same path
  as a BMI CR present from the first reconcile.

## Tests

Add operator unit coverage for:

1. Missing BMI CR plus active fulfillment BMI: no deletion, standard requeue.
2. Missing BMI CR plus deleting fulfillment BMI: attachment deletion requested.
3. Missing BMI CR plus fulfillment `NotFound`: attachment deletion requested.
4. Missing BMI CR plus transient fulfillment error: error returned and
   attachment retained.
5. BMI CR appears after a waiting reconcile: normal reconciliation continues.
6. Existing BMI CR deletion behavior remains unchanged.

Extend BMaaS E2E coverage to create a BMI with an explicit disk image and
auto-EIP enabled, then verify that the attachment CR reaches `Ready` and
ExternalIP attribution settles. The test must pass repeatedly without a larger
timeout.

## Acceptance criteria

- The reproduced `test_05b_verify_auto_eip_on_bmi3` failure no longer deletes
  the attachment during BMI CR projection.
- The attachment reaches `Ready` and its ExternalIP attribution references the
  BMI.
- Genuine BMI deletion still removes the attachment and releases the EIP.
- No fixed sleep is required in fulfillment-service or the test suite.
- Existing compute-instance, cluster, and manually-created attachment cleanup
  behavior remains unchanged.

## Alternatives considered

### Requeue on every missing BMI

This is a useful immediate safety fix and removes the race, but it cannot
distinguish a BMI still being projected from an orphaned attachment. It is
insufficient as the long-term deterministic policy.

### Create the attachment only from the Kubernetes operator

This would enforce Kubernetes ordering but would move fulfillment resource
ownership into the operator and create a larger cross-component API change. It
is not needed to solve the current race.

