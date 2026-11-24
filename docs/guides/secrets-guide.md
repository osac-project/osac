# Managing Secrets

OSAC Secrets hold credentials and other sensitive data for resources such as
clusters and compute instances. OSAC stores Secret data in its configured
secret store, so these commands do not create Kubernetes Secrets directly.

You need the `osac` CLI and an authenticated session (`osac login`). Under the
current default policy, tenant users can manage any tenant-level Secrets there.
Only store credentials there if other members of that tenant may read them.
Cloud provider administrators can also manage Secrets in the built-in `shared`
tenant. SSH public-key Secrets cannot be created in that tenant. For
secret-store deployment, see
[Secrets Management Configuration](installation/secrets-management-configuration.md).

## Create and use a Secret

In this example we will create a pull secret typed Secret from an existing
container registry credentials file.

```bash
osac create secret --name cluster-pull-secret --type=pull-secret \
  --from-file=.dockerconfigjson=./pull-secret.json
```

The `pull-secret` type requires the `.dockerconfigjson` key. The CLI reads the
file contents as the value; `--from-file=KEY=PATH` lets you choose the key
independently of the filename. Other consumers require different types and
keys; See the cli help command for more info.

List Secrets and inspect one without displaying its value:

```bash
osac get secrets
osac describe secret cluster-pull-secret
```

To use the pull Secret, choose a published cluster catalog item that permits a
user-supplied pull Secret:

```bash
osac create cluster --catalog-item example-cluster --name my-cluster \
  --pull-secret cluster-pull-secret
```

The Secret must be in the cluster's tenant and have the `pull-secret` type.
To replace its contents with an updated credentials file, run:

```bash
osac update secret cluster-pull-secret \
  --from-file=.dockerconfigjson=./new-pull-secret.json
```

`update secret` replaces the complete data map. Supply every key you want to
keep. User-data Secret contents cannot be updated after creation.
Delete an unused Secret with `osac delete secret cluster-pull-secret`; OSAC
rejects deletion while a resource still references it.

## Secret Types

OSAC supports multiple secret types, each designed for specific use cases. Some types
require specific data keys, which are validated when creating or updating the secret.

### Pull Secret (`pull-secret`)

**Purpose:** Store container registry credentials for pulling images from private registries.

**Required key:** `.dockerconfigjson`

**Used by:** Cluster provisioning

**Format:** Docker config JSON format (same as Kubernetes `dockerconfigjson` secret)

#### Example: Create from existing Docker config

```bash
# Create from your local Docker config file
osac create secret --name my-pull-secret \
  --type=pull-secret \
  --from-file=.dockerconfigjson=$HOME/.docker/config.json
```

#### Example: Create from scratch

```bash
# Generate a Docker config JSON for a single registry
cat > pull-secret.json <<EOF
{
  "auths": {
    "quay.io": {
      "auth": "$(echo -n 'username:password' | base64)"
    }
  }
}
EOF

osac create secret --name quay-pull-secret \
  --type=pull-secret \
  --from-file=.dockerconfigjson=./pull-secret.json

# Clean up the local file
rm pull-secret.json
```

#### Example: Multiple registries

```bash
cat > multi-registry-pull-secret.json <<EOF
{
  "auths": {
    "quay.io": {
      "auth": "$(echo -n 'quay-user:quay-pass' | base64)"
    },
    "registry.redhat.io": {
      "auth": "$(echo -n 'rh-user:rh-pass' | base64)"
    },
    "docker.io": {
      "auth": "$(echo -n 'docker-user:docker-pass' | base64)"
    }
  }
}
EOF

osac create secret --name all-registries-pull-secret \
  --type=pull-secret \
  --from-file=.dockerconfigjson=./multi-registry-pull-secret.json

rm multi-registry-pull-secret.json
```

**Usage:** Reference the secret when creating clusters or compute instances that pull
from private registries.

---

### Kubeconfig (`kubeconfig`)

**Purpose:** Store Kubernetes cluster access credentials and configuration.

**Required key:** `kubeconfig`

**Used by:** Tools and services that need to access Kubernetes clusters programmatically

**Format:** Standard Kubernetes kubeconfig YAML

#### Example: Create from existing kubeconfig

```bash
# Create from your local kubeconfig
osac create secret --name my-cluster-kubeconfig \
  --type=kubeconfig \
  --from-file=kubeconfig=$HOME/.kube/config
```

#### Example: Create from a cluster's kubeconfig

