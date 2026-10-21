#!/usr/bin/env bash
set -euo pipefail

# Remove the API token Secret the seed hook wrote into the operator namespace,
# which Helm cannot delete with the release because it lives outside the
# release namespace. Runs as a pre-delete hook, before Helm removes the release
# and the release-managed RBAC this Job relies on.
#
# Required env vars (set by the Job spec):
#   OPERATOR_NS — namespace holding the token Secret
#   SECRET_NAME — name of the token Secret

oc -n "${OPERATOR_NS}" delete secret "${SECRET_NAME}" --ignore-not-found
