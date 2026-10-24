# Testplan — OSAC-1111

## Overview

- **Feature:** OSAC-1111 — DiskImage resource for disk image metadata management
- **Total test cases:** 4
- **Requirements covered:** FR-1, FR-7, NFR-1

## Test Cases

### FR-1: DiskImage CRUD (create, list, get, update, delete) via UI, CLI, API

#### TC-FR1-01: Create DiskImage with required fields

| Story | AC | Priority | Automation |
|-------|-----|----------|------------|
| OSAC-3718 | AC-1, AC-2 | critical | automated |

##### Preconditions

- Authenticated as Cloud Provider Admin
- fulfillment-service running

##### Steps

1. Call `DiskImages/Create` with source_type=REGISTRY and a valid source_ref
2. Verify response contains system-generated id and metadata

##### Expected Results

- Response status 200 OK
- Response contains DiskImage with non-empty `id`

#### TC-FR1-02: Delete unreferenced DiskImage

| Story | AC | Priority | Automation |
|-------|-----|----------|------------|
| OSAC-3718 | AC-5 | high | manual |

##### Preconditions

- DiskImage exists with no referencing ComputeInstances

##### Steps

1. Call `DiskImages/Delete` with the DiskImage ID

##### Expected Results

- Subsequent Get returns NotFound

### FR-7 (continued): OBSOLETE and DEPRECATED DiskImage behavior on ComputeInstance creation

#### TC-FR7-04: OBSOLETE DiskImage blocks ComputeInstance creation

| Story | AC | Priority | Automation |
|-------|-----|----------|------------|
| OSAC-3723 | AC-4 | critical | automated |

##### Steps

1. Create ComputeInstance with spec.disk_image referencing the OBSOLETE DiskImage

##### Expected Results

- FailedPrecondition error: "cannot create compute instance: disk image is obsolete"

### FR-13: Obsolete images hidden from default list, available via filter

#### TC-FR7-06: Cross-tenant DiskImage reference rejected on ComputeInstance creation

| Story | AC | Priority | Automation |
|-------|-----|----------|------------|
| OSAC-3723 | AC-3 | critical | automated |

##### Steps

1. Create ComputeInstance in Tenant B referencing Tenant A's DiskImage

##### Expected Results

- NotFound error

### NFR-1: Display name and description inherited from shared Metadata

No standalone test case — dependent on OSAC-2921. Verified while inspecting FR-1 responses.

## Gaps

- **FR-14 (UI views):** Out of scope for this decomposition.
