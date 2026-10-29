# Projects E2E Tests

This directory contains end-to-end tests for the Projects API.

## Test Coverage

### test_project_e2e.py

Full lifecycle test that validates:

1. **Project Creation**: Tenant admin creates a parent project
2. **Keycloak Synchronization**: Project appears as a group in Keycloak organization
3. **Project Membership**: Assign viewer role to tenant user via ProjectMembership
4. **Access Control**: Verify tenant user can view project, non-member cannot
5. **Sub-projects**: Create child project under parent project
6. **Parent-Child Relationship**: Verify hierarchy is maintained
7. **Tenant Isolation**: Verify users from other tenants cannot access projects
8. **Deletion Constraints**: Parent project with children cannot be deleted
9. **Cascade Cleanup**: Child deletion removes from both gRPC and Keycloak
10. **Complete Cleanup**: Parent deletion after children are removed

`test_project_metadata_api_privacy_and_filtering` and
`test_project_metadata_rest_event_and_cli_compatibility` reuse the same
Keycloak-backed Project fixture. Together they check private metadata writes,
public gRPC and REST projection, public list and event filters, a real public
event, ordinary REST and CLI edits, mixed reserved-key rejection, optional
locking, and concurrent private edits. The API test validates
the checked-in inventory of 35 persistent resource types (32 public,
3 private-only); runtime metadata operations use Project so the tests do not
provision compute or cluster providers.

Run the metadata journey with:

```bash
uv run pytest tests/e2e/projects/test_project_e2e.py -k metadata
```

## Dependencies

- **Keycloak**: Must be accessible for organization/group verification
- **Multiple Tenants**: tenant1 and tenant2 must exist
- **JWT Authentication**: tenant1_admin, tenant1_user, tenant2_admin, tenant2_user must be configured
- **Environment Variables**:
  - `OSAC_KEYCLOAK_URL`: Keycloak base URL
  - `OSAC_KEYCLOAK_ADMIN_PASSWORD`: Keycloak admin password (default: "admin")
  - `OSAC_JWT_PASSWORD`: Password for JWT test users (default: "foobar")
  - `OSAC_SKIP_KEYCLOAK_SYNC`: Set to "true" to skip Keycloak sync verification (default: "false"). Useful when the project sync feature isn't deployed yet.

The metadata journey uses real `tenant1_admin` and `tenant1_user` JWT identities
plus the existing private service-account credential. Public/private
Fulfillment APIs, the REST gateway, PostgreSQL, Keycloak, and Kafka-backed
Events are real dependencies. The project fixture removes the membership and
child project before the parent project, including after a test failure.
Failure messages omit protected metadata values.

Reserved-metadata privacy depends on server-side projection and filter checks.
Older servers keep their previous visibility behavior; existing API and CLI
clients retain their request formats but cannot provide privacy when pointed
at a server that does not implement the protection. Run this test only against
a feature-capable Fulfillment server.
