# Legacy ClusterOrder admission webhook

This Kustomize bundle is for existing non-Helm operator deployments. New
deployments should use the operator Helm chart or the generated
`dist/install.yaml` bundle, which install the webhook and its RBAC together.

The standalone path defaults to the legacy development deployment name
`osac-operator-controller-manager` in namespace `osac-devel`. Update
`kustomization.yaml`, `manager-patch.yaml`, and the Certificate issuer reference
if the deployment uses a different namespace, Deployment name, or cert-manager
issuer. The manager ServiceAccount identity is read from the Downward API.

Install in this order so the `bind` permission is not granted until the
fail-closed webhook can validate requests:

1. Confirm cert-manager and the configured ClusterIssuer are ready.
2. Install the webhook Service, Certificate, and ValidatingWebhookConfiguration:

   ```bash
   oc apply -k osac-operator/config/webhook
   ```

3. Patch the legacy manager Deployment with its webhook port, certificate mount,
   and runtime identity:

   ```bash
   oc wait --for=condition=Ready certificate/osac-operator-clusterorder-rbac \
     -n osac-devel --timeout=5m
   oc patch deployment osac-operator-controller-manager -n osac-devel \
     --type=strategic \
     --patch-file osac-operator/config/webhook/manager-patch.yaml
   oc rollout status deployment/osac-operator-controller-manager -n osac-devel
   ```

4. Wait for cert-manager to inject the CA bundle, then apply the generated
   manager RBAC:

   ```bash
   until [ -n "$(oc get validatingwebhookconfiguration \
     osac-operator-clusterorder-rbac-webhook \
     -o jsonpath='{.webhooks[0].clientConfig.caBundle}')" ]; do
     sleep 2
   done
   oc apply -f osac-operator/config/rbac/role.yaml
   ```

Do not apply `config/rbac/role.yaml` by itself: it grants the manager `bind` on
the built-in `admin` ClusterRole, and that permission must be paired with this
webhook. The webhook fails closed and validates admin bindings only in generated
ClusterOrder namespaces; the handler additionally checks the exact manager
identity, existing ClusterOrder, generated namespace, and fixed `osac` subject.
