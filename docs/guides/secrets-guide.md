# Managing Secrets

OSAC Secrets hold credentials and other sensitive data for resources such as
clusters and compute instances. OSAC stores Secret data in its configured
secret store, so these commands do not create Kubernetes Secrets directly.

You need the `osac` CLI and an authenticated session (`osac login`). Under the
current default policy, tenant users can manage any tenant-level Secrets there.
Only store credentials there if other members of that tenant may read them.
Cloud provider administrators can also manage Secrets in the built-in `shared`
tenant. For secret-store deployment, see
[Secrets Management Configuration](admin/secrets-management.md).

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
