# Secrets Management Configuration

This guide is for cloud provider administrators connecting OSAC to an existing
Vault deployment. **For production, deploy and operate Vault before installing
OSAC.** OSAC does not install, initialize, unseal, back up, or upgrade Vault.
As with an external PostgreSQL database, Vault must be available when OSAC starts.

## Vault options and versions

This guide uses HashiCorp Vault terminology and CLI commands. OpenBao is also
an option because it provides the Vault-compatible API that OSAC needs. OpenBao
users can replace `vault` with `bao` and use `BAO_ADDR` and `BAO_TOKEN` in place
of the Vault environment variables. The OSAC Helm settings are the same.

- **HashiCorp Vault Enterprise 0.11 or later.** Namespaces were introduced in
  [Vault 0.11](https://www.hashicorp.com/blog/vault-0-11-feature-preview-namespaces).
  A self-managed Vault deployment needs an appropriate
  [Enterprise license](https://developer.hashicorp.com/vault/docs/enterprise/namespaces);
  the Community edition does not provide namespaces.
- **OpenBao 2.3.1 or later.** This was the first released version with
  [namespace support](https://openbao.org/docs/2.3.x/release-notes/2-3-0/).

OSAC creates one child namespace per tenant under a parent namespace (normally `osac`).
These are Vault namespaces, not Kubernetes namespaces.

The installer can deploy a single-pod OpenBao for development and CI. It uses
in-memory storage, so its data is lost on restart. Set `bundledVault.enabled`
to `false` in the `osac-infra` values for production. Point the OSAC instance
at your existing Vault. See the [Helm deployment guide](../../../osac-installer/docs/helm-deployment-guide.md)
for the two values files and install order.

## Prerequisites

Check these prerequisites with the administrators of Vault and
[Keycloak](../keycloak-configuration.md):

- Vault is initialized, unsealed, and reachable over HTTPS from the OSAC
  namespace. Provide its CA certificate to OSAC if the cluster does not already trust it.
- Vault can reach Keycloak's OIDC discovery URL and trust its certificate.
  The issuer URL must exactly match the `iss` claim in Keycloak access tokens.
- For production, create a dedicated Keycloak client such as `osac-vault`.
  Enable client authentication and service accounts so it can use the client
  credentials grant. Give its access token an audience mapper for `osac-api`
  (or your chosen `keycloakAudience` value). The token's `azp` claim must be
  `osac-vault`; the Vault role below checks it. This client needs no Keycloak
  realm-management roles.
- In the OSAC release namespace, provide that client's secret in a Kubernetes
  Secret under the key `client-secret`. Set `service.vault.keycloakClientId` to
  the same client ID and reference the Secret in `service.vault.credentials`.
  The installer provides `osac-controller` and its credentials for dev/CI; you
  can keep using them there without creating a dedicated client.

## Prepare Vault

Create the parent namespace, enable JWT authentication there, and grant a
lifecycle role permission to create and configure tenant namespaces. Run the
following commands with an administrator token against your existing Vault.
Replace the issuer URL and client ID to match Keycloak.

```sh
vault namespace create osac
vault auth enable -namespace=osac -path=jwt jwt
vault write -namespace=osac auth/jwt/config \
  oidc_discovery_url=https://keycloak.example.com/realms/osac \
  default_role=lifecycle
```

If Keycloak uses a private CA, add `oidc_discovery_ca_pem=@/path/to/keycloak-ca.pem`
to the `auth/jwt/config` command. This trust setting is on Vault; the OSAC CA
bundle below serves the connection in the other direction.

Create a policy with the permissions OSAC needs for tenant namespace setup and
deletion. Save this as `lifecycle.hcl`:

> **Scope:** The commands below create this policy and its JWT role in the
> Vault `osac` namespace. Vault interprets the policy paths relative to that
> namespace; `+` matches any immediate child namespace. This grants broad
> control over OSAC's child namespaces, including their mounts, auth methods,
> and policies, but does not grant access to Vault's root or sibling namespaces.
> Keep the `osac` parent namespace dedicated to OSAC.

```hcl
path "sys/namespaces/*" {
  capabilities = ["create", "read", "update", "delete", "list", "sudo"]
}
path "sys/namespaces" {
  capabilities = ["list"]
}
path "+/sys/mounts/*" {
  capabilities = ["create", "read", "update", "delete", "sudo"]
}
path "+/sys/mounts" {
  capabilities = ["read", "list"]
}
path "+/sys/policies/*" {
  capabilities = ["create", "read", "update", "delete", "list", "sudo"]
}
path "+/sys/auth/*" {
  capabilities = ["create", "read", "update", "delete", "list", "sudo"]
}
path "+/auth/*" {
  capabilities = ["create", "read", "update", "delete", "list", "sudo"]
}
```

Create the policy and a JWT role limited to your Keycloak client. The `azp`
value below must match its client ID.

```sh
vault policy write -namespace=osac lifecycle lifecycle.hcl
vault write -namespace=osac auth/jwt/role/lifecycle - <<'JSON'
{
  "role_type": "jwt",
  "bound_audiences": ["osac-api"],
  "bound_claims": {"azp": "osac-vault"},
  "user_claim": "sub",
  "token_policies": ["lifecycle"],
  "token_ttl": "1h"
}
JSON
```

OSAC creates each tenant's child namespace, KV v2 mount, JWT method, service
role, and access policy after the tenant is created in OSAC. It also sets up
these resources for its built-in `system` and `shared` tenants after startup.
You do not need to create those tenant resources by hand.

## Configure the OSAC instance

Store the dedicated client's secret in the OSAC release namespace. For
example, save it to a local file and create a Kubernetes Secret:

```sh
oc -n osac create secret generic osac-vault-credentials \
  --from-file=client-secret=/path/to/osac-vault-client-secret
```

Reference that Secret in your instance values file:

```yaml
service:
  vault:
    endpoint: https://vault.example.com:8200
    namespace: osac
    kvMountPath: secret
    lifecycleMountPath: jwt
    lifecycleRole: lifecycle
    keycloakIssuerUrl: https://keycloak.example.com/realms/osac
    keycloakClientId: osac-vault
    keycloakAudience: osac-api
    credentials:
      - secret:
          name: osac-vault-credentials
          items:
            - key: client-secret
              param: client-secret
    caBundle:
      configMap: vault-ca-bundle
```

Create `vault-ca-bundle` in the OSAC release namespace with trusted PEM
certificates under the key `bundle.pem`:

```sh
oc -n osac create configmap vault-ca-bundle \
  --from-file=bundle.pem=/path/to/vault-ca.pem
```

Omit `caBundle.configMap` when the existing shared CA pool already trusts Vault.
Keep credentials out of the values file. Set
`keycloakIssuerUrl` to the same issuer used by `service.auth.issuerUrl`; the
other values must match the parent namespace, JWT role, and Keycloak client
configured above. The fulfillment chart requires Vault settings when the
service is enabled; an empty endpoint is not a supported configuration.

## Verify after deployment

After installing or upgrading OSAC with these settings, expect:

1. The `fulfillment-grpc-server` and `fulfillment-controller` deployments
   become available. The gRPC server logs `Vault health check passed` during
   startup.
2. After OSAC finishes its initial setup, `vault namespace list -namespace=osac`
   shows namespaces for OSAC's built-in `system` and `shared` tenants. These
   provide a check immediately after deployment.

The tenant namespace confirms that OSAC authenticated to Vault and provisioned
resources there. Deployment readiness and the startup health check only show
that the services started and Vault responded.

## Troubleshooting

- No `Vault health check passed` log entry: check Vault's endpoint, DNS, TLS
  trust, and whether Vault is initialized and unsealed. A failed startup health
  check is logged but does not necessarily stop the deployment.
- No child namespace appears for a tenant: check the
  `fulfillment-controller` logs for `Failed to provision vault namespace for tenant`.
  Confirm the parent namespace and lifecycle policy, then compare the Keycloak
  token's `iss`, `aud`, and `azp` claims with the Vault JWT configuration.
