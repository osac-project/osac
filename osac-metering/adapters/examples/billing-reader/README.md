# Billing reader: the worked example

This is the small reader from the metering event contract. It reads every
tenant's heartbeat topics under one reader grant, drops repeated CloudEvent
ids, sums every quantity per tenant, resource, meter, component, and billing
dimensions, and prints the billable totals.

It is an example, not a production adapter. Its deduplication state lives in
memory, so a restart forgets the ids it has seen, and it lists the heartbeat
topics once at startup, so a tenant created later is read on the next restart.
It keeps no retry state of its own.

## What it proves

The reading rule is one sentence: add every usage event, and a correction is a
negative one. The example's unit tests replay the design's running example:
tenant `acme`, VM `vm-7` of instance type `small` in project `web`, with 20 s
at 10:01, 60 s at 10:02, 60 s at 10:03, and a -30 s correction when the VM
stopped at 10:02:30. The total for `acme` is 110 seconds; a second tenant's
usage stays separate.

## Grant the reader

Add the reader to the values. The entry names the reader and no tenant, because
one reader covers every tenant. On the umbrella installer chart, set
`metering.readers`:

```yaml
metering:
  readers:
    - name: billing-reader
```

On the standalone `osac-metering` chart, set the top-level `readers` list:

```yaml
readers:
  - name: billing-reader
```

The example assumes the umbrella installer, so the grant is under
`metering.readers`.

Apply the change with the installer's supported path, which builds the chart
dependencies and installs the platform layer:

```sh
make -C osac-installer install-osac PLATFORM=openshift PROFILE=full-ci NS=osac
```

Use the platform and profile of the deployment. A direct `helm upgrade
osac-installer/charts/osac` also works, but only after `make -C osac-installer
helm-deps` has built the umbrella chart's dependencies.

### The one Secret and what it covers

Strimzi issues one password Secret, named after the reader, in the Kafka
namespace. It is the reader's only credential: the `KafkaUser` behind it can
read and describe the `osac.metering.heartbeat.` topic prefix and read the
consumer group named after the reader. That one Secret covers every tenant,
including tenants created after the grant. There is no per-tenant Secret, and
creating a tenant adds no chart entry and no handover step.

Because one credential reads every tenant, a leaked reader Secret exposes all
of them. Hand the password, the shared OSAC CA, and the bootstrap address to
the reader's owner out of band. The repository holds none of them.

## Run it

The connection values come from the environment. Run the example in the
cluster, so the bootstrap address resolves and TLS verifies against the shared
CA. Build the binary into an image first (`go build -o bin/billing-reader
./examples/billing-reader`).

Strimzi creates the password Secret in `osac-kafka`, and the shared `ca-bundle`
ConfigMap exists in the release namespace (`osac`). Run the Job in `osac-kafka`
so it mounts the password Secret where Strimzi created it, and copy only the
public CA ConfigMap into that namespace; the password never leaves
`osac-kafka`:

```sh
oc get configmap ca-bundle -n osac -o jsonpath='{.data.bundle\.pem}' \
  | oc create configmap ca-bundle --from-file=bundle.pem=/dev/stdin -n osac-kafka
```

Then save this Job as `/tmp/billing-reader-job.yaml`. It mounts the reader's
Secret and that CA ConfigMap:

```yaml
apiVersion: batch/v1
kind: Job
metadata:
  name: billing-reader-example
  namespace: osac-kafka
spec:
  template:
    spec:
      restartPolicy: Never
      containers:
        - name: reader
          image: example/billing-reader:dev
          env:
            - name: KAFKA_BROKERS
              value: osac-kafka-kafka-bootstrap.osac-kafka.svc.cluster.local:9093
            - name: KAFKA_CONSUMER_GROUP
              value: billing-reader
            - name: KAFKA_SASL_USERNAME
              value: billing-reader
            - name: KAFKA_SASL_PASSWORD_FILE
              value: /etc/reader/password
            - name: KAFKA_TLS_CA_CERT
              value: /etc/ca/bundle.pem
            - name: READER_START_OFFSET
              value: oldest
          volumeMounts:
            - name: credentials
              mountPath: /etc/reader
            - name: ca
              mountPath: /etc/ca
      volumes:
        - name: credentials
          secret:
            secretName: billing-reader
        - name: ca
          configMap:
            name: ca-bundle
```

```sh
oc apply -f /tmp/billing-reader-job.yaml
kubectl logs job/billing-reader-example -n osac-kafka
oc delete job billing-reader-example -n osac-kafka
oc delete configmap ca-bundle -n osac-kafka
```

From a host that can reach the broker, export the same variables and run:

```sh
cd osac-metering/adapters
go run ./examples/billing-reader
```

The reader lists the heartbeat topics by prefix at startup and consumes them
under the one group. It prints one line per billable total, then one total per
tenant and unit:

```text
TENANT  RESOURCE TYPE      RESOURCE  METER    COMPONENT  DIMENSIONS                                               QUANTITY    UNIT
acme    compute_instance   vm-7      compute  compute    boot_disk_size_gib=30,deployment=example,image=rhel-9, 110.000000  second
                                                        instance_type=small,project=web,tenant=acme
BILLABLE TOTAL acme: 110.000000 second
```

A tenant created after the reader started is not in the list this run reads.
Restart the reader to list the heartbeat topics again and pick it up.

It stops after `READER_IDLE_TIMEOUT` (default 30 seconds) without a message,
or on Ctrl-C. If it meets an event it cannot decode or does not know, it logs
the error and exits non-zero without marking that event or printing the
billable totals, so the next run reads it again.

## First run

`READER_START_OFFSET` chooses where a reader with no committed offset starts on
each heartbeat topic. The default, `oldest`, reads every topic from its first
retained record; because retention is indefinite by default, the first run
prints the billable total for every tenant's whole retained history, not just
the usage since the grant. Set it to `newest` to start at the end of every topic
and bill only usage published after the reader starts. A committed offset takes
precedence over either value on later runs.

## Revoke the reader

Remove the entry and run the installer again. Strimzi deletes the `KafkaUser`,
its password Secret, and its ACLs, so the reader loses access to every tenant at
once. Any consumer-group error stops the example: it logs the error and exits
non-zero without printing the billable totals, so a failed run is not mistaken
for a completed bill. That includes the authorization error a revoked reader
gets on its next fetch. Nothing in OSAC restarts.
