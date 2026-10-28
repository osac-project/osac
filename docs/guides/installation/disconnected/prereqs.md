# OSAC Disconnected Installation Prerequisites

> [!IMPORTANT]
> This is Doc 2a for OSAC **0.0.24** on OpenShift **4.22**. Complete the selected-profile [mirroring handoff](README.md) first, then this guide, then [AAP Git source handoff](aap-git-source.md), then [operators and shared infrastructure](operators.md). [installation.md](installation.md) owns the OSAC Helm release.

This guide specifies disconnected inputs and acceptance gates. It does not install operators, shared services, provider operands, or the OSAC release. Use the canonical guides for configuration semantics; use the actual downloaded 0.0.24 chart for schema, defaults, images, and rendered consumers. Do not copy a different release's image inventory or version table.

| Canonical reference | Responsibility retained there |
| --- | --- |
| [Customer installation guide](../customer-install-guide.md) | Phase 2 configuration and platform lifecycle |
| [Helm deployment guide](../helm-deployment-guide.md) | Phase 1 architecture and standard prerequisite concepts |
| [Kafka configuration](../kafka-configuration.md) | Broker, connection, TLS/SASL, ACL, and rotation contracts |
| [Secrets management configuration](../secrets-management-configuration.md) | Vault/OpenBao roles, policies, claims, credentials, and rotation |

The dependency and infrastructure charts remain reference implementations: do not install or render them as customer prerequisites. BMaaS and CaaS provisioning are outside this workflow.

## 1. Execution and input contract

Use a secured administration host with cluster-admin access. Standard OpenShift, OLM, certificate, registry, and Helm operations are assumed knowledge. Prepare approved clients before the disconnected window; service-specific probes need their canonical clients.

Keep values, endpoint identities, discovery documents, renders, credentials, and checkpoints in owner-only private runtime storage outside source control. Disable tracing, history, recording, and command-output collection on every participating host. Discard raw command stdout/stderr at source; export only allowlisted completion metadata. A private owner review, not copied logs, handles failure.

> [!WARNING]
> One administrator owns shared mutations across all hosts. Local file locks do not serialize other hosts. Existing trust and pull-secret updates must preserve unrelated entries and submit server-enforced resourceVersion preconditions. Conflicts require a fresh read, merge, and review; never force ownership or replay stale writes.

<!-- WIP: companion validation toolkit is proposed and unpublished -->

### Companion validation interface (WIP — toolkit unpublished)

> **WIP:** The validation commands below reference a companion toolkit that is proposed but unpublished. Replace this section with the actual toolkit identity, version, and checksum before publication.

**Publication hold:** Do not publish these three installation guides (`prereqs.md`, `aap-git-source.md`, and `operators.md`) for operator use until the approved toolkit executable is actually available, its identity, version, and checksum are pinned, and all documented gates have been validated. WIP/TBF examples are not runnable acceptance gates or permission to bypass them.

Once published, set `OSAC_VALIDATE` to the approved, checksum-verified executable and `PREREQS_INPUTS` to its protected input file.

**WIP contract:** Each `prereqs check <gate>` is non-mutating except explicitly approved temporary service probes. It returns exit 0 with fixed `PASS <gate>` only after all required predicates pass; a nonzero exit emits fixed `FAIL <gate>` and blocks progress. Detailed inputs/results stay private. Missing, stale, ambiguous, or malformed evidence is failure, not a skip. No check creates an operator, approves an InstallPlan, or repairs Doc 1.

| Private input | Required value or source |
| --- | --- |
| `OSAC_CHART_REF`, `OSAC_CHART_VERSION` | `oci://ghcr.io/osac-project/charts/osac`, `0.0.24` |
| `OSAC_NAMESPACE` | `osac`, or the approved consistent release namespace |
| `DOC1_WORKSPACE` | Original oc-mirror workspace; ISC at its recorded authoritative location |
| `MIRROR_HOST` | Exact registry authority including port; never substring matching |
| `MIRROR_CA_FILE`, `MIRROR_AUTH_FILE` | Approved CA path; owner-readable authfile containing only the exact mirror entry and read-only credentials |
| `CATALOG_SOURCE`, `CATALOG_NAMESPACE` | `redhat-operators`, `openshift-marketplace` |
| `VALUES_FILE`, `OSAC_CHART_DIR` | Protected `values-osac.yaml` and exact path to the downloaded/extracted `0.0.24` chart |
| Approved service inputs | Endpoint, namespace, credential/CA references, and owner evidence; no credential bytes in handoffs |

