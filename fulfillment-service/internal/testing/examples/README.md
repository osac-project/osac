# CRD Examples

This directory contains YAML examples of CRDs used by the testing framework to
verify that CRDs are properly available after installations of components with
_Helm_.

The `waitForCrd` method of the `Kind` type uses these example objects to perform
dry run operations against the Kubernetes API server, ensuring that the CRD is
installed and established, that the API server accepts requests to create
instances and that the admission webhooks are operational.

These files are embedded into the Go binary using `//go:embed` and should contain
realistic, minimal examples that can be successfully validated by the respective
CRD schemas and webhooks.
