# Manual NetApp ONTAP and Trident integration for VMaaS

NetApp integration with OSAC is under development. This temporary guide is
the procedure to use for now. It adds an ONTAP NFS tier to an existing VMaaS
installation and is not part of the standard OSAC install. Complete
[Section 6.1](customer-install-guide.md#61-installing-osac-for-vmaas) of the
[customer install guide](customer-install-guide.md) first.

This guide covers one combination: NetApp ONTAP file storage over Ethernet/IP
(NFS). It does not cover block storage over FC.

> **Manual procedure.** The `osac` release does not enable this path.
>
> This procedure adds a NetApp ONTAP NFS tier to an existing VMaaS
> installation. The `csi-backends` chart supplies the Trident controller when
> that chart is rendered on its own. The chart does not install the Trident
> persistence CRDs or grant the SCCs, and the OSAC node DaemonSet cannot
> mount NFS. Setting `provider: ontap` does not enable the controller. The
> AAP collection has no ONTAP storage role, so the tenant StorageClass is
> created by hand and the first NetApp VM job is relaunched.

This procedure was tested with OpenShift 4.22, ONTAP NFS, and Trident
25.10.0. NetApp's Trident 25.10 requirements list OpenShift 4.12 and
4.14–4.20, so that combination is outside the versions NetApp documents.
Before you use this integration, check the current
[Trident requirements](https://docs.netapp.com/us-en/trident-2510/trident-get-started/requirements.html)
and confirm support for your OpenShift and Trident versions. See also NetApp's
[OpenShift installation guide](https://docs.netapp.com/us-en/trident-2510/trident-install/openshift-operator-deploy.html),
[ONTAP NAS backend preparation](https://docs.netapp.com/us-en/trident-2510/trident-use/ontap-nas-prep.html),
and [backend examples](https://docs.netapp.com/us-en/trident-2510/trident-use/ontap-nas-examples.html).
Do not install a second standalone Trident controller from OperatorHub, and
do not `helm upgrade` the `osac` release to enable Trident. Render only the
`csi-backends` chart, as in step 4.

The OSAC `StorageBackend` and the Trident backend are different objects.
Create them in the order below.

| Object | Role | When |
| --- | --- | --- |
| `StorageBackend` `provider: ontap` | Registers the array in OSAC. Does not install Trident or create a volume. | First, on the private API |
| `StorageTier` `ontap-nas` | NFS tier that a tenant selects on a VM | With the StorageBackend |
| Secret `trident-csi` and the Trident CRDs | TLS for the controller, and the persistence CRDs the chart does not install | Before the controller |
| Deployment `trident-csi-controller` | OSAC vendor controller from the `csi-backends` chart | After the Secret and CRDs |
| DaemonSet `trident-node-linux` | Official Trident node manifest, patched so it can mount NFS | After the controller is Ready |
| Trident backend `ontap-nas-economy` | Tells Trident which SVM and data LIF to use | After every worker mounts the export |
| Tenant and `tenant-admin` | Break-glass user created with the tenant. VMs require `tenant-admin`. | Before the StorageClass |
| StorageClass `ontap-nas-${TENANT}` | Class for that tenant's PVCs | After the Trident backend is online |

**Prerequisites**

- Complete [Section 6.1](customer-install-guide.md#61-installing-osac-for-vmaas) of the customer install guide and verify that a VM
  can use the existing `local` storage tier. Keep that tier. Add the NetApp
  tier alongside it.
- OpenShift Virtualization, OSAC VMaaS, the OSAC CSI driver, and AAP are
  installed. A cluster administrator can create namespaces, Secrets, CRDs,
  and SCCs.
- The storage administrator provides an ONTAP SVM management endpoint, the
  SVM name, an NFS data LIF, a dedicated export and capacity, and an account
  with the Trident permissions NetApp requires. Do not reuse an export or LUN
  that belongs to another workload.
- Keep ONTAP credentials in the approved secret manager or a protected shell
  environment. Do not put credentials, internal addresses, or access details
  in values files, source control, or this guide.
- The examples use `OSAC_DIR` (the `osac` checkout), `OSAC_NAMESPACE`, `TENANT`, `ONTAP_MANAGEMENT_ENDPOINT`
  (bare host, no `https://` prefix), `ONTAP_SVM`, `NFS_DATA_LIF`,
  `NFS_EXPORT_PATH`, `ONTAP_USERNAME`, `ONTAP_PASSWORD`, and
  `GUEST_PASSWORD`.

**Procedure**

1. Register the array in OSAC. Sign in on the private administrator API.
   Trust the route with the CA file from step 11 of
   [Section 7](customer-install-guide.md#7-verifying-the-installation) of the customer install guide. A tenant session cannot create
   a `StorageBackend`. This step does not install Trident.

   ```bash
   INT_ROUTE=$(oc get route fulfillment-internal-api -n "$OSAC_NAMESPACE" \
     -o jsonpath='{.spec.host}')
   osac login --address "$INT_ROUTE" --private \
     --ca-file default-ca.crt \
     --token-script "oc create token -n $OSAC_NAMESPACE admin"
   osac create -f - <<EOF
   "@type": type.googleapis.com/osac.private.v1.StorageBackend
   metadata:
     name: ontap-lab
   spec:
     provider: ontap
     endpoint: "${ONTAP_MANAGEMENT_ENDPOINT}"
     credentials:
       username: "${ONTAP_USERNAME}"
       password: "${ONTAP_PASSWORD}"
   EOF
   BACKEND_ID=$(osac get storagebackend ontap-lab -o json \
     | jq -r '.id // .metadata.id // .spec.id')
   osac create storagetier \
     --name ontap-nas \
     --backend-id "$BACKEND_ID" \
     --protocol NFS
   osac get storagetier
   ```

   Pass a bare host in `endpoint`. `provider: ontap` is the CSI routing key.
   Do not print the credential variables, and do not replace them with
   literal values in the command. Expect tier `ontap-nas` with protocol
   `NFS`, and leave the `local` tier in place. This procedure covers NFS. It
   does not cover block, iSCSI, or FC storage. Setting `provider: ontap` does
   not enable the Trident controller. `provider` cannot be changed after
   create. If it is wrong, delete that `StorageBackend` and create it again.
   `StorageBackendReady` on the tenant can stay False. That does not remove
   the `local` tier.
2. Create the `osac-csi-backends` namespace, copy the cluster pull secret
   into it, and create the `trident-csi` TLS Secret. The controller image
   `registry.connect.redhat.com/netapp/trident:25.10.0` uses that pull secret.
   The Secret keys are the ones listed in
   [`csi-backends/README.md`](https://github.com/osac-project/osac/blob/main/osac-csi-driver/charts/csi-backends/README.md).
   Sign each certificate with its own serial. A second `-CAcreateserial` in
   the same directory skips the client certificate. Give the server
   certificate the SAN `DNS:trident-csi`. The client certificate common name
   must be `trident-node`. The controller accepts that name on its HTTPS REST
   port.

   ```bash
   oc create namespace osac-csi-backends --dry-run=client -o yaml | oc apply -f -
   oc get secret pull-secret -n openshift-config -o json \
     | jq 'del(.metadata.uid,.metadata.resourceVersion,.metadata.creationTimestamp,.metadata.namespace,.metadata.ownerReferences)
           | .metadata.name="pull-secret"
           | .metadata.namespace="osac-csi-backends"' \
     | oc apply -f -
   oc -n osac-csi-backends delete secret trident-csi --ignore-not-found
   TRIDENT_CERT_DIR=$(mktemp -d)
   openssl genrsa -out "$TRIDENT_CERT_DIR/caKey" 2048
   openssl req -new -x509 -key "$TRIDENT_CERT_DIR/caKey" -sha256 -days 3650 \
     -subj '/CN=trident-ca' -out "$TRIDENT_CERT_DIR/caCert"
   openssl genrsa -out "$TRIDENT_CERT_DIR/serverKey" 2048
   openssl req -new -key "$TRIDENT_CERT_DIR/serverKey" -subj '/CN=trident-csi-controller' \
     -out "$TRIDENT_CERT_DIR/server.csr"
   printf 'subjectAltName=DNS:trident-csi\n' > "$TRIDENT_CERT_DIR/server.ext"
   openssl x509 -req -in "$TRIDENT_CERT_DIR/server.csr" -CA "$TRIDENT_CERT_DIR/caCert" \
     -CAkey "$TRIDENT_CERT_DIR/caKey" -set_serial 1 -out "$TRIDENT_CERT_DIR/serverCert" \
     -days 3650 -sha256 -extfile "$TRIDENT_CERT_DIR/server.ext"
   openssl genrsa -out "$TRIDENT_CERT_DIR/clientKey" 2048
   openssl req -new -key "$TRIDENT_CERT_DIR/clientKey" -subj '/CN=trident-node' \
     -out "$TRIDENT_CERT_DIR/client.csr"
   openssl x509 -req -in "$TRIDENT_CERT_DIR/client.csr" -CA "$TRIDENT_CERT_DIR/caCert" \
     -CAkey "$TRIDENT_CERT_DIR/caKey" -set_serial 2 -out "$TRIDENT_CERT_DIR/clientCert" \
     -days 3650 -sha256
   openssl rand -hex 16 > "$TRIDENT_CERT_DIR/aesKey"
   oc -n osac-csi-backends create secret generic trident-csi \
     --from-file=caKey="$TRIDENT_CERT_DIR/caKey" \
     --from-file=caCert="$TRIDENT_CERT_DIR/caCert" \
     --from-file=serverKey="$TRIDENT_CERT_DIR/serverKey" \
     --from-file=serverCert="$TRIDENT_CERT_DIR/serverCert" \
     --from-file=clientKey="$TRIDENT_CERT_DIR/clientKey" \
     --from-file=clientCert="$TRIDENT_CERT_DIR/clientCert" \
     --from-file=aesKey="$TRIDENT_CERT_DIR/aesKey"
   rm -rf "$TRIDENT_CERT_DIR"
   ```
3. Install the Trident persistence CRDs from the official installer package.
   Do not run its controller installation.

   ```bash
   TRIDENT_VERSION=25.10.0
   curl -fsSL -o "/tmp/trident-installer-${TRIDENT_VERSION}.tar.gz" \
     "https://github.com/NetApp/trident/releases/download/v${TRIDENT_VERSION}/trident-installer-${TRIDENT_VERSION}.tar.gz"
   tar -xzf "/tmp/trident-installer-${TRIDENT_VERSION}.tar.gz" -C /tmp
   cd /tmp/trident-installer
   ./tridentctl install --generate-custom-yaml -n osac-csi-backends
   oc apply -f setup/trident-crds.yaml
   oc get crd | grep trident
   ```

   Do not apply `setup/trident-deployment.yaml` or
   `tridentorchestrator_cr.yaml`. Those files start a separate Trident
   controller or operator. Keep `setup/trident-daemonset.yaml`. Step 5
   patches that file into the node DaemonSet.
4. Deploy the OSAC Trident controller from the `csi-backends` chart. The
   chart does not install the CRDs from step 3, and it does not grant the
   `anyuid` SCC.

   ```bash
   export OSAC_DIR=/path/to/osac
   helm template trident-be "${OSAC_DIR}/osac-csi-driver/charts/csi-backends" \
     --namespace osac-csi-backends \
     --set namespace=osac-csi-backends \
     --set trident.enabled=true \
     --set trident.config.certsSecret=trident-csi \
     --set 'imagePullSecrets[0].name=pull-secret' \
     | oc apply -f -
   oc adm policy add-scc-to-user anyuid -z trident-vendor-sa -n osac-csi-backends
   oc rollout status deploy/trident-csi-controller \
     -n osac-csi-backends --timeout=180s
   ```

   Review the `anyuid` grant with the cluster administrator before you apply
   it. Do not enable the OSAC chart node DaemonSet
   (`vendors.trident.enabled`). That DaemonSet can provision a volume and
   still fail to mount NFS.
5. After the controller is Ready, install the official Trident node
   DaemonSet. The node service account and SCC below are not part of the
   OSAC distribution. A cluster administrator must review them against the
   selected Trident release and the cluster SCC policy before you apply them.
   The ClusterRole `trident-be-csi-backends-trident` comes from the chart in
   step 4, so create the binding only after that apply succeeds.

   ```bash
   cat > /tmp/trident-installer/setup/trident-node-linux-serviceaccount.yaml <<'EOF'
   apiVersion: v1
   kind: ServiceAccount
   metadata:
     name: trident-node-linux
     namespace: osac-csi-backends
     labels:
       app: node.csi.trident.netapp.io
   EOF
   cat > /tmp/trident-installer/setup/trident-node-linux-scc.yaml <<'EOF'
   apiVersion: security.openshift.io/v1
   kind: SecurityContextConstraints
   metadata:
     annotations:
       kubernetes.io/description: 'trident-node-linux is a clone of the privileged built-in, and is meant just for use with trident.'
     name: trident-node-linux
     labels:
       app: node.csi.trident.netapp.io
   allowHostDirVolumePlugin: true
   allowHostIPC: true
   allowHostNetwork: true
   allowHostPID: true
   allowHostPorts: false
   allowPrivilegeEscalation: true
   allowPrivilegedContainer: true
   allowedCapabilities:
   - SYS_ADMIN
   allowedUnsafeSysctls: null
   defaultAddCapabilities: null
   fsGroup:
     type: RunAsAny
   groups: []
   priority: null
   readOnlyRootFilesystem: false
   requiredDropCapabilities: null
   runAsUser:
     type: RunAsAny
   seLinuxContext:
     type: RunAsAny
   supplementalGroups:
     type: RunAsAny
   users:
   - system:serviceaccount:osac-csi-backends:trident-node-linux
   volumes:
   - hostPath
   - downwardAPI
   - projected
   - emptyDir
   EOF
   oc apply -f /tmp/trident-installer/setup/trident-node-linux-serviceaccount.yaml
   oc apply -f /tmp/trident-installer/setup/trident-node-linux-scc.yaml
   oc adm policy add-scc-to-user trident-node-linux \
     -z trident-node-linux -n osac-csi-backends
   oc adm policy add-scc-to-user privileged \
     -z trident-node-linux -n osac-csi-backends
   oc create clusterrolebinding trident-node-linux-osac \
     --clusterrole=trident-be-csi-backends-trident \
     --serviceaccount=osac-csi-backends:trident-node-linux \
     --dry-run=client -o yaml | oc apply -f -
   ```

   Write `setup/trident-node-linux-osac.yaml` from
   `setup/trident-daemonset.yaml` in step 3. The script sets namespace
   `osac-csi-backends`, service account `trident-node-linux`, image
   `registry.connect.redhat.com/netapp/trident:25.10.0`, and the
   `pull-secret` from step 2. It removes the `trident-encryption-keys`
   secret from the `certs` volume. That secret is not created in step 2.
   Stop if this script exits with an error. Do not apply
   `setup/trident-deployment.yaml`.

   ```bash
   python3 <<'PY'
   from pathlib import Path
   src = Path("/tmp/trident-installer/setup/trident-daemonset.yaml")
   text = src.read_text()
   meta = "metadata:\n  name: trident-node-linux\n"
   if meta not in text:
       raise SystemExit("expected DaemonSet metadata name trident-node-linux")
   if "\n  namespace: osac-csi-backends\n" not in text:
       text = text.replace(meta, meta + "  namespace: osac-csi-backends\n", 1)
   sa = "      serviceAccount: trident-node-linux\n"
   if sa not in text:
       raise SystemExit("expected serviceAccount trident-node-linux")
   if "serviceAccountName: trident-node-linux\n" not in text:
       text = text.replace(
           sa, sa + "      serviceAccountName: trident-node-linux\n", 1)
   pull = "      serviceAccountName: trident-node-linux\n"
   if "name: pull-secret" not in text:
       text = text.replace(
           pull,
           pull + "      imagePullSecrets:\n      - name: pull-secret\n",
           1,
       )
   lines = text.splitlines(True)
   out = []
   in_main = False
   image_done = False
   i = 0
   while i < len(lines):
       line = lines[i]
       if line.strip() == "- name: trident-main":
           in_main = True
       elif in_main and line.startswith("      - name:"):
           in_main = False
       if in_main and not image_done and line.startswith("        image:"):
           out.append(
               "        image: registry.connect.redhat.com/netapp/trident:25.10.0\n")
           image_done = True
           i += 1
           continue
       if (line.strip() == "- secret:" and i + 1 < len(lines)
               and lines[i + 1].strip() == "name: trident-encryption-keys"):
           i += 2
           continue
       out.append(line)
       i += 1
   if not image_done:
       raise SystemExit("expected a trident-main image")
   patched = "".join(out)
   if "trident-encryption-keys" in patched:
       raise SystemExit("trident-encryption-keys is still present")
   Path("/tmp/trident-installer/setup/trident-node-linux-osac.yaml").write_text(
       patched)
   PY
   oc delete ds trident-csi-node -n osac-csi-backends --ignore-not-found
   oc apply -f /tmp/trident-installer/setup/trident-node-linux-osac.yaml
   oc rollout status ds/trident-node-linux \
     -n osac-csi-backends --timeout=180s
   fail=0
   if ! nodes=$(oc get nodes -l node-role.kubernetes.io/worker \
     -o jsonpath='{range .items[*]}{.metadata.name}{"\n"}{end}'); then
     echo "Failed to list worker nodes" >&2
     exit 1
   fi
   if [ -z "$nodes" ]; then
     if ! nodes=$(oc get nodes \
       -o jsonpath='{range .items[*]}{.metadata.name}{"\n"}{end}'); then
       echo "Failed to list nodes" >&2
       exit 1
     fi
   fi
   if [ -z "$nodes" ]; then
     echo "No nodes found" >&2
     exit 1
   fi
   for node in $nodes; do
     if ! oc get csinode "$node" \
         -o jsonpath='{range .spec.drivers[*]}{.name}{"\n"}{end}' \
         | grep -qx 'csi.trident.netapp.io'; then
       echo "csi.trident.netapp.io is not registered on ${node}"
       fail=1
     fi
   done
   test "$fail" -eq 0
   ```

   The last command fails if any worker is missing `csi.trident.netapp.io`.
   On a cluster with no `worker` role label, it checks every node.
6. From each node that will run VM pods, mount the dedicated NFS export with
   NFSv3, write a file, and unmount it. Stop if any node fails. Do not create
   the Trident backend until every node succeeds.

   ```bash
   if ! nodes=$(oc get nodes -l node-role.kubernetes.io/worker \
     -o jsonpath='{range .items[*]}{.metadata.name}{"\n"}{end}'); then
     echo "Failed to list worker nodes" >&2
     exit 1
   fi
   if [ -z "$nodes" ]; then
     if ! nodes=$(oc get nodes \
       -o jsonpath='{range .items[*]}{.metadata.name}{"\n"}{end}'); then
       echo "Failed to list nodes" >&2
       exit 1
     fi
   fi
   if [ -z "$nodes" ]; then
     echo "No nodes found" >&2
     exit 1
   fi
   for node in $nodes; do
     oc debug "node/${node}" -- chroot /host sh -c \
       "mkdir -p /mnt/osac-nfs-check && mount -t nfs -o nfsvers=3,sec=sys ${NFS_DATA_LIF}:${NFS_EXPORT_PATH} /mnt/osac-nfs-check && df -h /mnt/osac-nfs-check && echo ok > /mnt/osac-nfs-check/.osac-write-test && rm /mnt/osac-nfs-check/.osac-write-test && umount /mnt/osac-nfs-check" \
       || exit 1
   done
   ```

   The export policy on this share must allow each node's address. The
   Trident backend in step 7 uses export policy `default`. A successful mount
   of this dedicated export does not prove that `default` allows these nodes.
   Confirm that policy before you continue. This check does not mount the
   FlexVol pool Trident creates later. Confirm that pool when the first
   NetApp PVC is Bound, at the end of step 10. With `ontap-nas-economy`,
   Trident stores PVC qtrees in a FlexVol pool that it creates. Those qtrees
   are not placed in this export. Do not use `lun show` to verify this NFS
   workflow.
7. Create the Trident `ontap-nas-economy` backend and confirm that Trident
   reports it online. Write the JSON to a local file mode `600`. Do not
   commit that file. Applying the JSON as a Kubernetes resource does not
   create a Trident backend. Do not run `tridentctl create backend -n
   osac-csi-backends` on the workstation. That client looks for a stock
   Trident pod. The controller container has no `tar`, so `oc cp` cannot
   copy the file in. Send the JSON on stdin to `tridentctl` in the
   controller pod.

   ```bash
   umask 077
   ONTAP_BACKEND_FILE=$(mktemp)
   cat > "$ONTAP_BACKEND_FILE" <<EOF
   {
     "version": 1,
     "storageDriverName": "ontap-nas-economy",
     "backendName": "ontap-economy",
     "managementLIF": "${ONTAP_MANAGEMENT_ENDPOINT}",
     "dataLIF": "${NFS_DATA_LIF}",
     "svm": "${ONTAP_SVM}",
     "username": "${ONTAP_USERNAME}",
     "password": "${ONTAP_PASSWORD}",
     "storagePrefix": "osac_",
     "nfsMountOptions": "nfsvers=3",
     "defaults": {
       "spaceReserve": "none",
       "snapshotPolicy": "none",
       "exportPolicy": "default"
     }
   }
   EOF
   POD=$(oc get pod -n osac-csi-backends \
     -l app.kubernetes.io/name=trident-csi-controller \
     -o jsonpath='{.items[0].metadata.name}')
   oc exec -i -n osac-csi-backends "$POD" -c trident-main -- \
     tridentctl -s 127.0.0.1:8000 create backend -f - \
     < "$ONTAP_BACKEND_FILE" \
     && oc exec -n osac-csi-backends "$POD" -c trident-main -- \
       tridentctl -s 127.0.0.1:8000 get backend \
     && rm -f "$ONTAP_BACKEND_FILE"
   ```

   The file is removed only after create and get both succeed. If create
   fails, keep it and use a fallback below.

   `managementLIF` is the SVM management host from step 1. `dataLIF` is the
   NFS data LIF from step 6. See NetApp's
   [ONTAP NAS backend examples](https://docs.netapp.com/us-en/trident-2510/trident-use/ontap-nas-examples.html).

   If `tridentctl` in the container cannot reach `127.0.0.1:8000`,
   port-forward HTTP port 8000 to a free local port and use the installer
   `tridentctl`. Do not pass port `8443` to `tridentctl -s`. That port is
   TLS, and `tridentctl -s` speaks HTTP.

   ```bash
   oc port-forward -n osac-csi-backends deploy/trident-csi-controller 18000:8000
   ```

   ```bash
   /tmp/trident-installer/tridentctl -s 127.0.0.1:18000 create backend \
     -f "$ONTAP_BACKEND_FILE" \
     && /tmp/trident-installer/tridentctl -s 127.0.0.1:18000 get backend \
     && rm -f "$ONTAP_BACKEND_FILE"
   ```

   If nothing in the pod listens on port 8000, REST is HTTPS on port 8443.
   The controller requires the client certificate from step 2. Do not use
   `-k`. The server certificate name is `trident-csi`, so call that host and
   send the connection to the local port-forward.

   ```bash
   oc port-forward -n osac-csi-backends deploy/trident-csi-controller 18443:8443
   ```

   ```bash
   CERTDIR=$(mktemp -d)
   umask 077
   for f in caCert clientCert clientKey; do
     oc -n osac-csi-backends get secret trident-csi \
       -o jsonpath="{.data.${f}}" | base64 -d > "${CERTDIR}/${f}"
   done
   curl --fail --cacert "${CERTDIR}/caCert" \
     --cert "${CERTDIR}/clientCert" --key "${CERTDIR}/clientKey" \
     --resolve "trident-csi:18443:127.0.0.1" \
     -X POST "https://trident-csi:18443/trident/v1/backend" \
     -H 'Content-Type: application/json' \
     --data-binary @"$ONTAP_BACKEND_FILE" \
     && rm -f "$ONTAP_BACKEND_FILE"
   rm -rf "$CERTDIR"
   ```
8. Create the tenant on the private administrator session from step 1, if
   it does not already exist. A public session cannot create a tenant. The
   create output prints the break-glass username and password once. Copy
   them before you continue. The username is `${TENANT}-osac-break-glass`.
   Wait until the tenant is `SYNCED`. The default VirtualNetwork, Subnet,
   and SecurityGroup are created with the tenant. Do not create a second
   VirtualNetwork.

   ```bash
   osac create -f - <<EOF
   "@type": type.googleapis.com/osac.private.v1.Tenant
   metadata:
     name: ${TENANT}
   EOF
   oc get tenant "$TENANT" -n "$OSAC_NAMESPACE"
   oc get sc "osac-${TENANT}-local" --show-labels
   ```

   Creating the tenant grants IdP manager only. VM create requires
   `tenant-admin`. In the OSAC console, log in as the break-glass user,
   open **Role Bindings**, create a binding for role `tenant-admin` for
   that user, and wait until it is `READY`. Log out and back in. The header
   must show `tenant-admin`. If Keycloak asks for a new password, do not use
   `%` or `#`.

   Then sign in on the internal API as that user. `osac whoami` must list
   `tenant-admin`. `osac get tenants` returns PermissionDenied for this
   user.

   ```bash
   INT_ROUTE=$(oc get route fulfillment-internal-api -n "$OSAC_NAMESPACE" \
     -o jsonpath='{.spec.host}')
   KC_HOST=$(oc get route keycloak -n keycloak -o jsonpath='{.spec.host}')
   osac logout 2>/dev/null || true
   osac login --address "$INT_ROUTE" \
     --ca-file default-ca.crt \
     --issuer="https://${KC_HOST}/realms/osac" \
     --flow=password \
     --user="${TENANT}-osac-break-glass" \
     --password="$BREAK_GLASS_PASSWORD"
   osac whoami
   ```
9. Create one tenant StorageClass for the tier. Use the Trident
   `ontap-nas-economy` driver and thin provisioning. Set the tenant metadata
   and the labels OSAC uses to discover the class.

   ```bash
   cat <<EOF | oc apply -f -
   apiVersion: storage.k8s.io/v1
   kind: StorageClass
   metadata:
     name: ontap-nas-${TENANT}
     annotations:
       osac.openshift.io/tenant: "${TENANT}"
       osac.openshift.io/owner-reference: "Tenant/${TENANT}"
     labels:
       osac.openshift.io/tenant: "${TENANT}"
       osac.openshift.io/storage-tier: ontap-nas
   provisioner: csi.trident.netapp.io
   allowVolumeExpansion: true
   volumeBindingMode: Immediate
   parameters:
     backendType: ontap-nas-economy
     provisioningType: thin
   EOF
   ```

   After you apply the class, confirm that the tenant's
   `status.storageClasses` lists both the existing local class
   (`osac-${TENANT}-local`, tier `local`) and `ontap-nas-${TENANT}` (tier
   `ontap-nas`). Labels alone do not publish the class to Fulfillment.
   Request reconciliation first. Do not replace or remove the `local` entry.

   ```bash
   oc annotate tenant "$TENANT" -n "$OSAC_NAMESPACE" \
     osac.openshift.io/reconcile="$(date +%s)" --overwrite
   oc get tenant "$TENANT" -n "$OSAC_NAMESPACE" \
     -o jsonpath='{.status.storageClasses}{"\n"}'
   ```

   If reconciliation does not add the class, an authorized cluster
   administrator can patch the status subresource. Include both entries in
   the patch.

   ```bash
   cat > /tmp/tenant-storage-classes.json <<EOF
   {"status":{"storageClasses":[
     {"name":"osac-${TENANT}-local","tier":"local"},
     {"name":"ontap-nas-${TENANT}","tier":"ontap-nas"}
   ]}}
   EOF
   oc patch tenant "$TENANT" -n "$OSAC_NAMESPACE" \
     --type=merge --subresource=status \
     --patch-file=/tmp/tenant-storage-classes.json
   ```
10. Stay signed in as the tenant user from step 8. Do not use `--private`.
    A `--private` session creates the VM on the `shared` tenant. Read the
    default subnet and security group, then create a VM on the `local` tier
    and wait until it is `Running`. Its PVC must use `osac-${TENANT}-local`.
    Then create a second VM that uses the NetApp tier for its root disk.
    You can set an optional additional disk when you create that VM. This
    workflow cannot add a disk to a VM that already exists. If create fails
    because the catalog name is unknown, sign in with `--private`, run
    `osac get computeinstancecatalogitem`, and retry with that item's UUID
    on the tenant session.

    The guest password is written to a file and passed with `osac create -f`.
    It is not an argument of the `osac` process. It is inserted as a quoted
    YAML scalar, so characters such as `:` and `#` stay inside the value.

    ```bash
    SUBNET_ID=$(osac get subnet default-ipv4 -o json | jq -r '.id')
    SECURITY_GROUP_ID=$(osac get securitygroup default -o json | jq -r '.id')
    GUEST_PASSWORD_YAML=$(printf '%s' "$GUEST_PASSWORD" | python3 -c 'import json,sys; print(json.dumps(sys.stdin.read()), end="")')
    umask 077
    LOCAL_VM_FILE=$(mktemp)
    cat > "$LOCAL_VM_FILE" <<EOF
    "@type": type.googleapis.com/osac.public.v1.ComputeInstance
    metadata:
      name: local-vm
    spec:
      catalog_item: ${CATALOG_ITEM}
      instance_type: ${INSTANCE_TYPE}
      boot_disk:
        size_gib: 10
        storage_tier:
          name: local
      network_attachments:
        - subnet: ${SUBNET_ID}
          security_groups:
            - ${SECURITY_GROUP_ID}
      run_strategy: Always
      user_data: |
        #cloud-config
        user: osac-admin
        password: ${GUEST_PASSWORD_YAML}
        chpasswd:
          expire: false
        ssh_pwauth: true
    EOF
    osac create -f "$LOCAL_VM_FILE"
    rm -f "$LOCAL_VM_FILE"
    osac get computeinstance
    ```

    ```bash
    GUEST_PASSWORD_YAML=$(printf '%s' "$GUEST_PASSWORD" | python3 -c 'import json,sys; print(json.dumps(sys.stdin.read()), end="")')
    umask 077
    NETAPP_VM_FILE=$(mktemp)
    cat > "$NETAPP_VM_FILE" <<EOF
    "@type": type.googleapis.com/osac.public.v1.ComputeInstance
    metadata:
      name: netapp-vm
    spec:
      catalog_item: ${CATALOG_ITEM}
      instance_type: ${INSTANCE_TYPE}
      boot_disk:
        size_gib: 10
        storage_tier:
          name: ontap-nas
      additional_disks:
        - size_gib: 20
          storage_tier:
            name: ontap-nas
      network_attachments:
        - subnet: ${SUBNET_ID}
          security_groups:
            - ${SECURITY_GROUP_ID}
      run_strategy: Always
      user_data: |
        #cloud-config
        user: osac-admin
        password: ${GUEST_PASSWORD_YAML}
        chpasswd:
          expire: false
        ssh_pwauth: true
    EOF
    osac create -f "$NETAPP_VM_FILE"
    rm -f "$NETAPP_VM_FILE"
    ```

    Omit `additional_disks` for a root-only VM. The VM order must use the
    tenant's `ontap-nas` tier. The generated PVC must use the tenant-specific
    class `ontap-nas-${TENANT}`. The local VM does not need the AAP workaround
    below. The NetApp VM does. When that job fails, leave the
    `ComputeInstance` in `Starting`. Do not delete it.

**AAP workaround**

The first provisioning job for the NetApp VM fails with `the role
'osac.templates.ontap_storage' was not found`, and the `ComputeInstance`
stays in `Starting`. The AAP collection does not include that role. Do not
delete the `ComputeInstance`.

The workaround relaunches the failed job template after removing only the
`ontap-nas` entry from `storage_tier_definitions`. It fills in for the
missing role. Repeat it for each affected VM. It does not install Trident
or create the StorageClass.

In one terminal, port-forward the AAP controller service in the OSAC
namespace. In a second terminal, read the failed AAP job ID from that
`ComputeInstance` and run the script on a trusted admin workstation. The
script prompts for the AAP password so the password is not stored in shell
history. Confirm the failed job ID and the template ID before you launch the
job, and use the failed job for this VM only.

```bash
oc port-forward -n "$OSAC_NAMESPACE" svc/osac-aap-controller-service 18053:80
```

In the second terminal, set `CI_NAME` to the `ComputeInstance` that is still
in `Starting`:

```bash
CI_NAME="replace-with-computeinstance-name"
FAILED_JOB=$(oc get computeinstance "$CI_NAME" -n "$OSAC_NAMESPACE" \
  -o jsonpath='{.status.provisioningJobs[-1].jobID}')
printf 'Failed AAP job ID: %s\n' "$FAILED_JOB"
```

```python
import base64
import getpass
import json
import urllib.request

api = "http://127.0.0.1:18053"
username = input("AAP username: ")
password = getpass.getpass("AAP password: ")
job_id = input("Failed AAP job ID for this VM: ")
auth = base64.b64encode(f"{username}:{password}".encode()).decode()

def request(path, payload=None):
    data = None if payload is None else json.dumps(payload).encode()
    headers = {"Authorization": f"Basic {auth}", "Content-Type": "application/json"}
    req = urllib.request.Request(f"{api}{path}", data=data, headers=headers)
    with urllib.request.urlopen(req) as response:
        return json.load(response)

failed_job = request(f"/api/v2/jobs/{job_id}/")
template_id = failed_job["summary_fields"]["job_template"]["id"]
print(f"Job {job_id} status {failed_job.get('status')}, template {template_id}")
if input("Launch this template? Type yes: ") != "yes":
    raise SystemExit("not launched")
extra_vars = failed_job["extra_vars"]
if isinstance(extra_vars, str):
    extra_vars = json.loads(extra_vars)
job_vars = extra_vars["osac_job_vars"]
job_vars["storage_tier_definitions"] = [
    tier for tier in job_vars["storage_tier_definitions"]
    if tier.get("name") != "ontap-nas"
]
launched = request(
    f"/api/v2/job_templates/{template_id}/launch/",
    {"extra_vars": {"osac_job_vars": job_vars}},
)
print(f"Launched AAP job {launched.get('id')}")
```

Wait until the new AAP job finishes. Confirm that the `ComputeInstance`
reaches `Running`, and that both the root PVC and the optional `*-disk-1`
PVC are Bound to `ontap-nas-${TENANT}`. Confirm that the PV CSI driver is
`csi.trident.netapp.io`. In the guest, the additional disk appears as
`/dev/vdb` and is not formatted or mounted automatically. With
`ontap-nas-economy`, Trident stores PVCs as qtrees in Trident-managed FlexVol
pools. In the tested workflow, those qtrees were not inside the dedicated
export used to check NFS reachability. Do not use `lun show` to verify this
NFS workflow.

Use the subnet namespace from the tenant's default subnet to inspect the VM
and PVCs. `internalID` is the SVM, the FlexVol pool, and the qtree for that
disk. On ONTAP, run `qtree show -volume <flexvol>` and match `internalName`.
Those qtrees are on the pool volume, not on the export mounted in step 6.
If `driver` is `topolvm.io`, that disk is the `local` tier.

```bash
osac get computeinstance
oc get vm,dv,pvc -n "subnet-${SUBNET_ID}"
oc get pvc -n "subnet-${SUBNET_ID}" -o json \
  | jq -r --arg sc "ontap-nas-${TENANT}" '.items[] | select(.spec.storageClassName==$sc) | .metadata.name' \
  | while read -r pvc; do
      echo "===== ${pvc} ====="
      vol=$(oc get pvc -n "subnet-${SUBNET_ID}" "$pvc" -o jsonpath='{.spec.volumeName}')
      oc get pv "$vol" -o jsonpath='driver={.spec.csi.driver}{"\n"}internalID={.spec.csi.volumeAttributes.internalID}{"\n"}internalName={.spec.csi.volumeAttributes.internalName}{"\n"}'
    done
```

This workaround depends on AAP REST job variables and on the job template
that failed. A later AAP or OSAC release can change it.