```bash
# Get the cluster ID
CLUSTER_ID=$(osac get clusters --filter 'this.metadata.name == "my-cluster"' -o json | jq -r '.[0].id')

# Get the Secret name from the cluster status, then retrieve and decode it
KUBECONFIG_SECRET=$(osac get cluster "$CLUSTER_ID" -o json | jq -r '.status.kubeconfig_secret.name')
osac get secret "$KUBECONFIG_SECRET" -o json \
  | jq -r '.data.kubeconfig' \
  | base64 --decode > cluster-kubeconfig.yaml

# Store it as a secret
osac create secret --name my-cluster-access \
  --type=kubeconfig \
  --from-file=kubeconfig=./cluster-kubeconfig.yaml

# Clean up the local file
rm cluster-kubeconfig.yaml
```

#### Example: YAML format

```yaml
"@type": type.googleapis.com/osac.public.v1.Secret
metadata:
  name: prod-cluster-kubeconfig
  tenant: my-tenant
type: SECRET_TYPE_KUBECONFIG
data:
  # Base64-encoded kubeconfig YAML
  kubeconfig: YXBpVmVyc2lvbjogdjEKY2x1c3RlcnM6Ci0gY2x1c3RlcjoKICAgIGNlcnRpZmljYXRlLWF1dGhvcml0eS1kYXRhOiAuLi4K
```

**Usage:** Services or automation tools that need programmatic access to deployed
clusters.

---

### User Data

User-data Secrets store instance initialization content. Both types require a
nonempty `userdata` key. Use `--from-file=userdata=PATH` to supply the file
contents directly, without base64 encoding them for the CLI. Without an explicit
`userdata=` key, the CLI uses the filename as the data key.

<a id="user-data-user-data"></a>

#### Linux (`user-data`)

**Purpose:** Store Linux initialization data, such as cloud-init configuration
or shell scripts.

**Used by:** Compute instance and bare metal instance provisioning

