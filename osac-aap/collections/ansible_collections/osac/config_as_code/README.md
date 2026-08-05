# Ansible Collection - osac.config_as_code

This role defines the configuration as code of AAP to run the Ansible playbooks
that are part of OSAC.

## Credentials

Since AAP runs in an OpenShift cluster, we expect the required credentials to be
passed as environment variables. These environment variables are defined as
`Secrets` in OpenShift, and used by a container group.

We define 2 container groups in AAP, one used to reconcile the configuration of
AAP itself, and one used for cluster fulfillment operations.

### Config-as-code environment variables

In order to reconcile the configuration of AAP, the following environment
variables are used (injected from the `config-as-code-ig` secret or the pod
where applicable):

| Variable | Description | Default |
|----|----|----|
| `NAMESPACE` | OpenShift namespace (injected by the pod) | — |
| `AAP_INSTANCE_NAME` | Name of the AAP instance | `osac-aap` |
| `AAP_HOSTNAME` | URL of the AAP instance to be configured | `http://` + `AAP_INSTANCE_NAME` |
| `AAP_USERNAME` | Username to authenticate against AAP | `admin` |
| `AAP_PASSWORD` | Password to authenticate against AAP (injected from `osac-aap-admin-password` when running in-cluster) | — |
| `AAP_VALIDATE_CERTS` | Whether to validate the SSL certificate behind `AAP_HOSTNAME` (`true`/`false`) | `true` |
| `AAP_ORGANIZATION_NAME` | The AAP organization that should be created | `osac` |
| `AAP_PROJECT_NAME` | Name of the project to be created | value of `NAMESPACE` |
| `AAP_PREFIX` | Prefix used to create resources in AAP | `AAP_ORGANIZATION_NAME` |
| `AAP_PROJECT_GIT_URI` | Git repository URL for the AAP project | `https://github.com/osac-project/osac.git` |
| `AAP_PROJECT_GIT_BRANCH` | Git branch to use for the project | `main` |
| `AAP_PROJECT_ARCHIVE_URI` | Optional archive URL instead of git (e.g. tarball) | — |
| `AAP_EE_IMAGE` | Registry URL of the execution environment image | `ghcr.io/osac/osac-aap:latest` |
| `LICENSE_MANIFEST_PATH` | Path to the license manifest file to register the AAP instance ([Red Hat account](https://access.redhat.com/management/subscription_allocations)) | `/var/secrets/config-as-code-manifest/license.zip` |
| `REMOTE_CLUSTER_KUBECONFIG_SECRET_NAME` | Name of the secret holding the kubeconfig for the remote cluster (cluster fulfillment only) | — |
| `REMOTE_CLUSTER_KUBECONFIG_SECRET_KEY` | Key within that secret for the kubeconfig file | `kubeconfig` |
| `OSAC_PUBLISH_TEMPLATES_ENABLED` | Whether the periodic **publish-templates** schedule is enabled in Controller (`true`/`false`) | `true` |

These variables must be defined in a secret named `config-as-code-ig` in the
namespace where AAP is deployed.

The content of license manifest file must be set in a secret named
`config-as-code-manifest-ig` as `license.zip` in the namespace where AAP is
deployed.

Note: since the container group is configured to run in the same namespace as
the AAP instance, the admin credentials to log against AAP are injected into the
pod.

### Cluster fulfillment environment variables

Here we need to define all the credentials required by the cluster fulfillment
use case, e.g.:

- `AWS_*`: AWS credentials
- `OS_*`: OpenStack credentials
- ...whatever in needed
- `NETRIS_PASSWORD`: Netris password
- `SERVER_SSH_KEY`: Private key used to SSH into bare-metal servers via the bastion (base64-encoded)
- `SERVER_SSH_BASTION_KEY`: Private key used to SSH into the bastion host (base64-encoded)

These variables must be defined in a secret named: `cluster-fulfillment-ig` in
the namespace where AAP is deployed.

The cluster fulfillment needs to access the Kube API of the cluster it runs on,
so we expect a service account `osac-sa` to exists with enough rights.

### Compute instance operations environment variables

Compute instance (VMaaS) job templates run in the
`compute-instance-operations-ig` instance group. The default KubeVirt path
(`ocp_virt_vm`) uses the in-cluster `osac-sa` service account and does not
require credentials in a dedicated secret.

The pod spec optionally mounts a secret named
`compute-instance-operations-ig`. Create it only when a deployment-specific
compute template needs extra environment variables. OpenStack credentials are
not required for VMaaS; Neutron networking uses `network-fulfillment-ig`, and
cluster/ESI floating-IP workflows use `cluster-fulfillment-ig`.

The compute instance instance group also optionally mounts `storage-operations-ig`
for JIT tenant storage during VM provisioning, and may mount a remote cluster
kubeconfig when `REMOTE_CLUSTER_KUBECONFIG_SECRET_NAME` is configured.

## Deploy a local AAP installation using CRC

### Download and deploy CRC

CRC is basically OpenShift in a VM, you can download it from Red Hat Console:
https://console.redhat.com/openshift/downloads. You will need `oc` which is
downloadable from the same place.

Extract the package, put the binary in your path, start the cluster, and log as
`kubeadmin`:

    crc start
    oc login -u kubeadmin  https://api.crc.testing:6443

## Install AAP

Install the AAP operator (more information
[here](https://docs.redhat.com/en/documentation/red_hat_ansible_automation_platform/2.5/html/installing_on_openshift_container_platform/installing-aap-operator-cli_operator-platform-doc#install-cli-aap-operator_installing-aap-operator-cli)):

    cat << EOF > aap_install.yml
    ---
    apiVersion: v1
    kind: Namespace
    metadata:
      labels:
        openshift.io/cluster-monitoring: "true"
      name: aap
    ---
    apiVersion: operators.coreos.com/v1
    kind: OperatorGroup
    metadata:
      name: ansible-automation-platform-operator
      namespace: aap
    spec:
      targetNamespaces:
        - aap
    ---
    apiVersion: operators.coreos.com/v1alpha1
    kind: Subscription
    metadata:
      name: ansible-automation-platform
      namespace: aap
    spec:
      channel: 'stable-2.5-cluster-scoped'
      installPlanApproval: Automatic
      name: ansible-automation-platform-operator
      source: redhat-operators
      sourceNamespace: openshift-marketplace
    EOF

    oc apply -f aap_install.yml

### Deploy AAP

Create a new namespace and deploy an AAP instance using these manifests:

    cat << EOF > aap.yml
    ---
    apiVersion: v1
    kind: Namespace
    metadata:
      labels:
        openshift.io/cluster-monitoring: "true"
      name: fulfillment-aap
    ---
    apiVersion: aap.ansible.com/v1alpha1
    kind: AnsibleAutomationPlatform
    metadata:
      name: fulfillment
      namespace: fulfillment-aap
    spec:
      image_pull_policy: IfNotPresent
      controller:
        disabled: false
      eda:
        disabled: false
      hub:
        disabled: true
      lightspeed:
        disabled: true
    EOF

    oc apply -f aap.yml

### Apply OSAC configuration on AAP instance

#### Config-as-code configuration

Create the secrets required for the configuration of AAP:

    cat << EOF > config-as-code
    AAP_HOSTNAME=<your AAP hostname>
    AAP_VALIDATE_CERTS=true
    AAP_ORGANIZATION_NAME=<the organization name to be created in AAP>
    AAP_PROJECT_NAME=<the project name to be create in AAP>
    AAP_PREFIX=aap-prefix
    AAP_PROJECT_GIT_URI=<the git repository where your playbooks and rulebooks live>
    AAP_PROJECT_GIT_BRANCH=<the git branch to be tracked>
    AAP_EE_IMAGE=<the execution environment image>
    OSAC_FULFILLMENT_SERVICE_URI=<URI of the OSAC fulfillment service>
    OSAC_TEMPLATE_COLLECTIONS=<collections containing OSAC templates>
    EOF

    oc apply -f config-as-code -n fulfillment-aap
    oc create secret generic config-as-code-ig --from-env-file=config-as-code -n fulfillment-aap
    oc create secret generic config-as-code-manifest-ig --from-file=license.zip=/path/to/license.zip` -n fulfillment-aap

#### Fulfillment operations configuration

Create service account and secret required for the fulfillment operations, such
as AWS and OpenStack credentials:

    cat << EOF > osac_sa.yml
    ---
    apiVersion: v1
    kind: ServiceAccount
    metadata:
      name: osac-sa
    ---
    apiVersion: rbac.authorization.k8s.io/v1
    kind: ClusterRoleBinding
    metadata:
      name: osac-sa
    roleRef:
      apiGroup: rbac.authorization.k8s.io
      kind: ClusterRole
      name: cluster-admin
    subjects:
      - kind: ServiceAccount
        name: osac-sa
        namespace: fulfillment-aap
    EOF

    cat << EOF > fulfillment_creds
    AWS_ACCESS_KEY_ID=...
    AWS_SECRET_ACCESS_KEY=...
    AWS_REGION=...
    OS_AUTH_URL=...
    OS_PROJECT_NAME=...
    ...
    EOF

    oc create secret generic cluster-fulfillment-ig --from-env-file=fufillment_creds -n fulfillment-aap

#### Template publisher configuration

Create the service account, role, and role bindings required by the template
publisher to authenticate against the
[fulfillment-service](https://github.com/osac-project/fulfillment-service/):

    cat << EOF > template-publisher
    apiVersion: v1
    kind: ServiceAccount
    metadata:
      name: template-publisher
      namespace: fulfillment-aap
    ---
    apiVersion: rbac.authorization.k8s.io/v1
    kind: Role
    metadata:
      name: create-controller-token
      namespace: osac
    rules:
      - apiGroups: [""]
        resources: ["serviceaccounts/token"]

        # here is the name of the service account to authenticate against the
        # fulfillment-service
        resourceNames: ["controller"]
        verbs: ["create"]
    ---
    apiVersion: rbac.authorization.k8s.io/v1
    kind: RoleBinding
    metadata:
      name: aap-fulfillment-template-publisher-binding
      namespace: osac
    roleRef:-
      apiGroup: rbac.authorization.k8s.io
      kind: Role
      name: create-controller-token
    subjects:
      - kind: ServiceAccount
        name: template-publisher
        namespace: fulfillment-aap
    ---
    apiVersion: v1
    kind: ConfigMap
    metadata:
      name: my-prefix-publish-templates-ig
      namespace: fulfillment-aap
    data:
      OSAC_FULFILLMENT_SERVICE_URI: https://fulfillment-service.example.uri
      OSAC_TEMPLATE_COLLECTIONS: osac.templates,example.templates

      # this is the service account used to login against fulfillment service (see
      # RBAC above)
      OSAC_PUBLISH_TEMPLATES_SERVICE_ACCOUNT: controller

      # this is the namespace where the service account used to login against
      # fulfillment service lives ("controller" in this example)
      OSAC_PUBLISH_TEMPLATES_NAMESPACE: osac
    EOF

    oc apply -f template-publisher

#### Bootstrap AAP configuration

Use the execution environment built in the scope of `osac-aap` to run the
playbook that will perform the initial configuration of AAP:

    cat << EOF > job.yml
    apiVersion: batch/v1
    kind: Job
    metadata:
      name: aap-bootstrap
    spec:
      template:
        spec:
          containers:
            - image: ghcr.io/osac/osac-aap:latest
              name: bootstrap
              args:
                - ansible-playbook
                - "osac.config_as_code.subscription"
                - "osac.config_as_code.configure"
              envFrom:
                - secretRef:
                    name: config-as-code-ig
              env:
                - name: AAP_USERNAME
                  value: admin
                - name: AAP_PASSWORD
                  valueFrom:
                    secretKeyRef:
                      name: fulfillment-admin-password
                      key: password
                  - name: LICENSE_MANIFEST_PATH
                    value: /var/secrets/config-as-code-manifest/license.zip
              volumeMounts:
                - name: config-as-code-manifest-volume
                  mountPath: /var/secrets/config-as-code-manifest
                  readOnly: true
            volumes:
              - name: config-as-code-manifest-volume
                secret:
                  secretName: config-as-code-manifest-ig
          restartPolicy: Never
      backoffLimit: 4
    EOF

    oc apply -f job.yml -n fulfillment-aap
