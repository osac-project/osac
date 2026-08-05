# Fulfillment service

This project contains the code for the fulfillment service. For instructions on how to install it
in a production environment see the [installation guide](docs/INSTALL.md).

## Required development tools

To work with this project you will need the following tools:

- [Go](https://go.dev) - Used to build the Go code.
- [Buf](https://buf.build) - Used to generate Go code from gRPC specifications.
- [Ginkgo](https://onsi.github.io/ginkgo) - Used to run unit tests.
- [gomock](https://github.com/uber-go/mock) - Used to generate test mocks.
- [Kubectl](https://kubernetes.io/es/docs/reference/kubectl) - Used to deploy to an OpenShift cluster.
- [PostgreSQL](https://www.postgresql.org) - Used to store persistent state.
- [Podman](https://podman.io) - Used to build and run container images.
- [gRPCurl](https://github.com/fullstorydev/grpcurl) - Used to test the gRPC API from the CLI.
- [curl](https://curl.se) - Used to test the REST API from the CLI.
- [jq](https://jqlang.org) - Used by some of the commands in this document.
- [kind](https://kind.sigs.k8s.io) - Used to create Kubernetes clusters for integration tests.
- [helm](https://helm.sh/) - Used by default to deploy the service during integration tests.
- [Python](https://www.python.org) - Used to run the `dev.py` script for development tasks like linting.
- [uv](https://docs.astral.sh/uv) - Used to run the `dev.py` script without manually managing Python dependencies.

See [dev/README.md](dev/README.md) for more information about the `dev.py` script and how to extend
it with new commands.

## Building the binaries

The project contains two binaries: the service and the CLI.

To build the `fulfillment-service` binary:

```bash
$ go build ./cmd/fulfillment-service
```

To build the `osac` binary:

```bash
$ go build ./cmd/osac
```

## Running unit tests

To run the unit tests of the internal packages:

```bash
$ ginkgo run -r internal
```

This avoids running the integration tests (in the `it` package), which can take a long time. To
run all tests, including integration tests:

```bash
$ ginkgo run -r
```

## Running PostgreSQL

To quickly run a local postgresql database in a container, run the following command:

```
podman run -d --name postgresql_database \
  -e POSTGRESQL_USER=user -e POSTGRESQL_PASSWORD=pass -e POSTGRESQL_DATABASE=db \
  -p 127.0.0.1:5432:5432 quay.io/sclorg/postgresql-18-c10s:latest
```

Done!

Or if you prefer to install and run postgresql directly on your development
system, you'll need to create a database for the service. For example, assuming
that you already have administrator access to the database, you can create a
user `user` with password `pass` and a database `db` with the following
commands:

    postgres=# create user user with password 'pass';
    CREATE ROLE
    postgres=# create database db owner user;
    CREATE DATABASE
    postgres=#

## Running the fulfillment-service

To run the the gRPC server use a command like this:

    $ ./fulfillment-service start grpc-server \
    --log-level=debug \
    --log-headers=true \
    --log-bodies=true \
    --grpc-listener-address=localhost:8000 \
    --db-url=postgres://user:pass@localhost:5432/db

To run the the REST gateway use a command like this:

    $ ./fulfillment-service start rest-gateway \
    --log-level=debug \
    --log-headers=true \
    --log-bodies=true \
    --http-listener-address=localhost:8001 \
    --grpc-server-address=localhost:8000 \
    --grpc-server-plaintext

You may need to adjust the commands to use your database details.

To verify that the gRPC server is working use `grpcurl`. For example, to list the available gRPC services:

    $ grpcurl -plaintext localhost:8000 list
    osac.public.v1.ClusterOrders
    osac.public.v1.ClusterTemplates
    osac.public.v1.Clusters
    osac.public.v1.Events
    grpc.reflection.v1.ServerReflection
    grpc.reflection.v1alpha.ServerReflection

To list the methods available in a service, for example in the `ClusterTemplates` service:

    $ grpcurl -plaintext localhost:8000 list osac.public.v1.ClusterTemplates
    osac.public.v1.ClusterTemplates.Get
    osac.public.v1.ClusterTemplates.List

To invoke a method, for example the `List` method of the `ClusterTemplates` service:

    $ grpcurl -plaintext localhost:8000 osac.public.v1.ClusterTemplates/List
    {
      "size": 2,
      "total": 2,
      "items": [
        {
          "id": "my-template",
          "title": "My template",
          "description": "My template is *nice*."
        },
        {
          "id": "your-template",
          "title": "Your template",
          "description": "Your template is _ugly_."
        }
      ]
    }

To verify that the REST gateway is working use `curl`. For example, to get the list of templates:

    $ curl --silent http://localhost:8001/api/fulfillment/v1/cluster_templates | jq
    {
      "size": 2,
      "total": 2,
      "items": [
        {
          "id": "my-template",
          "title": "My template",
          "description": "My template is *nice*."
        },
        {
          "id": "your-template",
          "title": "Your template",
          "description": "Your template is _ugly_."
        }
      ]
}

## Building the container image

Select your image name, for example `quay.io/myuser/fulfillment-service:latest`, then build and tag the image with a
command like this:

    $ podman build -t quay.io/myuser/fulfillment-service:latest .

To build the debug variant (includes the `dlv` debugger and disables compiler optimisations), use the
`runtime-debug` target:

    $ podman build --build-arg DEBUG=true --target runtime-debug -t quay.io/myuser/fulfillment-service:latest .

If you want to deploy to an OpenShift cluster then you will also need to push the image, so that the cluster can pull
it:

    $ podman push quay.io/myuser/fulfillment-service:latest

## Running integration tests

The project includes integration tests that run against a real Kubernetes cluster created using
[kind](https://kind.sigs.k8s.io). These tests verify the end-to-end functionality of the fulfillment
service by deploying it to a temporary cluster and exercising the APIs.

The integration tests use TLS with SNI (_Server Name Indication_) routing through the Envoy Gateway.
This means that the services are accessed using their Kubernetes internal host names, but routed
through `127.0.0.1:8000` which is exposed by the Kind cluster.

For the tests to work correctly, the following host names must resolve to `127.0.0.1`:

- `keycloak.keycloak.svc.cluster.local` - The Keycloak identity provider used for authentication.
- `fulfillment-api.osac.svc.cluster.local` - The fulfillment service external API.
- `fulfillment-internal-api.osac.svc.cluster.local` - The fulfillment service internal API.

Add the following entries to your `/etc/hosts` file:

```text
127.0.0.1 keycloak.keycloak.svc.cluster.local
127.0.0.1 fulfillment-api.osac.svc.cluster.local
127.0.0.1 fulfillment-internal-api.osac.svc.cluster.local
```

To run the integration tests:

```bash
$ ginkgo run it
```

The integration tests will automatically:

1. Create a kind cluster named "fulfillment-service-it".
2. Build and load the container image.
3. Deploy the fulfillment service.
4. Run all test cases.
5. Clean up the kind cluster.

### Preserving the test cluster

By default, the kind cluster is deleted after the tests complete. If you want to preserve the cluster
for debugging or manual inspection, you can set the `IT_KEEP_KIND` environment variable:

```bash
$ IT_KEEP_KIND=true ginkgo run it
```

When `IT_KEEP_KIND=true`, the cluster will remain running after the tests finish, allowing you to:
- Inspect the deployed resources with `kubectl`.
- Debug test failures manually.
- Examine logs and cluster state.

The `setup` label can be combined with this to get a fresh integration environment where you can then
run your manual tests:

```bash
$ IT_KEEP_KIND=true ginkgo run --label-filter setup it
```

That will create the Kind cluster, install the dependencies and deploy the application, but will not
run any actual test.

To clean up a preserved cluster manually:

```bash
$ kind delete cluster --name fulfillment-service-it
```

### Secret for passwords and credentials

The integration tests use a single secret in all places where passwords or secrets are needed, such
as service account client secrets and user passwords. By default, a random secret is generated. If
you want to use a known value, for example to log in with the CLI afterwards, you can set the
`IT_SECRET` environment variable:

```bash
$ IT_KEEP_KIND=true IT_SECRET=my-secret ginkgo run --label-filter setup it
```

The secret used to run the integration tests is saved to the `random` secret inside the `default`
namespace. This can be useful if you didn't use the `IT_SECRET` environment variable, but still
want to use the secret. You can get it like this:

```bash
$ kubectl get secret -n default random -o json | jq -r '.data["secret"] | @base64d'
```

### Custom CA certificate

By default, each integration test run generates a fresh CA private key and certificate. This means
that the CA changes every time the cluster is recreated, which forces you to either re-extract the
CA bundle and pass it to `osac login --ca-file`, or use `--insecure` to skip verification.

To avoid this, you can provide your own pre-generated CA files via the `IT_CA_KEY` and `IT_CA_CRT`
environment variables. Both must be set together and must point to PEM-encoded files. When provided,
the integration tests will use them instead of generating a new CA. This allows you to configure the
CA once in your browser or pass it to `osac login --ca-file` without having to update it after every
run.

To generate a CA key and certificate with `openssl`:

```bash
openssl req \
-x509 \
-newkey rsa:2048 \
-nodes \
-keyout ca.key \
-out ca.crt \
-days 365 \
-subj "/CN=Default CA" \
-addext "keyUsage=critical,keyCertSign,cRLSign" \
-addext "basicConstraints=critical,CA:TRUE"
```

Then run the integration tests pointing to those files:

```bash
export IT_KEEP_KIND=true
export IT_CA_KEY=ca.key
export IT_CA_CRT=ca.crt
ginkgo run -v it
```

Note that `-nodes` flag in the `openssl` command above means `ca.key` (referenced by `IT_CA_KEY`) is
not passphrase-protected. This is fine for local development, but as a good habit consider setting
restrictive permissions (`chmod 600 ca.key`) and avoiding committing it to version control. If the
key is ever shared accidentally, simply regenerate both files and recreate the cluster.

As long as you reuse the same files across runs, the CA will remain stable and you can use the
certificate directly with the CLI:

```bash
osac login --ca-file ca.crt ...
```

### Login to the integration tests environment

Once the cluster is running, you can log in using the credentials flow:

```bash
osac login \
--ca-file ca.crt \
--flow credentials \
--client-id osac-admin \
--client-secret my-secret \
--private \
https://fulfillment-internal-api.osac.svc.cluster.local:8000
```

The same secret is shared by all service accounts and users.

The `--ca-file` flag should point to a file containing the trusted CA certificates. If you used the
`IT_CA_CRT` environment variable, you can point directly to that file. Otherwise, the CA bundle is
stored in the `ca-bundle` ConfigMap created by _trust-manager_. You can extract it with:

```bash
kubectl get configmap ca-bundle -n osac -o json | jq -r '.data["bundle.pem"]' > ca.crt
```

### Debugging in the integration tests environment

In the integration tests environment you can use the usual Kubernetes tools and logs for
debugging, but you can also set the `IT_DEBUG` environment variable to `true`. That will add the
`dlv` debugger to the container image, use it to run the binaries of the gRPC server, the REST
gateway and the controller, and expose the debugger on the following ports:

| Component    | Port  |
|--------------|-------|
| gRPC server  | 30001 |
| REST gateway | 30002 |
| Controller   | 30003 |

For example, to connect to the gRPC server debugger from Visual Studio Code, add the following
configuration to your `.vscode/launch.json` file:

```json
{
        "name": "attach grpc-server",
        "type": "go",
        "request": "attach",
        "mode": "remote",
        "host": "127.0.0.1",
        "port": 30001
}
```

If you use a different development environment, you can connect directly with `dlv` from the
command line:

```bash
$ dlv connect 127.0.0.1:30001
```
