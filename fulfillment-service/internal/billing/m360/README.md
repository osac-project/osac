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