**Format:** Typically cloud-init YAML or a shell script supported by the target
image. Secret creation checks that `userdata` is nonempty, without validating
its format. The consuming resource applies its own checks. See the
[Windows subsection](#windows-user-data-windows-user-data) for Windows answer-file
XML validation at Secret creation and current availability.

##### Example: Cloud-init script

```bash
cat > cloud-init.yaml <<'EOF'
#cloud-config
users:
  - name: admin
    sudo: ALL=(ALL) NOPASSWD:ALL
    groups: sudo
    shell: /bin/bash
    ssh_authorized_keys:
      - ssh-rsa fake... user@host

packages:
  - curl
  - vim
  - git

runcmd:
  - echo "Instance initialized" > /var/log/init.log
EOF

osac create secret --name vm-cloud-init \
  --type=user-data \
  --from-file=userdata=./cloud-init.yaml

rm cloud-init.yaml
```

##### Example: Shell script user data

```bash
cat > init-script.sh <<'EOF'
#!/bin/bash
set -e

# Install custom software
yum install -y nginx
systemctl enable nginx
systemctl start nginx

# Configure firewall
firewall-cmd --permanent --add-service=http
firewall-cmd --reload

echo "Setup complete"
EOF

osac create secret --name nginx-init \
  --type=user-data \
  --from-file=userdata=./init-script.sh

rm init-script.sh
```

##### Example: YAML format

```yaml
"@type": type.googleapis.com/osac.public.v1.Secret
metadata:
  name: linux-userdata
  tenant: my-tenant
type: SECRET_TYPE_USER_DATA
data:
  # Base64-encoded cloud-init configuration
  userdata: I2Nsb3VkLWNvbmZpZwpwYWNrYWdlczoKICAtIGN1cmwK
```

**Usage:** Attach to a compute instance or bare metal instance to initialize
it on first boot. Data is immutable after creation; create a new Secret to
use different initialization data. See the [ComputeInstance guide](developer/computeinstance-guide.md#user-data-inline-or-secret)
for the current Windows admission rules.

<a id="windows-user-data-windows-user-data"></a>

#### Windows (`windows-user-data`)

**Purpose:** Store Windows answer-file XML for instance initialization.

**Availability:** Secret creation and retrieval are supported. Referencing this
type from a ComputeInstance is not yet supported; that work is tracked in
[OSAC-7713](https://redhat.atlassian.net/browse/OSAC-7713). For current Windows
ComputeInstances, use inline XML or a `user-data` Secret; see the
[ComputeInstance guide](developer/computeinstance-guide.md#user-data-inline-or-secret).

**Format:** One well-formed XML 1.0 document encoded as UTF-8. The `userdata`
value must be at most 65,536 bytes (64 KiB), including any byte-order mark
(BOM), declaration, and whitespace. The limit applies to the raw XML bytes,
before base64 encoding for JSON or YAML.

An XML declaration is optional; if present, it must specify version `1.0`.
A declared encoding must be `UTF-8` (case-insensitive). A UTF-8 BOM is accepted
and preserved. Other encodings are rejected without conversion.

DTDs and other `<!...>` declarations are prohibited, except comments and
CDATA sections. Processing instructions are allowed. The validator does not
fetch external resources or expand external entities. OSAC does not require
a particular root name or validate namespace conformance, Microsoft schemas,
or Windows settings.
Successful validation does not guarantee a working Windows answer file.

##### Example: Create from an answer file

```bash
osac create secret --name windows-answer \
  --type=windows-user-data \
  --from-file=userdata=/path/to/Unattend.xml

osac describe secret windows-answer
```

The `describe` command shows metadata and data-key sizes, not the XML content.

##### Restrictions and troubleshooting

Data and type are immutable after creation. Create a new Secret to use
different XML; metadata updates remain supported. The private API's `HUB`
backend is not supported for this type.

Validation failures return `InvalidArgument` without including submitted XML
or parser tokens. A rejected Secret is not stored.

| Error | What to check |
|---|---|
| Missing or empty `userdata` | Use `--from-file=userdata=PATH` with a nonempty file. |
| Exceeds 65,536 bytes | Check the file's byte size, including the BOM and whitespace. |
| Unsupported encoding | Save the file as UTF-8 and remove or correct a conflicting encoding declaration. Changing the declaration alone does not convert the file. |
| Prohibited XML directive | Remove DTDs and other prohibited declarations; do not rely on custom entities. |
| Not well-formed XML | Check for one complete root element, matching tags, quoted attributes, valid references, and an XML 1.0 declaration if present. |

OSAC preserves the exact submitted bytes, including any UTF-8 BOM. API List
and mutating responses omit Secret data; an authorized API Get returns it.
Likewise, CLI `create` and `describe` output omit the values, but
`osac get secret windows-answer -o yaml` (or `-o json`) includes them as base64
strings. Base64 is an encoding, not redaction; treat that output as sensitive.

---

### Opaque (`opaque`)

**Purpose:** Store arbitrary key-value pairs with no required schema.

**Required keys:** None (flexible)

**Used by:** Custom applications, storage backends, API credentials, database passwords

**Format:** Any keys and values you define

#### Example: Database credentials

```bash
osac create secret --name postgres-creds \
  --type=opaque \
  --from-literal=username=fake \
  --from-literal=password='fake!' \
  --from-literal=host=postgres.example.com \
  --from-literal=port=5432 \
  --from-literal=database=production
```

#### Example: API keys and tokens

```bash
osac create secret --name external-api-creds \
  --type=opaque \
  --from-literal=api-key=fake \
  --from-literal=api-secret=fake \
  --from-literal=webhook-url=https://api.example.com/webhook
```

#### Example: TLS certificate and key

```bash
osac create secret --name app-tls \
  --type=opaque \
  --from-file=tls.crt=./server.crt \
  --from-file=tls.key=./server.key \
  --from-file=ca.crt=./ca-bundle.crt
```

#### Example: Configuration files

```bash
# Create a secret from multiple config files
osac create secret --name app-config \
  --type=opaque \
  --from-file=config.yaml=./app-config.yaml \
  --from-file=logging.conf=./logging.conf \
  --from-literal=environment=production
```

#### Example: YAML format

```yaml
"@type": type.googleapis.com/osac.public.v1.Secret
metadata:
  name: storage-backend-creds
  tenant: my-tenant
  project: platform-managed
type: SECRET_TYPE_OPAQUE
data:
  # All values are base64-encoded
  username: ZmFrZQo=
  password: ZmFrZQo=
  endpoint: aHR0cHM6Ly9zdG9yYWdlLmV4YW1wbGUuY29t
```

**Usage:** Most flexible type for any use case not covered by specific types. Common
for provider-managed secrets, application credentials, and custom integrations.

---

### Value (`value`)

**Purpose:** Store a single scalar value.

**Required key:** `value`

**Used by:** Simple tokens, single-value credentials, feature flags

**Format:** Single text or binary value

#### Example: API token

```bash
echo -n 'ghp_abc123xyz789token' | osac create secret --name github-token \
  --type=value \
  --from-file=value=/dev/stdin
```

#### Example: License key

```bash
osac create secret --name app-license \
  --type=value \
  --from-literal=value='XXXX-YYYY-ZZZZ-AAAA-BBBB'
```

#### Example: Webhook secret

```bash
osac create secret --name webhook-secret \
  --type=value \
  --from-literal=value='randomly-generated-webhook-secret-123'
```

#### Example: YAML format

```yaml
"@type": type.googleapis.com/osac.public.v1.Secret
metadata:
  name: oauth-client-secret
  tenant: my-tenant
type: SECRET_TYPE_VALUE
data:
  # Base64-encoded single value
  value: ZmFrZS10b2tlbgo=
```

**Usage:** When you need to store a single credential or token without additional
context. E.g. a client secret for an Identity Provider

---

### SSH Public Key (`ssh-public-key`)

**Purpose:** Store OpenSSH public keys for authentication.

**Required key:** `public_key`

**Used by:** Compute instances (for SSH access)

**Format:** OpenSSH public key format (same as `~/.ssh/id_rsa.pub`)

#### Example: Create from existing SSH key

```bash
osac create secret --name my-ssh-key \
  --type=ssh-public-key \
  --from-file=public_key=$HOME/.ssh/id_rsa.pub
```

#### Example: Create from literal value

```bash
osac create secret --name admin-ssh-key \
  --type=ssh-public-key \
  --from-literal=public_key='ssh-rsa AAAAB...com'
```

#### Example: Generate new SSH key and store

```bash
# Generate a new SSH key pair
ssh-keygen -t ed25519 -C "automation@example.com" -f ./automation-key -N ""

# Store the public key
osac create secret --name automation-ssh-key \
  --type=ssh-public-key \
  --from-file=public_key=./automation-key.pub

# Keep the private key secure locally, remove the public key
rm ./automation-key.pub
```

#### Example: YAML format

```yaml
"@type": type.googleapis.com/osac.public.v1.Secret
metadata:
  name: deploy-key
  tenant: my-tenant
type: SECRET_TYPE_SSH_PUBLIC_KEY
data:
  # Base64-encoded SSH public key
  public_key: c3NoLXJzYSBmYWtlCg==
```

**Usage:** Attach to compute instances or bare metal instances to enable SSH access
with specific keys. Multiple keys can be combined in a single secret (one per line).

---

## Secret Type Summary

| Type | CLI Flag | Required Key | Primary Use Case |
|------|----------|--------------|------------------|
| Pull Secret | `pull-secret` | `.dockerconfigjson` | Container registry authentication |
| Kubeconfig | `kubeconfig` | `kubeconfig` | Kubernetes cluster access |
| User Data | `user-data` | `userdata` | Linux/cloud-init or shell-style initialization |
| Windows User Data | `windows-user-data` | `userdata` | Windows answer-file XML |
| Opaque | `opaque` | (none) | General-purpose key-value storage |
| Value | `value` | `value` | Single scalar values |
| SSH Public Key | `ssh-public-key` | `public_key` | SSH authentication |

**Choosing the right type:**

- Use **typed secrets** (`pull-secret`, `kubeconfig`, `user-data`,
  `windows-user-data`, `ssh-public-key`) when an OSAC resource expects a
  specific data format
- Use **user-data** for Linux/cloud-init or shell-style initialization
- Use **windows-user-data** for Windows answer-file XML
- Use **opaque** for provider-managed secrets, storage backends, or multi-value credentials
- Use **value** for simple single-value tokens or API keys

## Provider pull Secrets for shared offerings

A cloud provider administrator can create a pull Secret in the `shared` tenant:

```bash
osac --tenant shared create secret --name shared-pull-secret \
  --type=pull-secret --from-file=.dockerconfigjson=./pull-secret.json
```

Set `spec_defaults.pull_secret_secret.name: shared-pull-secret` on a **shared
cluster template**, then publish a cluster catalog item that references that
template. A tenant user creates a cluster with `--catalog-item` and omits
`--pull-secret`; the cluster inherits the template's pull Secret. The user can
instead supply a pull Secret from their own tenant if the catalog item allows
it. Tenant users cannot read the shared Secret value or reference it directly.

The [Catalog Items guide](../../fulfillment-service/docs/CATALOG_ITEMS.md#shared-pull-secrets-in-cluster-templates)
shows the template field and explains the catalog rules.
