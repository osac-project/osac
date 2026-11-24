# M360 RevOS adapter

This adapter is the only place that translates M360 RevOS API responses into
OSAC's provider-neutral billing types (see OSAC-3784 design §2.3: mapping a
provider's native shape onto OSAC concepts is the adapter's responsibility).

## Account field mapping

M360 account records expose UUID pointers whose readable values live in the
companion `__r` objects. The adapter reads the readable side only:

| M360 field            | Read from          | Maps to `billing.Account` |
|-----------------------|--------------------|---------------------------|
| `Id`                  | —                  | `Ref` (via `NewAccountRef`) |
| `Name`                | —                  | `Name`                    |
| `ExternalId`          | —                  | `ExternalID` (correlation value) |
| `Status__r.Name`      | readable status    | `RawStatus`               |
| `ApprovalStatus__r.Name` | readable approval | `ApprovalStatus`       |
| —                     | —                  | `BaseCurrency` stays empty: M360 has no account currency field |

A missing or null `Status__r` / `ApprovalStatus__r` reads as the empty string.

## Correlation value (`externalId`)

OSAC's provider-neutral correlation value maps to the M360 account `ExternalId`. The adapter
reads that field and does not write it. The provider-neutral `billing.Adapter` exposes
neither a correlation setter nor a write-capability query.

This is the **approved OSAC capability for this increment**, not a claim about what M360 can
do. During assignment, the resolution still generates or adopts the value and stores it on the
OSAC side, but it never copies it to M360. When no provider account carries that value,
the assignment stays `PENDING` with the
stable reason `BILLING_PROVIDER_CORRELATION_WRITE_UNSUPPORTED` until the Cloud Provider Admin
sets the matching `externalId` on the M360 account (the value is shown by
`BillingAccounts.List`) and re-submits `Update`. See the merged design §4.1 note, §4.6, and the
correlation-id drawback. Revalidation uses one account correlation read and one complete,
exact-value carrier walk (at most 50 pages). `CONNECTED` requires the selected native account
to report the value and be its sole listed carrier. A foreign, multiple, or contradictory
carrier fails with `ACCOUNT_AMBIGUOUS`; a provider-present value with no listed carrier stays
`PENDING`. Nonblank values retain their exact bytes; no provider write or readback is attempted.

### Vendor fact versus OSAC authority

The two statements below are deliberately separate:

- **Current vendor fact.** The vendor's current Accounts documentation (read 2026-11-13)
  describes `PUT /accounts/{accountId}` as updating scalar fields on an existing account. A
  disposable-account probe against the supplied M360 UAT host recorded `POST` `201` without
  `externalId`, an `externalId`-only `PUT` `200` whose value the following `GET` returned, a
  repeated identical `PUT` `200`, and `DELETE` `204` followed by `GET` `404`. That is evidence
  about the documented API and one UAT deployment, not a guarantee for the version in use.
- **OSAC approved boundary.** Correlation resolution is read-only for every adapter in this
  increment. A locally retained value remains `PENDING` until external administration supplies
  the matching provider value and read-only revalidation proves the selected unique carrier.
  The stable unsupported-write reason is manual-remediation text, not an executable capability.

The exact M360 version OSAC may be configured against is unknown, so this README does not
promise that the update endpoint is absent, or that an unsupported route presents a particular
status: an unknown deployment could answer `404` as readily as `405`. The adapter makes no
update request, so no such assumption is encoded in behavior.

The test stub in `internal/testing/billingstub` serves GET requests only and rejects and records
all non-GET requests. Tests use `SetScenario` to model external manual remediation; they exercise
the real adapter's read protocol, not the vendor update endpoint.

No host, token, account id or correlation value from the probe is recorded in this repository.

## Account state mapping

`mapState` in `accounts.go` normalizes the two raw signals into one
`billing.AccountState`, which the servers map to the
`BILLING_ACCOUNT_STATE_*` proto enum served to the UI:

- **INACTIVE wins**: if either signal maps to inactive, the state is `INACTIVE`.
- **ACTIVE requires both signals**: only `Active` status **and** `Approved`
  approval produce `ACTIVE`. ACTIVE is never returned on a partial signal.
- **Everything else is `UNKNOWN`**: unmapped values, missing signals, or
  ambiguous combinations.

| `Status__r.Name` | `ApprovalStatus__r.Name` | State |
|------------------|--------------------------|-------|
| `Active`         | `Approved`               | `ACTIVE` |
| `Inactive`       | any                      | `INACTIVE` |
| any              | `Rejected`               | `INACTIVE` |
| `Active`         | anything else            | `UNKNOWN` |
| anything else    | `Approved`               | `UNKNOWN` |
| missing          | missing                  | `UNKNOWN` |

## Recognized M360 values

Only these literals carry a signal (`statusSignal` / `approvalSignal` in
`accounts.go`); every other value — including empty and null — is neutral and
leads to `UNKNOWN`:

- Status: `Active`, `Inactive`
- Approval: `Approved`, `Rejected`

Values observed on the UAT tenant but **not** recognized: `Draft` (approval),
and null `Status__r` (common). Accounts with those read as `UNKNOWN`, or
`INACTIVE` when the other signal is negative. Extending the recognized set is a
one-line change to the signal maps; the M360 picklists are not documented in
the public API, so verify new literals against the live tenant first.

Pricing mappings (service, dimension, unit, currency) will be documented here
as they land with catalog pricing (OSAC-6873).
