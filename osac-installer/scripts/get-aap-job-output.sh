#!/bin/bash
# Get AAP job output by job ID using oc exec (runs curl inside the cluster)
# Usage: ./get-aap-job-output-alt.sh <job-id>

set -euo pipefail

JOB_ID="${1:-}"
OSAC_NS="${NS:-osac}"
# Bound both connection setup and response transfer. Override these values for
# unusually slow AAP environments without allowing a stalled request to hang
# the diagnostic script indefinitely.
AAP_CURL_CONNECT_TIMEOUT="${AAP_CURL_CONNECT_TIMEOUT:-10}"
AAP_CURL_MAX_TIME="${AAP_CURL_MAX_TIME:-120}"

if [[ -z "$JOB_ID" ]]; then
    echo "Usage: $0 <job-id>"
    echo ""
    echo "Example:"
    echo "  $0 1359"
    exit 1
fi

echo "Fetching AAP job output for job ID: $JOB_ID"
echo ""

# Get AAP password from K8s secret
echo "Retrieving AAP credentials..."
AAP_PASSWORD=$(kubectl get secret osac-aap-controller-admin-password -n "$OSAC_NS" -o jsonpath='{.data.password}' 2>/dev/null | base64 -d)
if [[ -z "$AAP_PASSWORD" ]]; then
    echo "ERROR: Could not retrieve AAP admin password from secret osac-aap-controller-admin-password in namespace $OSAC_NS"
    exit 1
fi

# Get AAP URL from automationcontroller resource
echo "Retrieving AAP URL from automationcontroller..."
AAP_URL=$(kubectl get automationcontroller osac-aap-controller -n "$OSAC_NS" -o jsonpath='{.status.URL}' 2>/dev/null)
if [[ -z "$AAP_URL" ]]; then
    echo "ERROR: Could not retrieve AAP URL from automationcontroller osac-aap-controller in namespace $OSAC_NS"
    exit 1
fi
echo "Using AAP endpoint from the AutomationController status."
echo ""

# Get a running controller pod (not migration/bootstrap)
CONTROLLER_POD=$(kubectl get pods -n "$OSAC_NS" --field-selector=status.phase=Running | grep 'osac-aap-controller-' | grep -v migration | grep -v bootstrap | head -1 | awk '{print $1}')

if [[ -z "$CONTROLLER_POD" ]]; then
    echo "ERROR: Could not find a running AAP controller pod in namespace $OSAC_NS"
    echo "Available pods:"
    kubectl get pods -n "$OSAC_NS" | grep aap
    exit 1
fi

echo "Using pod: $CONTROLLER_POD"
echo ""

# Use internal service URL since we're executing inside the cluster
INTERNAL_AAP_URL="http://osac-aap-controller-service.${OSAC_NS}.svc.cluster.local"
echo "Using the in-cluster AAP service."
echo ""

json_value() {
    local document="$1" key="$2"
    if command -v jq &> /dev/null; then
        printf '%s' "$document" | jq -r --arg key "$key" '.[$key] // ""'
        return
    fi
    if command -v python3 &> /dev/null; then
        printf '%s' "$document" | python3 -c '
import json
import sys

try:
    document = json.load(sys.stdin)
except (json.JSONDecodeError, OSError):
    raise SystemExit(1)

value = document.get(sys.argv[1])
if value is None:
    print("")
elif isinstance(value, (dict, list)):
    print(json.dumps(value))
else:
    print(value)
' "$key"
        return
    fi
    echo "ERROR: jq or python3 is required to parse AAP JSON responses" >&2
    return 1
}

json_content() {
    local document="$1"
    if command -v jq &> /dev/null; then
        printf '%s' "$document" | jq -er 'if (.content? // null) == null then error else .content end'
        return
    fi
    if command -v python3 &> /dev/null; then
        printf '%s' "$document" | python3 -c '
import json
import sys

try:
    document = json.load(sys.stdin)
except (json.JSONDecodeError, OSError):
    raise SystemExit(1)

content = document.get("content")
if content is None:
    raise SystemExit(1)
print(content, end="")
'
        return
    fi
    echo "ERROR: jq or python3 is required to parse AAP JSON responses" >&2
    return 1
}

fetch_aap_json() {
    local url="$1" escaped_password
    escaped_password=${AAP_PASSWORD//\\/\\\\}
    escaped_password=${escaped_password//\"/\\\"}
    # Keep the administrator password in curl's stdin configuration rather
    # than in kubectl exec arguments, which are retained in API audit URIs.
    printf 'user = "admin:%s"\n' "$escaped_password" | \
        kubectl exec -i -n "$OSAC_NS" "$CONTROLLER_POD" -- \
        curl -sS --connect-timeout "$AAP_CURL_CONNECT_TIMEOUT" \
            --max-time "$AAP_CURL_MAX_TIME" --config - "$url" 2>/dev/null
}

# Fetch job details via exec
echo "Fetching job details..."
JOB_RESPONSE=$(fetch_aap_json "$INTERNAL_AAP_URL/api/v2/jobs/$JOB_ID/" ||
    echo '{"error":"exec failed"}')

if ! JOB_OBJECT_ID=$(json_value "$JOB_RESPONSE" id) || [[ -z "$JOB_OBJECT_ID" ]]; then
    echo "ERROR: Job not found or invalid AAP response for job ID: $JOB_ID"
    exit 1
fi
STATUS=$(json_value "$JOB_RESPONSE" status) || {
    echo "ERROR: Invalid AAP job response for job ID: $JOB_ID" >&2
    exit 1
}
NAME=$(json_value "$JOB_RESPONSE" name) || {
    echo "ERROR: Invalid AAP job response for job ID: $JOB_ID" >&2
    exit 1
}
STARTED=$(json_value "$JOB_RESPONSE" started) || {
    echo "ERROR: Invalid AAP job response for job ID: $JOB_ID" >&2
    exit 1
}
FINISHED=$(json_value "$JOB_RESPONSE" finished) || {
    echo "ERROR: Invalid AAP job response for job ID: $JOB_ID" >&2
    exit 1
}

echo "Job ID: $JOB_ID"
echo "Job Name: $NAME"
echo "Job Status: $STATUS"
echo "Started: $STARTED"
echo "Finished: $FINISHED"
echo ""

# Fetch job output/stdout via exec
echo "Fetching job output..."
echo "-------------------------------------------"

# Get raw output
RAW_OUTPUT=$(fetch_aap_json "$INTERNAL_AAP_URL/api/v2/jobs/$JOB_ID/stdout/?format=json")

# Try formatted output with jq
if ! json_content "$RAW_OUTPUT"; then
    echo "ERROR: AAP returned no job stdout for job ID: $JOB_ID" >&2
    exit 1
fi
echo "-------------------------------------------"