## 2. Consume the mirror handoff

Doc 1 owns one ISC and workspace lineage, registry population, and generated-resource application. Missing inventory, mirror content, mappings, or package visibility returns to Doc 1; do not create replacement resources here.

| Handoff item | OSAC-specific requirement |
| --- | --- |
| `idms-oc-mirror.yaml` | Generated ImageDigestMirrorSet names and exact source-to-mirror rules |
| `itms-oc-mirror.yaml` | Generated ImageTagMirrorSet names and exact tag rules; required by this handoff |
| `cs-redhat-operator-index-v4-22.yaml` | CatalogSource `redhat-operators` in `openshift-marketplace`; exact generated mirror image |
| `cc-redhat-operator-index-v4-22.yaml` | Generated ClusterCatalog identities; use these objects, not any catalog on the cluster |
| `signature-configmap.yaml` | Generated signature identity and Doc 1 managed-signature evidence |
| ISC and workspace | Original configuration, selected inventory, generated files, and incremental lineage |
| Affected pools | Explicit pool names and expected current generations from the change owner |
| VMaaS source selection (when `enable_vmaas=true`) | Nonempty `selected-diskimage-sources.json` with exact selected upstream references and tags; omit for `enable_vmaas=false` |

Check default-source isolation:

```bash
oc get operatorhub cluster -o json | jq -e '.spec.disableAllDefaultSources == true'
```

Expected local result: `true`. An online default source is not a substitute for missing mirror content.

Check complete identity, content, policy, and lineage binding:

<!-- TBF: requires unpublished companion toolkit -->

```bash
"$OSAC_VALIDATE" prereqs check doc1 --inputs "$PREREQS_INPUTS"
```

Expected: `PASS doc1`. Require both mirror-set types, the exact CatalogSource name/namespace/image with `READY`, each generated ClusterCatalog `Serving=True` with reason `Available`, and the generated signature handoff. OLM Classic PackageManifest visibility does not prove ClusterCatalog readiness or vice versa.

Check affected-pool convergence:

<!-- TBF: requires unpublished companion toolkit -->

```bash
"$OSAC_VALIDATE" prereqs check machineconfig --inputs "$PREREQS_INPUTS"
```

