# Testplan — OSAC-2222

## Overview

- **Feature:** OSAC-2222 — BMaaS Provisioning Progress and Step Visibility
- **Total test cases:** 3

## Test Cases

### FR-1: Step-level current phase, state, and human-readable message

#### TC-FR1-01: API and detail view advance the current stage through provisioning

| Interface Change | Priority | Automation |
|---|---|---|
| IC-1, IC-2, IC-4 | critical | automated |

##### Preconditions

- A bare-metal instance exists with its hub `BareMetalInstance` CR.

##### Steps

1. Reconcile the instance before `Allocated=True`.
2. Advance the lifecycle conditions to true, syncing the fulfillment status.

##### Expected Results

- The stage message is non-empty and curated.

#### TC-FR1-02: Detail view renders the four fixed steps and their state categories

| Interface Change | Priority | Automation |
|---|---|---|
| IC-4 | high | automated |

##### Expected Results

- The labels appear in this exact order: Host Allocation, Provisioning, Network Setup, Ready.

### NFR-3: Exhaustive condition-to-stage contract and fixed condition presentation

#### TC-NFR3-01: Every exported lifecycle condition is classified by the derivation contract

| Interface Change | Priority | Automation |
|---|---|---|
| IC-6 | critical | automated |

##### Expected Results

- Every exported condition appears exactly once in one classification.

## Gaps

None.
