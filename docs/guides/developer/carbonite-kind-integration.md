# KiND access from the Carbonite/OpenShell sandbox

Phase 7 connects one local, host-managed KiND cluster to the level-3 OSAC
sandbox. The operator integration suite runs inside Carbonite; cluster creation,
operator deployment, and the Kubernetes identity remain host responsibilities.
This does not enable `make install` against OpenShift or the separate AAP
integration harness.

Run `kind` only on the host. KiND uses Podman to create and inspect its node
containers, and the level-3 sandbox does not permit nested Podman. The
Carbonite image intentionally contains `kubectl` but no `kind` binary.
For example, run `KIND_EXPERIMENTAL_PROVIDER=podman kind get clusters` on the
host, and use `kubectl` with the scoped kubeconfig inside the sandbox.

Ordinary development-container targets can select Carbonite with
`USE_CARBONITE=true` or `OSAC_USE_CARBONITE=true`; an explicit command-line
value, including `false`, overrides the environment. Only `true` and `false`
are valid; any other value fails immediately. This first PR supports
host Podman only. Docker is not a supported Carbonite runtime until the
separate delivery phase validates it.

Carbonite entry points default to the cluster-enabled sandbox described in
this guide; an ordinary no-cluster Carbonite sandbox requires explicit
overrides for its name, workspace, policy, and empty kubeconfig directory.

Run the host commands below from the OSAC repository root in one host shell.
They use the `osac-dev` KiND cluster created by `install-infra` and its admin
kubeconfig at `~/.kube/osac-dev-kind.kubeconfig`. The scoped kubeconfig for the
sandbox is a separate file created later in this guide; do not mount the admin
kubeconfig into Carbonite.

## What the developer must prepare on the host