Expected: `PASS machineconfig`: every affected pool unpaused, observedGeneration=current generation, Updated=True, updatedMachineCount=machineCount, unavailable/degraded counts zero, and Degraded=False. Creating an IDMS does not drain or reboot nodes; modifying or deleting an IDMS can cause node disruption. ITMS changes can drain nodes. See the [OpenShift 4.22 mirror-set behavior](https://docs.openshift.com/it/documentation/openshift_container_platform/4.22/html/disconnected_environments/updating-a-cluster-in-a-disconnected-environment). Consume the completed rollout; do not reapply policies here.

| Package visibility | Required channel | Selection |
| --- | --- | --- |
| `openshift-cert-manager-operator` | `stable-v1` | Core |
| `ansible-automation-platform-operator` | `stable-2.6-cluster-scoped` | Core |
| `amq-streams` | `stable` | Core package coverage; live provider follows Kafka selection |
| `kubevirt-hyperconverged`, `lvms-operator`, `metallb-operator` | Selected approved Doc 1 channels | Additional VMaaS coverage; package presence does not select installation |

Check the selected packages and channels:

<!-- TBF: requires unpublished companion toolkit -->

```bash
"$OSAC_VALIDATE" prereqs check packages --inputs "$PREREQS_INPUTS"
```

Expected: `PASS packages`. Every selected PackageManifest must match both `status.catalogSource=redhat-operators` and `status.catalogSourceNamespace=openshift-marketplace`. Operator namespaces, scopes, CSV pins, and Manual approval belong to the single product table in `operators.md`, not a second table here.

## 3. Registry access and bounded configuration

| Parameter | Contract |
| --- | --- |
| Mirror DNS | Approved address reachable from administration host and cluster execution contexts |
| Registry TLS | Hostname and chain validation using approved trust; no `-k` or public fallback |
| Node registry probe | Each applicable node reaches `https://<mirror-authority>/v2/`; HTTP `200` or `401` proves transport only |
| `image.config.openshift.io/cluster.spec.additionalTrustedCA.name` | Existing referenced ConfigMap in `openshift-config`; `registry-config` only when no name is configured |
| CA ConfigMap key | Registry hostname; replace port separator `:` with `..`, for example `mirror.example.com..8443` |
| `openshift-config/pull-secret` | `.dockerconfigjson` includes approved mirror-read credentials; preserve all unrelated auth entries |
| Credential boundary | Never upload the bastion source-read/mirror-write authfile or substitute an OpenShift API token |

Check actual transport and credential usability:

<!-- TBF: requires unpublished companion toolkit -->

```bash
"$OSAC_VALIDATE" prereqs check registry --inputs "$PREREQS_INPUTS"
```

Expected: `PASS registry`: approved DNS/TLS in the required contexts, exact mirror authority, credential validation and representative mirrored manifests. A key's presence, PEM marker, reachable host, or HTTP challenge alone is insufficient. Include catalog, OSAC operator, Fulfillment Service, and AAP EE from the selected release inventory.

If trust or pull credentials need correction, have the cluster owner perform the standard reviewed merge procedure using the parameters above. This is the only shared-cluster configuration repair owned here. Preserve existing CA/auth entries; creation must not overwrite a concurrently created object. Protect the image-config reference update against conflicting changes as well as the ConfigMap/Secret update. Recheck current state and affected pools afterward.

<!-- TBF: requires unpublished companion toolkit -->

```bash
"$OSAC_VALIDATE" prereqs check registry --inputs "$PREREQS_INPUTS"
```

Expected: `PASS registry` after correction. Missing manifest/blob/repository or failed mirror redirection returns to Doc 1; DNS, trust, authentication, or local trust-rollout failures remain with this prerequisite owner.

## 4. Select providers and record production contracts

These are **installation selections**, not seven invented Helm values. Persist the original names for the downstream consumer; parse the selection file as data, never source it into a shell.

| Selection field | Allowed value and chart relationship |
| --- | --- |
| `profile` | `core` or `core-vmaas`; no independent Helm key |
| `enable_vmaas` | `false` or `true`; must equal `global.services.vmaas.enabled` |
| `with_lvms` | Explicit boolean; true installs LVMS later, only with VMaaS; not an `osac` chart key |
| `with_metallb` | Explicit boolean; true installs MetalLB later, only with VMaaS; not an `osac` chart key |
| `kafka_provider` | `external` or `in-cluster`; `kafka.enabled` false or true respectively |
| `storage_class` | Approved dynamic class; not a global per-PVC Helm override |
| `LOADBALANCER_PROVIDER` | `existing` with `with_metallb=false`, or `metallb` with `with_metallb=true`; protected site input |

`core` requires `enable_vmaas=false`; `core-vmaas` requires true. Set `global.services.caas.enabled=false`, `global.services.bmaas.enabled=false` and `global.services.maas.enabled=false` for both profiles in this workflow. Mirroring optional packages does not authorize consuming disks or installing another load-balancer provider.

| Production dependency | Required private contract and readiness |
| --- | --- |
| PostgreSQL | Approved host/port/database, TLS CA and client identity, backup/restore owner evidence; authenticated temporary-table test returns count `1` |
| Database URL | Released shape: `postgres://<user>@<host>:5432/<database>?sslmode=verify-full&sslrootcert=<mounted-ca>&sslcert=<mounted-cert>&sslkey=<mounted-key>`; actual paths from render |
| Database consumers | `dbInit.host`, `service.database.connection[]`; URL Secret key `url`; exact mounted keys/names come from rendered consumers |
| External Kafka | Bootstrap **and advertised broker** addresses, canonical TLS/SASL/ACL completion, credential and CA references; `service.kafka.connection[]` and `service.certs.caBundle` |
| In-cluster Kafka | Intended `osac-kafka` namespace/cluster, storage selection and mirror-resident operand images; broker/user/ACL checks deferred to `operators.md`, never reported passed early |
| Vault/OpenBao | Approved HTTPS `service.vault.endpoint`, namespace/mount/role/issuer/audience and Secret/CA references from the canonical secrets guide |
| Production identity | Approved exact HTTPS issuer/hosts; discovery issuer equality, authorization/token/JWKS endpoints and nonempty signing keys; approved client/role evidence |
| Dynamic storage | Existing provider: actual bound PVC **and consuming workload write/read**; LVMS selected: device approval now, live provider test in `operators.md` |
| LoadBalancer | Existing provider: allocation plus client traffic from required networks; MetalLB selected: approved non-overlapping pool now, live test in `operators.md` |

No passwords, tokens, private keys, or credential-bearing URLs belong in selection/completion files. Endpoint identities stay private. Verify expected Secret **keys**, not merely nonempty Secret data. Do not pre-create Phase 2 chart-owned database URL Secrets or Certificate outputs; external PostgreSQL does not disable those chart writers or the db-init contract.

Check selection consistency and applicable production contracts:

<!-- TBF: requires unpublished companion toolkit -->

```bash
"$OSAC_VALIDATE" prereqs check providers --inputs "$PREREQS_INPUTS"
```

Expected: `PASS providers`. Only the explicit in-cluster Kafka/LVMS/MetalLB live gates may be deferred to `operators.md`; external dependencies must already pass their canonical contracts. Deferred is not ready, and file existence alone is not owner verification. Storage Pending/WaitForFirstConsumer is not successful provisioning.

Check identity without sending credentials to unapproved endpoints:

<!-- TBF: requires unpublished companion toolkit -->

```bash
"$OSAC_VALIDATE" prereqs check identity --inputs "$PREREQS_INPUTS"
```

Expected: `PASS identity`. Approve HTTPS scheme and exact hosts before discovery/JWKS requests or credential POST; reject userinfo, unexpected query/fragment and redirects. Match the same approved issuer across the OSAC clients and Vault contract. Missing roles/claims return to the identity/service owner, not a demonstration fixture.

## 5. Prepare the release chart and values

The chart-delivery path is a controlled connected workstation/bastion with approved tunnel/VPN access to the disconnected API. Pull the published OCI chart there; do not use a repository checkout as the release artifact, push the chart into Quay Mirror, or assume oc-mirror v2 mirrors it. Cluster workloads must not fetch public registries or GitHub. Use a fresh chart staging directory; verify/adopt an existing approved chart instead of overwriting it on retry.

```bash
helm pull oci://ghcr.io/osac-project/charts/osac --version 0.0.24 --untar --untardir .
```

Expected: successful pull and extracted `osac/` chart. Registry login is not required for this public chart.

```bash
yq -e '.version == "0.0.24"' osac/Chart.yaml
```

Expected local result: `true`. **Chart acceptance:** Before rendering or handoff, require successful signature verification of the pulled chart's exact OCI digest and approved OSAC publisher identity per the [repository chart-signature guidance](https://github.com/osac-project/osac/blob/37a47700819b4a1e4e51057dd503630f05a426e2/README.md). Retain the verified digest/result in existing private integrity state, bound to the complete chart/dependency contents actually used. A version match or local checksum alone is insufficient; failed verification or changed/unbound chart contents block use.

```bash
helm show values ./osac
```

Expected local result: released defaults. Initialize the protected `VALUES_FILE` only when absent, using a no-clobber workflow; never truncate an existing approved file. Edit values through the canonical contracts; subsequent corrections modify values, not rendered manifests.

| Values delta | OSAC 0.0.24 requirement |
| --- | --- |
| `cliImage`, `aap.bootstrap.cliImage` | Merge `{cliImage: "<approved-mirror-cli-ref>", aap: {bootstrap: {cliImage: "<approved-mirror-cli-ref>"}}}` into protected `$VALUES_FILE`, preserving other `aap` keys; these independent settings otherwise retain Origin `4.20.0` defaults. Do not rely on a root-only or transient `--set` override. |
| CLI-specific acceptance before Helm (WIP) | Existing `prereqs check render` must verify each rendered CLI consumer and exact mirror lineage/availability; `prereqs check registry` must cover the selected image with a fresh cluster pull and non-mutating required-tool smoke test in an approved existing probe context. Pull success alone does not prove script compatibility. |
| CLI failure | Unpullable images block hook startup; missing/incompatible tools fail bootstrap/token scripts and can block or time out Helm hooks. Stop on failed preflight, correct approved values or the mirror/tool contract, then rerun both gates; do not bypass hooks. |
| `aap.configAsCode.eeImage`, component images | Exact selected-chart references plus proven mirrored content; never derive component versions from umbrella version |
| `aap.configAsCode.projectGitUri`, `projectGitBranch` | [aap-git-source.md](aap-git-source.md) supplies the internal full-monorepo handoff and exact commit from the actual chart; mirroring the EE does not deliver Git content |
| `service.database.connection[]`, `service.kafka.connection[]`, `service.vault.*` | Preserve canonical structured sources, Secret keys, CA references and rendered ownership |
| `service.auth.controllerCredentials[]`, `service.idp.credentials[]`, `ui.auth.serviceAccount.credentialsSecret` | Governed per-consumer identity references; no invented operator OIDC credential array |
| Storage | Canonical default StorageClass behavior; explicit selection does not imply every PVC consumes a new `storage_class` value |
| Image redirection | Retain released upstream references where correct IDMS/ITMS applies; no `global.imageRegistry` key exists |

```bash
helm lint ./osac --values "$VALUES_FILE"
```

Expected: no lint errors.

```bash
helm template osac ./osac --namespace "$OSAC_NAMESPACE" --values "$VALUES_FILE" > rendered-osac.yaml
```

Expected: protected nonempty render. Do not require global server-side dry-run: operator APIs/webhooks do not yet exist, and Phase 2 installs additional CRDs.

<!-- TBF: requires unpublished companion toolkit -->

```bash
"$OSAC_VALIDATE" prereqs check render --inputs "$PREREQS_INPUTS"
```

Expected: `PASS render`: no unresolved placeholders or embedded credentials; actual consumers match selected services; every exact image has archived inventory membership, correct tag/ITMS or digest/IDMS mapping, specificity-resolved policy, exact mirror authority and available destination manifest. VMaaS selected sources require effective `NeverContactSource`; the separate CDI import path remains a later VMaaS acceptance gate. A broad registry prefix is not image availability proof. Any direct-mirror override, including CLI, needs its own exact lineage proof.

## 6. Handoff and worked flow

<!-- TBF: requires unpublished companion toolkit -->

```bash
"$OSAC_VALIDATE" prereqs complete --inputs "$PREREQS_INPUTS"
```

Expected: `PASS complete` only after all applicable checks and change-owner review. Stage a consistent private record set, then atomically publish its final generation marker; failure/interruption must leave the last valid generation or no completion, never partial success. Recheck live state at the next boundary; a past record is not continuing readiness or authorization.

| Artifact | Consumer and boundary |
| --- | --- |
| `prerequisite-selection.env` | Protected, data-only selections; retain explicit fields and consistent approved inputs |
| `prereqs-completion.txt` | Private `profile`, `enable_vmaas`, `with_lvms`, `with_metallb`, `kafka_provider`, `storage_class`, `chart_version=0.0.24`, original `values_sha256` |
| `prereqs-artifacts.sha256` and private input state | Bind original values/render, complete chart, protected `OSAC_CHART_DIR` path (consume unchanged in `aap-git-source.md`), Doc 1 lineage, provider evidence and resource identities; available for owner verification/resume, not audit export |
| Fresh prerequisite audit directory | Only schema-versioned allowlisted outcomes, timestamp, release/selections and approved configuration checksums; never endpoints, raw logs, source paths, credential-derived hashes or private input files |

For Core + VMaaS with enterprise storage/network and external Kafka, select `core-vmaas`, `enable_vmaas=true`, both `with_*` fields false, the approved storage class, and `kafka_provider=external`. The administrator sees each fixed gate result and retains exact private bindings; the record agrees with values before `operators.md` begins.

If the workload PVC cannot bind or write, `providers` fails and no completion record is published. The storage owner corrects the existing provider, or approves an explicit with-LVMS selection change and its deferred live test. Regenerate affected selection/render checks; never install both providers opportunistically. If a rendered image is missing, return to Doc 1's same lineage rather than substituting a public image. If another administrator changes trust/pull credentials, the stale write must fail and be re-reviewed.

Remove only this run's approved temporary objects using recorded UID/ownership preconditions; never delete a shared namespace or adopted resource. Retain private runtime inputs through Phase 2 and the approved retry window, then dispose under the site's secret-media policy. Continue to [AAP Git source handoff](aap-git-source.md) with the reviewed prerequisite handoff, then to [operators and shared infrastructure](operators.md) with its validated Git handoff; deliberate later values edits preserve the original prerequisite digest and require a new approved checkpoint, not an overwritten past record.

### Secret-key acceptance reference

| Consumer | Required released key contract |
| --- | --- |
| Cluster registry authentication | `openshift-config/pull-secret`: `.dockerconfigjson`; exact mirror entry validated privately |
| PostgreSQL connection | URL source key `url`; client TLS mount keys and producer ownership resolved from the actual render |
| Default Kafka connection | `fulfillment-service-kafka`: `brokers`, `user`, `password`; external sources may remap keys through the canonical structured connection contract |
| Distributed trust | `ca-bundle` ConfigMap key `bundle.pem`; full approved certificate-set verification belongs to `operators.md` |
| AAP license | Selected manifest Secret key `license.zip`; prepared/verified in `operators.md`, not a prerequisite to catalog visibility |

This table describes consumers, not permission to create their inputs now. Final in-cluster Kafka credentials and database Certificate/Secret outputs remain Phase 2-owned; adopt or prepare only the separately governed Phase 1 inputs identified by the rendered ownership contract.
