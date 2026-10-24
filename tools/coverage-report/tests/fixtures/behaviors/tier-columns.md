# Testplan — OSAC-3333

## Test Cases

### FR-2: Networking lifecycle

#### TC-FR2-01: Provision and remove the full supported networking resource set

| Story | AC | Priority | Tier | Boundary | Automation |
|-------|-----|----------|------|----------|------------|
| OSAC-5000 | AC-1 | critical | e2e | real, simulated | automated |

##### Expected Results

- Each created resource reaches the public state exposed by its API.

#### TC-FR2-02: Reject an unsupported in-place network edit

| Story | AC | Priority | Tier | Automation |
|-------|-----|----------|------|------------|
| OSAC-5000 | AC-2 | medium | integration | automated |

##### Expected Results

- The edit is rejected without a mutation.
