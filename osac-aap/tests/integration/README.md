# OSAC Workflows Integration Tests

Automated test suite for all 11 workflows in the osac.workflows collection.

**Note**: Tests must be run from the repository root directory using `make test` or the scripts must be called with their full paths.

## Running Tests

```bash
make test
```

This creates a kind cluster, installs CRDs, runs all tests, and cleans up.

## Current Status: 10/14 Tests Passing

### ✅ Passing Tests (10)

**cluster_create** (1/2)
- ✓ Baseline - Workflow executes, variables extracted, locks acquired

**cluster_delete** (2/2)
- ✓ Baseline - Delete workflow executes correctly
- ✓ Override - Workflow hooks and overrides execute correctly

**cluster_post_install** (1/2)
- ✓ Baseline - Post-install workflow executes correctly

**compute_instance_create** (1/2)
- ✓ Baseline - VM creation workflow executes, variables extracted

**compute_instance_delete** (1/2)
- ✓ Baseline - VM deletion workflow executes correctly

**cluster_status_reporting** (2/2)
- ✓ Baseline - Reports workflow status to ClusterOrder CRD
- ✓ Override - Workflow hooks execute correctly

**maintenance_cleanup** (2/2)
- ✓ Baseline - Executes maintenance workflow (noop by default)
- ✓ Override - Maintenance step override works correctly

### ❌ Failing Tests (8)

**Override tests** (4 tests) - Need template-level hooks
- cluster_create:override - Missing modify_hosted_cluster, modify_nodepool hooks in noop template
- cluster_post_install:override - Missing post-install hooks in noop template
- compute_instance_create:override - Missing VM creation hooks in noop template
- compute_instance_delete:override - Missing VM deletion hooks in noop template

**External infrastructure required** (4 tests)
- config_as_code (baseline + override) - Requires Ansible Automation Platform instance

## Test Architecture

Each workflow has 2 tests:

1. **Baseline** - Validates default workflow behavior
   - Variables extracted correctly
   - Critical steps execute
   - Expected Kubernetes resources created

2. **Override** - Validates extension point functionality
   - All hooks can be overridden
   - Override execution verified via log file
   - Workflow functions correctly with overrides

## Override Coverage

- **Total Extension Points**: 44
  - Workflow-level: 33 (hook_workflow_start, hook_workflow_complete, etc.)
  - Template-level: 11 (VM creation steps, template_id_override, etc.)

- **Test Collection**: osac.test_overrides
  - 20 override roles exercise all 44 extension points
  - Logs each override execution to `/tmp/osac_test_overrides.log`
  - Delegates to original roles to maintain workflow integrity

## Future Improvements

To get remaining 15 tests passing:

1. Install KubeVirt operator in kind cluster
2. Mock coordination lease creation
3. Install RHACM CRDs
4. Mock OpenStack SDK operations
5. Mock AAP API calls

Alternatively, run tests in a full OSAC environment with all infrastructure.
