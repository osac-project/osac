# Fulfillment service

This project contains the code for the fulfillment service. For instructions on how to install it
in a production environment see the [installation guide](docs/INSTALL.md).

The API is defined using protocol buffers in the [`proto`](proto) directory. An OpenAPI
specification is generated automatically from those definitions and published as raw YAML at
[openapi/v3/public.yaml](https://osac-project.github.io/osac/openapi/v3/public.yaml).
The same documentation is also available with a more convenient UI at
[osac-project.github.io/osac](https://osac-project.github.io/osac/).

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
service by deploying it to that cluster and exercising the APIs.

```bash
# Create Kind cluster + deploy infrastructure
$ make -C ../osac-installer install-infra PLATFORM=kind PROFILE=dev NS=osac

# Build image, deploy fulfillment-service via osac chart, run tests
$ make -C ../osac-installer test PLATFORM=kind PROFILE=dev NS=osac SUITE=fulfillment

# Clean up
$ make -C ../osac-installer uninstall PLATFORM=kind PROFILE=dev NS=osac
```

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

Or, against a cluster where the fulfillment-service image is already deployed, run `ginkgo`
directly -- it only connects and exercises the suite, it does not build, load, or deploy anything:

```bash
$ cd fulfillment-service && ginkgo run it
```

### Secret for passwords and credentials

The integration tests use a single secret in all places where passwords or secrets are needed, such
as service account client secrets and user passwords. By default, a random secret is generated. If
you want to use a known value, for example to log in with the CLI afterwards, you can set the
`IT_SECRET` environment variable:

```bash
$ IT_SECRET=my-secret ginkgo run it
```

The secret used to run the integration tests is saved to the `random` secret inside the `default`
namespace. This can be useful if you didn't use the `IT_SECRET` environment variable, but still
want to use the secret. You can get it like this:

```bash
$ kubectl get secret -n default random -o json | jq -r '.data["secret"] | @base64d'
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

The `--ca-file` flag should point to a file containing the trusted CA certificates, extracted from
the `ca-bundle` ConfigMap created by _trust-manager_:

```bash
kubectl get configmap ca-bundle -n osac -o json | jq -r '.data["bundle.pem"]' > ca.crt
```