1. Install host `kind`, `kubectl`, Podman, and a fixed TCP relay such as `socat`.
   Run `make check-carb`. The OpenShell Podman gateway must be local and have
   `allow_driver_config = true`, `enable_bind_mounts = true`, and
   `userns = "keep-id:uid=1000,gid=1000"` under `[openshell.drivers.podman]`
   in the host's `~/.config/openshell/gateway.toml`, as in the
   [root setup](../../../README.md#carbonite--openshell-linuxx86_64).
   On this Podman driver, the supervisor's `host.openshell.internal` mapping
   can select an address no longer assigned to a laptop after a network
   change. Before creating the cluster-enabled sandbox, set
   `host_gateway_ip` in that same `[openshell.drivers.podman]` section to the
   host's current IPv4 address, then restart the gateway. The following
   host-only command preserves the rest of the TOML and saves its first backup
   beside it. It confines strict shell options to a subshell:

   ```bash
   (
   set -euo pipefail
   gateway_config="$HOME/.config/openshell/gateway.toml"
   host_gateway_ip="$(ip -4 route get 1.1.1.1 | awk '{ for (i = 1; i < NF; i++) if ($i == "src") { print $(i+1); exit } }')"
   python3 - "$gateway_config" "$host_gateway_ip" <<'PY'
   import ipaddress, os, re, shutil, stat, sys, tempfile, tomllib
   from pathlib import Path

   path = Path(sys.argv[1])
   address = ipaddress.IPv4Address(sys.argv[2])
   assert address.is_private and not address.is_loopback and not address.is_link_local
   original = path.read_text()
   lines = original.splitlines(keepends=True)
   section = '[openshell.drivers.podman]'
   starts = [i for i, line in enumerate(lines) if line.strip() == section]
   assert len(starts) == 1
   start = starts[0]
   end = next((i for i in range(start + 1, len(lines)) if lines[i].lstrip().startswith('[')), len(lines))
   keys = [i for i in range(start + 1, end) if re.match(r'\s*host_gateway_ip\s*=', lines[i])]
   assert len(keys) <= 1
   setting = f'host_gateway_ip = "{address}"\n'
   if keys:
       lines[keys[0]] = setting
   else:
       lines.insert(start + 1, setting)
   updated = ''.join(lines)
   tomllib.loads(updated)
   backup = path.with_name(path.name + '.before-kind-relay')
   if not backup.exists():
       shutil.copy2(path, backup)
   fd, next_path = tempfile.mkstemp(prefix=path.name + '.', dir=path.parent)
   try:
       os.fchmod(fd, stat.S_IMODE(path.stat().st_mode))
       with os.fdopen(fd, 'w') as out:
           out.write(updated)
           out.flush()
           os.fsync(out.fileno())
       os.replace(next_path, path)
   finally:
       if os.path.exists(next_path):
           os.unlink(next_path)
   PY
   systemctl --user restart openshell-gateway
   systemctl --user is-active openshell-gateway
   )
   ```

   This driver option controls the host alias injected into *new* supervisors.
   If the sandbox already exists, rebuild it after changing this setting. On
   each move to a network with a different host IP, repeat the setting and
   restart/rebuild before using the KiND relay. OpenShell documents the
   [Podman host-gateway override](https://docs.nvidia.com/openshell/v0.1.2/how-it-works/gateways/configuration).
2. Create or select a KiND cluster using the Podman provider, then deploy the
   current OSAC operator and console proxy to namespace `osac` from the host.
   Keep the cluster-admin kubeconfig on the host, outside the sandbox-mounted
   checkout. The integration suite expects one controller-manager pod labeled
   `control-plane=controller-manager,app.kubernetes.io/name=operator`, the
   console-proxy deployment, CRDs, and its APIService. Check these with the
   admin kubeconfig before giving the sandbox access. Before creating a
   rootless KiND cluster, check the host's inotify limits.
   [KiND's guidance](https://kind.sigs.k8s.io/docs/user/known-issues/#pod-errors-due-to-too-many-open-files)
   recommends `fs.inotify.max_user_instances=512` and
   `fs.inotify.max_user_watches=524288` to avoid pods failing with
   `too many open files`:

   ```bash
   sysctl fs.inotify.max_user_instances fs.inotify.max_user_watches
   ```

   For a temporary change while debugging, run on the host:

   ```bash
   sudo sysctl -w fs.inotify.max_user_instances=512 \
     fs.inotify.max_user_watches=524288
   ```

   This takes effect immediately and resets on reboot. To keep the limits
   across reboots, create a dedicated host configuration file and load it:

   ```bash
   printf '%s\n' \
     'fs.inotify.max_user_instances = 512' \
     'fs.inotify.max_user_watches = 524288' \
     | sudo tee /etc/sysctl.d/99-osac-kind-inotify.conf >/dev/null
   sudo sysctl --load=/etc/sysctl.d/99-osac-kind-inotify.conf
   sysctl fs.inotify.max_user_instances fs.inotify.max_user_watches
   ```

   The persistent file affects host-wide limits, not only KiND. If pods were
   already failing, restart those deployments after changing the limits.

   Install the development infrastructure from the host. This creates or
   reuses the `osac-dev` cluster and writes its cluster-admin kubeconfig to
   `~/.kube/osac-dev-kind.kubeconfig`. The parentheses contain the strict
   shell options: on failure, this block stops but your host shell remains open.

   ```bash
   (
   set -euo pipefail
   KIND_EXPERIMENTAL_PROVIDER=podman \
     make -C osac-installer install-infra PLATFORM=kind PROFILE=dev NS=osac
   kubectl --kubeconfig "$HOME/.kube/osac-dev-kind.kubeconfig" \
     cluster-info --context kind-osac-dev
   )
   ```

   Build the fulfillment and operator images from this same checkout on the
   host, then load both into KiND. Use a fresh tag for each build so Helm
   rolls out changed code. The image archives are temporary:

   ```bash
   kind_tag="local-$(date +%Y%m%d%H%M%S)"
   make -C fulfillment-service image-build IMG="localhost/fulfillment-service:${kind_tag}"
   make -C osac-operator image-build IMG="localhost/osac-operator:${kind_tag}"
   image_dir="$(mktemp -d)"
   podman save -o "$image_dir/fulfillment.tar" "localhost/fulfillment-service:${kind_tag}"
   podman save -o "$image_dir/operator.tar" "localhost/osac-operator:${kind_tag}"
   KIND_EXPERIMENTAL_PROVIDER=podman kind load image-archive "$image_dir/fulfillment.tar" --name osac-dev
   KIND_EXPERIMENTAL_PROVIDER=podman kind load image-archive "$image_dir/operator.tar" --name osac-dev
   rm -- "$image_dir/fulfillment.tar" "$image_dir/operator.tar"
   rmdir -- "$image_dir"
   ```

   Install the OSAC release with these checkout-matched images. This deploys
   the OSAC operator, its CRDs, and its console proxy (the console proxy is
   enabled in the KiND values). Both operator containers use the local operator
   image. Keep `kind_tag` from the previous block in the same host shell:

   ```bash
   helm_args="--set service.images.service=localhost/fulfillment-service:${kind_tag} \
     --set service.images.pullPolicy=Never \
     --set operator.image.repository=localhost/osac-operator \
     --set operator.image.tag=${kind_tag} \
     --set operator.image.pullPolicy=Never"
   KIND_EXPERIMENTAL_PROVIDER=podman \
     make -C osac-installer install-osac PLATFORM=kind PROFILE=dev NS=osac \
       EXTRA_HELM_ARGS="$helm_args"
   kubectl --kubeconfig "$HOME/.kube/osac-dev-kind.kubeconfig" \
     rollout status deployment/osac-operator -n osac --timeout=2m
   kubectl --kubeconfig "$HOME/.kube/osac-dev-kind.kubeconfig" \
     rollout status deployment/osac-operator-console-proxy -n osac --timeout=2m
   kubectl --kubeconfig "$HOME/.kube/osac-dev-kind.kubeconfig" \
     rollout status deployment/fulfillment-grpc-server -n osac --timeout=2m
   kubectl --kubeconfig "$HOME/.kube/osac-dev-kind.kubeconfig" \
     get deployments -n osac osac-operator osac-operator-console-proxy
   kubectl --kubeconfig "$HOME/.kube/osac-dev-kind.kubeconfig" \
     get apiservice v1alpha1.console.osac.openshift.io
   kubectl --kubeconfig "$HOME/.kube/osac-dev-kind.kubeconfig" \
     rollout status deployment/fulfillment-ingress-proxy -n osac --timeout=2m
   ```

   `fulfillment-ingress-proxy` uses the chart's pinned Envoy image, not either
   checkout-built image above. If its pod template did not change, Helm leaves
   its existing pod running; an older pod creation time is expected. The
   rollout-status check confirms it is healthy. If you changed the ingress
   ConfigMap or its TLS material, restart that deployment from the host to
   make Envoy read the new files:

   ```bash
   kubectl --kubeconfig "$HOME/.kube/osac-dev-kind.kubeconfig" \
     rollout restart deployment/fulfillment-ingress-proxy -n osac
   kubectl --kubeconfig "$HOME/.kube/osac-dev-kind.kubeconfig" \
     rollout status deployment/fulfillment-ingress-proxy -n osac --timeout=2m
   ```

   The installer exports `KUBECONFIG` only to its child processes; plain
   `kubectl` in another shell will not find `kind-osac-dev` unless you pass
   this kubeconfig or export its path. On 2026-10-12, `make install` with
   floating `:latest` images left fulfillment and operator unhealthy because
   those images expected settings or permissions absent from this checkout's
   charts. The checkout-matched build and install above reached Ready pods
   and deployed Helm releases. Rebuild and reload both images when changing
   their source. Keep installation on the host, where its container and
   cluster-admin operations belong.
3. Create a dedicated, short-lived Kubernetes ServiceAccount for these tests.
   Grant only the required permissions in `osac`: read operator pods and
   deployment; create/get/list/patch/delete the ComputeInstance,
   VirtualNetwork, and Subnet test resources and their finalizers. Grant
   cluster-scoped `get` on the one console APIService and `get` on `/readyz`.
   Review the exact suite before issuing the Role/ClusterRole; the suite mutates
   test resources and is not read-only. Check the permissions with
   `kubectl auth can-i --as=system:serviceaccount:osac:<service-account> ...`.
   Do not mount the cluster-admin kubeconfig. See the Kubernetes
   [RBAC guide](https://kubernetes.io/docs/reference/access-authn-authz/rbac/).

## Create scoped test RBAC on the host

The following host command writes the scoped RBAC manifest outside the
checkout and applies it with the default KiND admin kubeconfig. The Role is
limited to `osac`; the ClusterRole exposes only one APIService and
health/discovery paths. Review it against the current tests and adjust it
if they acquire new resource dependencies. The parentheses run the guarded
commands in a subshell, so a failed command returns to your host prompt.

```bash
(
set -euo pipefail
umask 077
STATE_DIR="${XDG_STATE_HOME:-$HOME/.local/state}/osac-carbonite-kind"
ADMIN_KUBECONFIG="$HOME/.kube/osac-dev-kind.kubeconfig"
test -f "$ADMIN_KUBECONFIG"
install -d -m 700 "$STATE_DIR" "$STATE_DIR/kube"
cat > "$STATE_DIR/operator-test-rbac.yaml" <<'YAML'
apiVersion: v1
kind: ServiceAccount
metadata:
  name: carbonite-operator-test
  namespace: osac
---
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: carbonite-operator-test
  namespace: osac
rules:
  - apiGroups: [""]
    resources: ["pods"]
    verbs: ["get", "list", "watch"]
  - apiGroups: ["apps"]
    resources: ["deployments"]
    verbs: ["get", "list", "watch"]
  - apiGroups: ["osac.openshift.io"]
    resources: ["computeinstances", "virtualnetworks", "subnets"]
    verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]
  - apiGroups: ["osac.openshift.io"]
    resources: ["computeinstances/finalizers", "virtualnetworks/finalizers", "subnets/finalizers"]
    verbs: ["update", "patch"]
  - apiGroups: ["console.osac.openshift.io"]
    resources: ["computeinstances/console", "computeinstances/vnc"]
    verbs: ["get"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: carbonite-operator-test
  namespace: osac
subjects:
  - kind: ServiceAccount
    name: carbonite-operator-test
    namespace: osac
roleRef:
  kind: Role
  name: carbonite-operator-test
  apiGroup: rbac.authorization.k8s.io
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: carbonite-operator-test
rules:
  - apiGroups: ["apiregistration.k8s.io"]
    resources: ["apiservices"]
    resourceNames: ["v1alpha1.console.osac.openshift.io"]
    verbs: ["get"]
  - nonResourceURLs: ["/readyz", "/apis/console.osac.openshift.io", "/apis/console.osac.openshift.io/v1alpha1"]
    verbs: ["get"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: carbonite-operator-test
subjects:
  - kind: ServiceAccount
    name: carbonite-operator-test
    namespace: osac
roleRef:
  kind: ClusterRole
  name: carbonite-operator-test
  apiGroup: rbac.authorization.k8s.io
YAML
kubectl --kubeconfig "$ADMIN_KUBECONFIG" apply -f "$STATE_DIR/operator-test-rbac.yaml"
)
```

## Create the sandbox kubeconfig

Use a dedicated host directory **outside the checkout**. Keep it owned by the
host user with mode `0700`, and keep only a single `config` file with mode
`0600`. OpenShell's verified keep-id mapping presents that host user as the
unprivileged `sandbox` user. The Make target checks the private read-only mount
before creation; do not change ownership to a subordinate UID. For the default
`osac-dev` install, this is
`~/.local/state/osac-carbonite-kind/kube/config` when `XDG_STATE_HOME` is unset.
`make install-infra` creates only the host admin kubeconfig; it does not create
this scoped sandbox kubeconfig.

The kubeconfig must contain only the scoped identity, an embedded CA, and a
single API endpoint: `https://host.openshell.internal:18443`. Create a
[time-limited ServiceAccount token](https://kubernetes.io/docs/reference/kubectl/generated/kubectl_create/kubectl_create_token/)
on the host. After applying the RBAC manifest above, copy and run this block
on the host. It reads the admin kubeconfig but writes only the scoped identity
to the sandbox kubeconfig. Its strict shell options are also isolated in a
subshell:

```bash
(
set -euo pipefail
umask 077
STATE_DIR="${XDG_STATE_HOME:-$HOME/.local/state}/osac-carbonite-kind"
ADMIN_KUBECONFIG="$HOME/.kube/osac-dev-kind.kubeconfig"
SERVICE_ACCOUNT='carbonite-operator-test'
test -f "$ADMIN_KUBECONFIG"
install -d -m 700 "$STATE_DIR" "$STATE_DIR/kube"
kubectl --kubeconfig "$ADMIN_KUBECONFIG" -n osac get serviceaccount "$SERVICE_ACCOUNT" >/dev/null
trap 'rm -f "$STATE_DIR/token.tmp" "$STATE_DIR/config.next"' EXIT
kubectl --kubeconfig "$ADMIN_KUBECONFIG" -n osac \
  create token "$SERVICE_ACCOUNT" --duration=12h > "$STATE_DIR/token.tmp"
kubectl --kubeconfig "$ADMIN_KUBECONFIG" config view --raw --minify --flatten -o json |
  python3 -c '
import json, pathlib, sys, urllib.parse
source = json.load(sys.stdin)
cluster = source["clusters"][0]["cluster"]
ca = cluster["certificate-authority-data"]
original_name = cluster.get("tls-server-name") or urllib.parse.urlsplit(cluster["server"]).hostname
token = pathlib.Path(sys.argv[1]).read_text().strip()
assert ca and original_name and token
config = {
    "apiVersion": "v1", "kind": "Config",
    "clusters": [{"name": "kind-test", "cluster": {
        "server": "https://host.openshell.internal:18443",
        "certificate-authority-data": ca,
        "tls-server-name": original_name,
    }}],
    "users": [{"name": "sandbox-test", "user": {"token": token}}],
    "contexts": [{"name": "kind-test", "context": {
        "cluster": "kind-test", "user": "sandbox-test", "namespace": "osac",
    }}],
    "current-context": "kind-test",
}
json.dump(config, sys.stdout)
' "$STATE_DIR/token.tmp" > "$STATE_DIR/config.next"
chmod 600 "$STATE_DIR/config.next"
if command -v getenforce >/dev/null 2>&1 && [ "$(getenforce)" != Disabled ]; then
  chcon --reference="$STATE_DIR/kube" "$STATE_DIR/config.next"
fi
mv -f "$STATE_DIR/config.next" "$STATE_DIR/kube/config"
rm -f "$STATE_DIR/token.tmp"
)
```

Check that `tls-server-name` is a name actually covered by the API server's
certificate; the original kubeconfig server name is normally suitable when
that admin connection already validates TLS. Do not use
`insecure-skip-tls-verify`. The API server may grant a shorter lifetime than
requested. Refresh the token and kubeconfig when it expires, using a new file
and an atomic replacement in the dedicated directory. The read-only volume
mount then sees the new file without rebuilding the image.
On an SELinux host, the `chcon` step gives the replacement file the same
container-readable label as the mounted directory before the atomic rename.
Without it, a later token refresh can leave the directory visible inside the
sandbox while access to `config` fails with `Permission denied`.

If you created or refreshed the kubeconfig before this instruction was added
and `make enter-carb` reports that the mount is unreadable, repair the label
on the host and retry the same `make enter-carb` command:

```bash
chcon --reference="$STATE_DIR/kube" "$STATE_DIR/kube/config"
```

This changes only the file's SELinux label; it does not print or alter the
credential. Check the directory and file ownership/modes as described above
if the entry check still fails.

For subsequent renewals, review the host script once and install a copy
outside the sandbox-mounted checkout:

```bash
install -d -m 700 "$HOME/.local/libexec"
install -m 700 tools/carbonite/refresh-kind-kubeconfig.py \
  "$HOME/.local/libexec/osac-refresh-kind-kubeconfig"
```

Run this one command on the host before the token expires, or whenever
`kubectl` inside the sandbox reports `Unauthorized`:

```bash
"$HOME/.local/libexec/osac-refresh-kind-kubeconfig"
```

It requests a 12-hour token, reports the lifetime Kubernetes actually
granted, and replaces the scoped kubeconfig atomically with the correct
SELinux label. The running sandbox sees the new file through its existing
read-only mount; no restart or rebuild is needed. The installed script is
outside the mount, so the sandbox cannot edit the host command. If the
script's source changes later, review it again before reinstalling.

## Select the network policy and create the sandbox

Copy `tools/carbonite/policy.yaml` to a host-only path outside the checkout,
then add a KiND-only rule under its `network_policies:` map. Run this from the
repository root in the same host shell used above:

```bash
STATE_DIR="${XDG_STATE_HOME:-$HOME/.local/state}/osac-carbonite-kind"
install -d -m 700 "$STATE_DIR"
cp tools/carbonite/policy.yaml "$STATE_DIR/policy-kind.yaml"
chmod 600 "$STATE_DIR/policy-kind.yaml"
python3 - "$STATE_DIR/policy-kind.yaml" <<'PY'
from pathlib import Path
import sys

path = Path(sys.argv[1])
policy = path.read_text()
anchor = "network_policies:\n"
assert policy.count(anchor) == 1 and "  kind_api:\n" not in policy
rule = """  kind_api:
    name: kind-api
    endpoints:
      - host: host.openshell.internal
        port: 18443
        protocol: tcp
        tls: skip
    binaries:
      - path: /usr/local/bin/kubectl
"""
path.write_text(policy.replace(anchor, anchor + rule, 1))
PY
```

The copied policy keeps deployment-internal endpoints as reserved `.example`
placeholder hosts (the `gitlab`, `jira`, and `rh_pattern_server` rules).
Substitute them with the deployment's real endpoints — or remove rules you
do not need — before creating the sandbox:

```bash
sed -i \
  -e 's/gitlab\.internal\.example/<internal-gitlab-host>/' \
  -e 's/jira\.internal\.example/<internal-jira-host>/' \
  -e 's/patterns\.internal\.example/<internal-pattern-server-host>/' \
  "$STATE_DIR/policy-kind.yaml"
```

Keep the substituted policy outside the repository and never commit it: it
contains the deployment's internal endpoints.

Review `$STATE_DIR/policy-kind.yaml` before creating the sandbox. The `tls:
skip` policy setting delegates certificate verification to `kubectl` and its
scoped kubeconfig; do not disable verification in the kubeconfig.

The exact hostname, port, and binary limit outbound Kubernetes API access;
Kubernetes RBAC limits what the mounted identity can do after connecting.
Keep the active policy outside the checkout so a sandbox edit cannot change
the policy that the host later submits. A policy change or initial kubeconfig
mount requires sandbox recreation. The dedicated credential directory is
attached through an approved Podman named volume because this gateway rejects
direct bind mounts under resource admission. The volume is mounted read-only
at `/sandbox/kube`; the sandbox receives `KUBECONFIG=/sandbox/kube/config` at
creation. `make enter-carb` and `make claude-carb` check that this path is
readable and read-only inside the selected sandbox before opening a session.
The volume's admission label scopes it to an OpenShell workspace,
not to a single sandbox. Use the dedicated `osac-kind` workspace for this
cluster-enabled sandbox. The default Carbonite configuration mounts the
scoped kubeconfig and uses the host-only cluster policy.

Create that workspace once from the host; `--workspace osac-kind` does not
create it automatically. If it already exists, inspect its membership before
placing this credential volume there. With a policy copied and reviewed at
`$STATE_DIR/policy-kind.yaml`, run:

```bash
openshell -g openshell workspace create --name osac-kind
make enter USE_CARBONITE=true
```

The defaults select `osac-carbonite-kind`, the `osac-kind` workspace, and the
host-only policy and kubeconfig under `$STATE_DIR`. The explicit
`make enter-carb` form remains supported.

If this cluster-enabled sandbox already exists, stop work in it and use
`make rebuild-carb`. This replaces its container while
retaining its checkout volume; inspect container-local state before doing so. A stale
named kubeconfig volume with a different host source or approval labels is
rejected. Inspect and remove that volume explicitly before reusing its name.

## Start the host API relay

A KiND API usually listens on host loopback at a generated port, which the
sandbox cannot reach as `localhost`. The OpenShell Podman supervisor uses host
networking and mediates the sandbox's connections. Its
`host.openshell.internal` mapping may be a host interface address; the
Podman `openshell` bridge gateway is not necessarily the right bind address.

After `make enter-carb` creates the cluster-enabled sandbox, run this block on
the host. It finds the matching
supervisor for the default `osac-carbonite-kind` name, sets `RELAY_BIND_IP`
from its actual host alias, verifies that address is still assigned locally,
and starts a host user service forwarding only the KiND API port. If you chose another sandbox name, change
the `grep` pattern accordingly. The strict shell options stay inside the
parentheses, so an error returns you to the host prompt:

```bash
(
set -euo pipefail
SANDBOX_CONTAINER="$(podman ps --format '{{.Names}}' | awk '/--osac-carbonite-kind-/ && !found { print; found = 1 }')"
test -n "$SANDBOX_CONTAINER"
SUPERVISOR="openshell-supervisor-${SANDBOX_CONTAINER##*--osac-carbonite-kind-}"
HOSTS_FILE="$(podman inspect "$SUPERVISOR" --format '{{.HostsPath}}')"
RELAY_BIND_IP="$(awk '{ for (i = 2; i <= NF; i++) if ($i == "host.openshell.internal") { print $1; exit } }' "$HOSTS_FILE")"
test -n "$RELAY_BIND_IP"
ip -o -4 addr show | awk -v ip="$RELAY_BIND_IP" \
  '{ split($4, address, "/"); if (address[1] == ip) found = 1 } END { exit !found }'
LOCAL_SOURCE="$(ip -4 route get "$RELAY_BIND_IP" | awk '{ for (i = 1; i < NF; i++) if ($i == "src") { print $(i+1); exit } }')"
test "$LOCAL_SOURCE" = "$RELAY_BIND_IP"
ADMIN_KUBECONFIG="$HOME/.kube/osac-dev-kind.kubeconfig"
KIND_API_URL="$(kubectl --kubeconfig "$ADMIN_KUBECONFIG" \
  config view --minify -o jsonpath='{.clusters[0].cluster.server}')"
KIND_API_PORT="$(python3 -c \
  'import sys, urllib.parse; print(urllib.parse.urlsplit(sys.argv[1]).port)' \
  "$KIND_API_URL")"
test "$KIND_API_PORT" != None
if systemctl --user is-active --quiet osac-kind-api-relay; then
  systemctl --user stop osac-kind-api-relay
fi
systemd-run --user --unit=osac-kind-api-relay --collect \
  --property=Restart=on-failure -- /usr/bin/socat \
  "TCP4-LISTEN:18443,bind=${RELAY_BIND_IP},range=${RELAY_BIND_IP}/32,reuseaddr,fork" \
  "TCP4:127.0.0.1:${KIND_API_PORT}"
systemctl --user is-active osac-kind-api-relay
ss -lnt '( sport = :18443 )'
)
```

If the address check fails after moving between networks, the existing
supervisor has a stale host alias. Recreate the sandbox/supervisor and recheck
before starting the relay. The `range` filter allows connections only from
the host's own source address. If the alias maps to a LAN interface, also
block inbound LAN traffic to port `18443` with the host firewall. Never bind
the relay to `0.0.0.0`. This transient user service survives closing the
terminal but must be started again after a reboot or a host address change.
Stop it with `systemctl --user stop osac-kind-api-relay` when cluster access is
no longer needed.
The relay passes Kubernetes TLS through unchanged; the scoped kubeconfig
created above verifies the cluster CA and original serving-certificate name.
OpenShell's [policy schema](https://docs.nvidia.com/openshell/v0.1.2/how-it-works/policies/schema)
blocks ordinary loopback destinations and control-plane ports, so this uses
the explicit host relay and a nonreserved port.

Inside the sandbox, verify the path, cluster connection, and limited identity:

```bash
printf '%s\n' "$KUBECONFIG"    # /sandbox/kube/config
kubectl --request-timeout=5s get --raw=/readyz
kubectl -n osac get pods -l control-plane=controller-manager,app.kubernetes.io/name=operator
kubectl auth can-i create computeinstances -n osac
kubectl auth can-i get secrets -n kube-system  # should say no
make -C osac-operator integration-tests
```

If `/readyz` returns `Unauthorized`, run the installed host renewal command
above. The ServiceAccount token is requested for 12 hours, but the cluster
may grant a shorter lifetime. If it returns `EOF`, check the
relay service and listener on the host, then verify that the supervisor's
host alias still points to the current address. A network change requires
updating `host_gateway_ip`, restarting the gateway, rebuilding the sandbox,
and restarting the relay.

### Recover after laptop sleep or a network change

First check that the host still reaches KiND and that the relay is running:

```bash
kubectl --kubeconfig "$HOME/.kube/osac-dev-kind.kubeconfig" \
  --request-timeout=5s get --raw=/readyz
systemctl --user is-active osac-kind-api-relay
ss -lnt '( sport = :18443 )'
```

If the host API is healthy but sandbox `kubectl` times out, the laptop may
have a new address while the existing supervisor and relay still use the old
one. Repeat the `host_gateway_ip` configuration block under **What the
developer must prepare on the host**. It restarts the gateway. Then recreate
the cluster-enabled sandbox from the host, using the same host-only policy and
kubeconfig directory:

```bash
make rebuild-carb
```

`rebuild-carb` replaces container-local state, so inspect it first if you
have installed tools or saved files outside the mounted checkout. Restart
the host relay with the block under **Start the host API relay**; an active
relay can still be listening on the previous address. If the host address
did not change and only the relay stopped, restart just the relay.

If sandbox `kubectl` instead reports `Unauthorized`, run the installed host
renewal command above. The scoped token can expire during sleep. Refreshing
it does not require a sandbox rebuild. Then
rerun `kubectl --request-timeout=5s get --raw=/readyz` inside the sandbox.

The Make target now fails promptly if the mounted kubeconfig is absent, the
API is unreachable, or no operator pod is found, instead of polling for five
minutes against `localhost:8080`. AAP's full integration target still manages
its own KiND lifecycle and needs separate host setup/teardown work; this guide
does not run it inside Carbonite. Two console-proxy negative cases currently
accept any `kubectl` error for a nonexistent resource, so an RBAC denial could
also satisfy them with this scoped identity. Review those cases separately
before treating their result as proof of the expected API error.
