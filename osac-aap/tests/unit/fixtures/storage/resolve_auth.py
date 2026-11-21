"""Resolve Kubernetes collection authentication in an isolated interpreter."""

import json
import sys

sys.path[0] = sys.argv[1]
from ansible_collections.kubernetes.core.plugins.module_utils.k8s.client import _create_auth_spec

auth_keys = ("kubeconfig", "context", "host", "validate_certs")
auths = [_create_auth_spec(**{key: call[key] for key in auth_keys if key in call})
         for call in json.loads(sys.argv[2])]
print(json.dumps([{key: auth.get(key) for key in ("kubeconfig", "context", "host", "verify_ssl")} for auth in auths]))
