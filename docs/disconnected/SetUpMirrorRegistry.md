# Mirror Registry Setup

This document describes the Container Images required to present in the Openshift Mirror Registry for the OSAC installation to succeed in disconnected environment.

| Item | Value |
|------|-------|
| Static IP | `<BASTION_IP>` |
| Gateway | `<GATEWAY_IP>` |
| DNS Server | `<DNS_SERVER_IP>` |
| External exposure | NodePort Service |
| Network interface | Default pod network (masquerade) |
| MAC address | OCP-Virt assigned |
| Openshift version |  4.22.6 |

Operators
Per-operator review:

|  |Operator | Package Name              |      Channel       |      Notes |
|--|------------------|-----------------------------|---------------- |-------------------------
|1 | OpenShift Virtualization (CNV) | kubevirt-hyperconverged    |     stable        | Required |
|2 | LVMS (LVM Storage)   |          lvms-operator    |               stable-4.22     | Required |
|3 | MetalLB             |           metallb-operator  |              stable     | Required |
|4 | AAP              |              dev-ansible-automation-platform |stable-2.6-cluster-scoped | Required |
|5 |  cert-manager      |             openshift-cert-manager-operator |stable-v1   | Required |
|6 |  Kafka (AMQ Streams)      |      amq-streams         |           stable    | Required |
|7 |  Multicluster Engine (MCE)     | multicluster-engine     |        (all channels)   |   Not required for Core/VMaaS |

## Disconnected SNO installation
### Mirrored registry[^1]

###### Assumptions
Bastion VM and a CentOS Stream 9  VM having podman based red hat quay registry with sqllite db for dev setup ( source: registry.redhat.io/quay/quay-rhel8:v3.12.18)

> **Note:** Replace `<MIRROR_REGISTRY>` with your mirror registry hostname and port (e.g. `myregistry.example.com:8443`).

#### Authentication prerequisites

Before running `oc-mirror`, ensure your pull-secret file contains credentials for
both the source and destination registries:

- **Source registry:** Download your pull secret from the
  [Red Hat Hybrid Cloud Console](https://console.redhat.com/openshift/install/pull-secret)
  (includes `registry.redhat.io` and `quay.io` access).
- **Mirror registry:** Add credentials for your mirror registry to the same file.

`oc-mirror` reads credentials from `~/.docker/config.json` or
`$XDG_RUNTIME_DIR/containers/auth.json`. Merge both sets of credentials into
one of these files before proceeding.

#### Openshift Release image

Use oc-mirror v2 (the default in OCP 4.22) with the ImageSetConfiguration shown in the [Operators section](#openshift-operators) to mirror both the release and operator images in a single pass.

To mirror the release image separately, create an ImageSetConfiguration that includes only the platform section:

```yaml
apiVersion: mirror.openshift.io/v2alpha1
kind: ImageSetConfiguration
archiveSize: 8
mirror:
  platform:
    architectures:
      - amd64
    channels:
      - name: stable-4.22
        minVersion: 4.22.6
        maxVersion: 4.22.6
```

Then run:

```bash
oc-mirror --config=release-imageset-config.yaml \
  --workspace file://$(pwd)/oc-mirror-workspace \
  docker://<MIRROR_REGISTRY> \
  --parallel-images 1 \
  --parallel-layers 1 \
  --v2
```

The generated cluster resources (IDMS/ITMS) replace the legacy `imageContentSources` and `ImageContentSourcePolicy`.

<details>
<summary>Deprecated: oc adm release mirror (use oc-mirror v2 instead)</summary>

> `ImageContentSourcePolicy` has been deprecated since OCP 4.14 (replaced by
> `ImageDigestMirrorSet`/`ImageTagMirrorSet`). `oc adm release mirror` has been
> deprecated since OCP 4.22. Both are planned for removal in a future release.
> While they still work on OCP 4.22, oc-mirror v2 (above) is the recommended
> approach for all new deployments.

If your mirror registry uses a self-signed CA, add the CA bundle to the
system trust store before running the mirror command:

```bash
sudo cp <your-ca.crt> /etc/pki/ca-trust/source/anchors/
sudo update-ca-trust
```

Alternatively, pass `--certificate-authority=<path-to-ca.crt>` to the
command below.

```bash
# Mirror the images serially to avoid 500 Internal Server error
GODEBUG=http2client=0 oc adm release mirror \
  --max-per-registry=1 \
  --from="quay.io/openshift-release-dev/ocp-release:4.22.6-x86_64" \
  --to="<MIRROR_REGISTRY>/openshift/release-images" \
  --to-release-image="<MIRROR_REGISTRY>/openshift/release-images:4.22.6-x86_64"
```
Expected output is the update image, mirror prefix, imageContentSources, and ImageContentSourcePolicy:

```
Success
Update image:<MIRROR_REGISTRY>/openshift/release-images:4.22.6-x86_64
Mirror prefix: <MIRROR_REGISTRY>/openshift/release-images
Mirror prefix: <MIRROR_REGISTRY>/openshift/release-images:4.22.6-x86_64
```

To use the new mirrored repository to install, add the following section to the install-config.yaml:

```yaml
imageContentSources:
- mirrors:
  - <MIRROR_REGISTRY>/openshift/release-images
  source: quay.io/openshift-release-dev/ocp-release
- mirrors:
  - <MIRROR_REGISTRY>/openshift/release-images
  source: quay.io/openshift-release-dev/ocp-v4.0-art-dev
```

To use the new mirrored repository for upgrades, use the following to create an ImageContentSourcePolicy:

```yaml
apiVersion: operator.openshift.io/v1alpha1
kind: ImageContentSourcePolicy
metadata:
  name: example
spec:
  repositoryDigestMirrors:
  - mirrors:
    - <MIRROR_REGISTRY>/openshift/release-images
    source: quay.io/openshift-release-dev/ocp-release
  - mirrors:
    - <MIRROR_REGISTRY>/openshift/release-images
    source: quay.io/openshift-release-dev/ocp-v4.0-art-dev
```

</details>

[^1]: https://docs.redhat.com/en/documentation/openshift_container_platform/4.22/html/disconnected_environments/installing-mirroring-installation-images

#### Openshift Operators
Populate the registry with operators for  OSAC core and vmaas profile in OSAC
```yaml
apiVersion: mirror.openshift.io/v2alpha1
kind: ImageSetConfiguration
archiveSize: 8
mirror:
  platform:
    architectures:
      - amd64
    channels:
      - name: stable-4.22
        minVersion: 4.22.6
        maxVersion: 4.22.6
  operators:
    - catalog: registry.redhat.io/redhat/redhat-operator-index:v4.22
      packages:
        - name: kubevirt-hyperconverged
          channels:
            - name: stable
        - name: lvms-operator
          channels:
            - name: stable-4.22
        - name: metallb-operator
          channels:
            - name: stable
        - name: dev-ansible-automation-platform
          channels:
            - name: stable-2.6-cluster-scoped
        - name: openshift-cert-manager-operator
          channels:
            - name: stable-v1
        - name: amq-streams
          channels:
            - name: stable
```
Initiate the mirroring
```bash
oc-mirror --config=imageset-config.yaml \
--workspace file://$(pwd)/oc-mirror-workspace \
  docker://<MIRROR_REGISTRY> \
  --parallel-images 1 \
  --parallel-layers 1 \
  --v2
```

> **TLS verification:** oc-mirror verifies TLS certificates by default. If your
> mirror registry uses a self-signed CA, add the CA bundle to the system trust
> store before running oc-mirror:
> ```bash
> sudo cp <your-ca.crt> /etc/pki/ca-trust/source/anchors/
> sudo update-ca-trust
> ```
>
> **Installer trust:** When your mirror registry uses a self-signed CA, you must
> also add the CA certificate to `install-config.yaml` so the OpenShift installer
> and cluster nodes can pull images from the mirror:
> ```yaml
> additionalTrustBundle: |
>   -----BEGIN CERTIFICATE-----
>   <base-64-encoded CA certificate>
>   -----END CERTIFICATE-----
> ```

Approximate time: 30 minutes
Mirroring failures around blob copy failure must be resolved by rerunning the above command.

=== Results ===
 ✓  192 / 192 release images mirrored successfully
 ✓  91 / 91 operator images mirrored successfully
Pinned configurations written to the workspace directory( see oc-mirror command for the path )
 - ISC( ImageSetConfiguration )
 - DISC( DeleteImageSetConfiguration )

Cluster resources
- IDMS file  ( filename: idms-oc-mirror.yaml contains Openshift Release and Openshift Catalog. The resource `ImageDigestMirrorSet` mapping source image registries (e.g., `registry.redhat.io`) to your local mirror (`<MIRROR_REGISTRY>`). )
- ITMS file ( filename: itms-oc-mirror.yaml contains `ImageTagMirrorSet` for tag-based workloads.)
- CatalogSource file ( Filename: cs-redhat-operator-index-v4-22.yaml: CatalogSource object that exposes the mirrored Operator catalog to Operator Lifecycle Manager (OLM).)
- ClusterCatalog file ( Filename: cc-redhat-operator-index-v4-22.yaml. It provides  File-Based Catalog (FBC) data available to the cluster )

After the cluster is running, apply these resources so the cluster can resolve
mirrored images and access the operator catalog:

```bash
oc apply -f <workspace>/working-dir/cluster-resources/
```

> **Note:** For new installations, the IDMS mirror mappings are already
> referenced in `install-config.yaml` via `imageDigestSources`. The
> CatalogSource and ClusterCatalog resources must still be applied after the
> cluster is up.

#### OSAC Images
Use skopeo copy to push osac images to the mirror registry. Use this script to inspect the pushed images and validate them by digest.

> **Expected source digests:** Record the digest of each source image before
> mirroring (e.g. `skopeo inspect docker://ghcr.io/osac-project/osac-operator:latest --format '{{.Digest}}'`).
> Pass them to the validation script via the `EXPECTED_DIGESTS` associative
> array so the script can confirm that the mirrored image matches the source.

```bash
#!/bin/bash
MIRROR="<MIRROR_REGISTRY>"

# OSAC component images
IMAGES=(
  "ghcr.io/osac-project/osac-operator:latest"
  "ghcr.io/osac-project/fulfillment-service:main"
  "ghcr.io/osac-project/envoy:v1.33.0"
  "ghcr.io/osac-project/osac-ui:v0.0.5"
  "ghcr.io/osac-project/osac-aap:latest"
  "ghcr.io/osac-project/osac-csi-driver:latest"
  "ghcr.io/osac-project/metering-service:latest"
  "ghcr.io/osac-project/bare-metal-fulfillment-operator:latest"
  "ghcr.io/openbao/openbao:2.6.2"
  "quay.io/openshift/origin-cli:4.20.0"
  "quay.io/containerdisks/fedora:latest"
)

# PostgreSQL (digest-based)
DIGEST_IMAGES=(
  "quay.io/sclorg/postgresql-18-c10s@sha256:6be2c9d855f06fb665257a6b0911676a38d740be7022cc61acee1c99a832b1b2"
)

# Populate expected source digests before running this script.
# Example:
#   declare -A EXPECTED_DIGESTS=(
#     ["osac-project/osac-operator:latest"]="sha256:abc123..."
#     ["osac-project/fulfillment-service:main"]="sha256:def456..."
#   )
declare -A EXPECTED_DIGESTS
# TODO: populate EXPECTED_DIGESTS from your source registry before mirroring

echo "=== Checking Tag-Based Images in Mirror ==="
FAIL=0
for img in "${IMAGES[@]}"; do
  src_path="${img#*/}"
  target="docker://${MIRROR}/${src_path}"

  if mirror_digest=$(skopeo inspect "${target}" --format '{{.Digest}}' 2>/dev/null); then
    expected="${EXPECTED_DIGESTS[${src_path}]:-}"
    if [[ -n "${expected}" && "${mirror_digest}" != "${expected}" ]]; then
      echo " [MISMATCH] ${src_path} (mirror: ${mirror_digest}, expected: ${expected})"
      FAIL=1
    else
      echo " [OK] ${src_path} (Digest: ${mirror_digest})"
    fi
  else
    echo " [MISSING] ${src_path}"
    FAIL=1
  fi
done

echo ""
echo "=== Checking Digest-Based Images in Mirror ==="
for img in "${DIGEST_IMAGES[@]}"; do
  src_path="${img#*/}"
  target="docker://${MIRROR}/${src_path}"

  if mirror_digest=$(skopeo inspect "${target}" --format '{{.Digest}}' 2>/dev/null); then
    # For digest-based images, verify the digest matches the @sha256 reference
    expected_digest="${img##*@}"
    if [[ "${mirror_digest}" != "${expected_digest}" ]]; then
      echo " [MISMATCH] ${src_path} (mirror: ${mirror_digest}, expected: ${expected_digest})"
      FAIL=1
    else
      echo " [OK] ${src_path} (Digest: ${mirror_digest})"
    fi
  else
    echo " [MISSING] ${src_path}"
    FAIL=1
  fi
done

if [[ ${FAIL} -ne 0 ]]; then
  echo ""
  echo "ERROR: Some images are missing or have mismatched digests."
  exit 1
fi
```

Expected Output. Tags and SHAsums will vary.
```
=== Checking Tag-Based Images in Mirror ===
 [OK] osac-project/osac-operator:latest (Digest: sha256:cb73e36d959e74b8739a7598e4f66b3e4ac7e24f3391eb88bf8a380c3a6284cd)
 [OK] osac-project/fulfillment-service:main (Digest: sha256:cece9ee969835c0e6467463cf840b30bf59d7acd4a5447bcbb7864a218f8e76a)
 [OK] osac-project/envoy:v1.33.0 (Digest: sha256:c681cedd8c930397a24f855af1a339d3f0bc4e942a1d4396768bd045fe013f37)
 [OK] osac-project/osac-ui:v0.0.5 (Digest: sha256:87aee616a8eb1ca8c73b4b3b39f2b4ee5b086787e2e84a3b355984f1875013c8)
 [OK] osac-project/osac-aap:latest (Digest: sha256:70914999ca7787d8d11c914399fe4e355326cb5975098e2ebfcfd91d96252266)
 [OK] osac-project/osac-csi-driver:latest (Digest: sha256:96621e1ba03e3d86d8454605d2fdd429cb1612bb850d74ec4a8ec8d2fd10984f)
 [OK] osac-project/metering-service:latest (Digest: sha256:e4183bddc23a240f85bec2d0e945c898753fd12471033d0e55154cfefd3a1acc)
 [OK] osac-project/bare-metal-fulfillment-operator:latest (Digest: sha256:805e312a71f296883c51601fa1976832387d09f994b908e6937c76be6d259491)
 [OK] openbao/openbao:2.6.2 (Digest: sha256:11fd73a2102cda9c55d5d881a8c3210303146a7ec1e8ac76f526e175c6d24641)
 [OK] openshift/origin-cli:4.20.0 (Digest: sha256:ebd858bafa7fe3bf04eda2753d47f74be9608c867f41567cea4af1b1b4189fac)
 [OK] containerdisks/fedora:latest (Digest: sha256:1571d49ee43106e9c7cc4e9f487f6f885ee7c156dc5834fad87ad1e38768b778)

=== Checking Digest-Based Images in Mirror ===
 [OK] sclorg/postgresql-18-c10s@sha256:6be2c9d855f06fb665257a6b0911676a38d740be7022cc61acee1c99a832b1b2 (Digest: sha256:6be2c9d855f06fb665257a6b0911676a38d740be7022cc61acee1c99a832b1b2)
```

# Summary:
For the VMAAS Profile of the OSAC ; Mirrored Registry should have
- OCP 4.22.6
- 6 required operators sourced from one catalog (`redhat-operator-index:v4.22`)
- 8 OSAC Component images
- 3 external dependency images
- 1 digest-based PostGresql image
