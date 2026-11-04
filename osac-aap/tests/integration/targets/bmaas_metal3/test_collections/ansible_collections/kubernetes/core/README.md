# Test-only kubernetes.core fixture

This collection replaces only the `k8s`, `k8s_info`, and `k8s_json_patch`
module boundary for the isolated `bm_host_provisioning` role tests. It records
resources and operations in the JSON file named by
`OSAC_BM_HOST_PROVISIONING_STATE`.

The Kind lifecycle scenario does not load this collection.
