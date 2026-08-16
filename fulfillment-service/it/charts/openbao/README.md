# OpenBao integration test chart

Deploys [OpenBao](https://openbao.org) in dev mode as a Vault-compatible secret store for
integration testing. Dev mode runs auto-unsealed with in-memory storage and a configurable root
token.

This chart is for **integration testing only** and must not be used for production installations.

## Configuration

| Value | Description | Default |
|-------|-------------|---------|
| `images.openbao` | Container image | `ghcr.io/openbao/openbao:2.6.1` |
| `dev.rootToken` | Root token for authentication | (must be set) |
| `dev.listenAddress` | API listen address | `0.0.0.0:8200` |
| `caBundle.configMap` | ConfigMap with trusted CA bundle (`bundle.pem` key) | (optional) |
