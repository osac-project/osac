# Billing reader: the worked example

This is the small reader from the metering event contract. It reads one
tenant's usage topic under a reader grant, drops repeated CloudEvent ids,
sums every quantity per resource, meter, component, and billing dimensions,
and prints the billable total.

It is an example, not a production adapter. Its deduplication state lives in
memory, so a restart forgets the ids it has seen. It reads one topic and
keeps no retry state of its own.

## What it proves

The reading rule is one sentence: add every usage event, and a correction is a
negative one. The example's unit tests replay the design's running example:
tenant `acme`, VM `vm-7` of instance type `small` in project `web`, with 20 s
at 10:01, 60 s at 10:02, 60 s at 10:03, and a -30 s correction when the VM
stopped at 10:02:30. The total is 110 seconds.

## Grant the reader

Add the reader to the platform values, with one entry per tenant:

```yaml
metering:
  readers:
    - name: billing-reader
      tenant: acme
```

Apply the change with the installer's supported path, which builds the chart
dependencies and installs the platform layer:

```sh
make -C osac-installer install-osac PLATFORM=openshift PROFILE=full-ci NS=osac
```

Use the platform and profile of the deployment. A direct `helm upgrade
osac-installer/charts/osac` also works, but only after `make -C osac-installer
helm-deps` has built the umbrella chart's dependencies.

Strimzi issues the password into the Secret named after the reader in the
Kafka namespace. Hand the password, the shared OSAC CA, and the bootstrap
address to the reader's owner out of band. The repository holds none of them.

## Run it

The connection values come from the environment. Run the example in the
cluster, so the bootstrap address resolves and TLS verifies against the shared
CA. Build the binary into an image first (`go build -o bin/billing-reader
./examples/billing-reader`).

Strimzi creates the password Secret in `osac-kafka`, and the shared `ca-bundle`
ConfigMap exists in the release namespace (`osac`). Copy the password into the
release namespace so one Job can mount both:

```sh
oc get secret billing-reader -n osac-kafka -o jsonpath='{.data.password}' \
  | base64 -d \
  | oc create secret generic billing-reader --from-file=password=/dev/stdin -n osac
```

Then run a Job in `osac` that mounts that Secret and the CA ConfigMap:

```yaml
apiVersion: batch/v1
kind: Job
metadata:
  name: billing-reader-example
  namespace: osac
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
            - name: KAFKA_TOPIC
              value: osac.metering.heartbeat.acme
            - name: KAFKA_CONSUMER_GROUP
              value: billing-reader
            - name: KAFKA_SASL_USERNAME
              value: billing-reader
            - name: KAFKA_SASL_PASSWORD_FILE
              value: /etc/reader/password
            - name: KAFKA_TLS_CA_CERT
              value: /etc/ca/bundle.pem
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
kubectl logs job/billing-reader-example -n osac
oc delete secret billing-reader -n osac
```

From a host that can reach the broker, export the same variables and run:

```sh
cd osac-metering/adapters
go run ./examples/billing-reader
```

The reader prints one line per billable total, then one total per unit:

```text
RESOURCE TYPE      RESOURCE  METER    COMPONENT  DIMENSIONS                                               QUANTITY    UNIT
compute_instance   vm-7      compute  compute    boot_disk_size_gib=30,deployment=example,image=rhel-9, 110.000000  second
                                                  instance_type=small,project=web,tenant=acme
BILLABLE TOTAL: 110.000000 second
```

It stops after `READER_IDLE_TIMEOUT` (default 30 seconds) without a message,
or on Ctrl-C. If it meets an event it cannot decode or does not know, it logs
the error and exits non-zero without marking that event, so the next run reads
it again.

## Revoke the reader

Remove the entry and run the installer again. Strimzi deletes the `KafkaUser`,
its password Secret, and its ACLs. Any consumer-group error stops the example:
it logs the error, prints the totals it has, and exits non-zero. That includes
the authorization error a revoked reader gets on its next fetch. Nothing in
OSAC restarts.
