# Testplan — OSAC-4444

## Test Cases

### FR-1: Complete resource journeys

#### TC-FR1-01: Complete deployed networking and ComputeInstance journeys

| Interface Change | Priority | Automation |
|-----------------|----------|------------|
| IC-3 | critical | automated |

##### Preconditions

- **Tier / owner:** E2E / [QE].
- **Execution:** No runnable command; proposed path: `tests/e2e/mcp/test_complete_journeys.py`.
- **Boundary:** Deployed MCP, Keycloak, Fulfillment, PostgreSQL, and providers run for real.

##### Steps

1. Invoke each typed write tool in dependency order.

#### TC-FR1-02: Retrieve created resources after pod replacement

| Interface Change | Priority | Automation |
|-----------------|----------|------------|
| IC-4 | critical | automated |

##### Preconditions

- **Tier / owner:** Component integration / [DEV].
- **Boundary:** Deployed Fulfillment and PostgreSQL are real. Providers are omitted.

##### Steps

1. Call `get_resource` after reconnecting.
