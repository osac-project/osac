# Disconnected Installation

Deploy OSAC on a disconnected (air-gapped) OpenShift cluster that has no
direct access to external registries.
This document describes the custom values used by the osac-deps & osac-infra helm charts for disconnected environments.
The source of truth will be the docs for connected environment. Exceptions for disconnected environments will be documented here.
 1. steps for consuming the Operators to a local registry and configuring OSAC to use the mirrored catalog.
## Prerequisites
 The following setup is expected to be completed before installing OSAC deps helm chart in a disconnected environment.
 General Overview of the mirroring process in Openshift 4.22
```
 Mirrored catalog image
          │
          ├── CatalogSource / ClusterCatalog
                 └── OLM discovers packages and channels
                    └── Subscription
                  └── InstallPlan
                          └── CSV and operator deployment
                                 │
                                  └── operator/operand image pulls
                                          └── ITMS/IDMS redirects to mirror
```
 Tested Method: `oc mirror --v2` for the version OCP 4.22
## Required Operator Packages

Security Exception: Ensure the mirrored catalog sets the maxVersion of AAP to aap-operator.v2.6.0-0.1787258256, matching the startingCSV in the AAP Subscription template.
Look in the docs/ for the list of required operator packages and channels.
As a OSAC Cloud Administrator, you will need to ensure the the following resources are healthy
'ImageDigestMirrorSet', 'ImageTagMirrorSet. These Subscriptions consume a CatalogSource.If the installation uses OLMv1, ClusterCatalog is required too. Read more at [docs.redhat.com](https://docs.redhat.com/pt-br/documentation/openshift_container_platform/4.22/html/disconnected_environments/about-installing-oc-mirror-v2)



## Configuring OSAC for a Mirrored Catalog

The osac-deps chart uses two values to locate the OLM CatalogSource:

| Value | Default | Description |
|-------|---------|-------------|
| `catalogSource` | `redhat-operators` | CatalogSource name for operator Subscriptions |
| `catalogSourceNamespace` | `openshift-marketplace` | Namespace where the CatalogSource exists |

If your mirrored CatalogSource uses a different name or namespace, override
these values in your profile's `infra.yaml`:

```yaml
# values/<profile>/infra.yaml
catalogSource: my-mirror-redhat-operators
catalogSourceNamespace: mandai
```

Then install/upgrade the helm chart for osac-deps as documented in the docs/

All operator Subscriptions (cert-manager, AAP, CNV, MetalLB, MCE, LVMS,
AMQ Streams) will reference the specified CatalogSource instead of the
default `redhat-operators`.
## TODO: Test and verify the following instructions for CAAS in disconnected environments
## CaaS Provisioned Clusters

#CaaS-provisioned clusters install additional operators via AAP cluster
#templates (RHOAI, Node Feature Discovery). These use the same
#`redhat-operators` CatalogSource on the provisioned cluster. Ensure the
#target cluster's mirrored catalog includes these packages as well.

## Troubleshooting

### Operator Not Installing

If an operator Subscription is stuck in a pending state:

```bash
oc get sub -A
oc get catalogsource -A
```

Verify the CatalogSource exists, is in a `READY` state, and contains the
required package:

```bash
oc -n mandai get CatalogSource my-mirror-redhat-operators -o jsonpath='{.status.connectionState}' | jq .
oc get packagemanifest -n mandai
```
Hints: Check whether CatalogSource resources( catalogd ) can perform DNS resolution and validate the mirror registry's certificate
### cert-manager Pre-Install Hook Timeout

The OSAC installer waits for cert-manager to be operational before
proceeding. If the mirrored catalog is missing
`openshift-cert-manager-operator`, the hook will time out and the install
will fail.