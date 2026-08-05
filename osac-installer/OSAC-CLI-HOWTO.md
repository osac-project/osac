# OSAC CLI Comprehensive How-To Guide

This document provides a detailed, step-by-step guide for setting up, configuring, and using the OSAC CLI with OpenShift/Kubernetes environments, including real-world examples and troubleshooting scenarios.

## Table of Contents

1. [Overview](#overview)
2. [Prerequisites](#prerequisites)
3. [Setup and Compilation](#setup-and-compilation)
4. [Configuration](#configuration)
5. [OpenShift/Kubernetes Deployment](#openshiftkubernetes-deployment)
6. [gRPC and HTTP/2 Call Flow](#grpc-and-http2-call-flow)
7. [Hub Management](#hub-management)
8. [Cluster Order Management](#cluster-order-management)
9. [Networking and Proxy Setup](#networking-and-proxy-setup)
10. [Real-World Usage Examples](#real-world-usage-examples)
11. [Troubleshooting](#troubleshooting)
12. [API Reference](#api-reference)

## Overview

The OSAC CLI is a sophisticated command-line tool that communicates with the Fulfillment Service to manage cluster lifecycle operations. It uses gRPC over HTTP/2 with JWT-based authentication and supports TLS encryption.

**Architecture Components:**
- **osac**: Go-based client application for cluster management
- **fulfillment-service**: Backend service with gRPC API and database integration
- **envoy**: High-performance proxy for HTTP/gRPC translation and TLS termination
- **OpenShift Router**: External traffic routing with TLS passthrough support
- **cert-manager**: Automated certificate lifecycle management

**Key Features:**
- Cluster creation, management, and deletion
- Template-based cluster provisioning
- JWT-based authentication with automatic token refresh
- TLS encryption with OpenShift compatibility
- Comprehensive logging and debugging capabilities

## Prerequisites

### Required Software
- **Go 1.23.0 or later**: For building the CLI from source
- **OpenShift CLI (oc)**: Version 4.10 or later
- **Git**: For source code management
- **curl**: For API testing and troubleshooting

### Access Requirements
- Access to OpenShift/Kubernetes cluster with admin privileges
- Network connectivity to the cluster's API endpoint
- Appropriate service account permissions for cluster operations

### Environment Setup
```bash
# Verify Go installation
go version
# Expected output: go version go1.23.0 linux/amd64

# Verify OpenShift CLI
oc version
# Expected output: Client Version: 4.15.x, Server Version: 4.15.x

# Verify cluster access
oc cluster-info
# Expected output: Kubernetes control plane is running at https://...
```

## Setup and Compilation

### 1. Clone and Build the CLI

```bash
# Clone the repository
git clone https://github.com/osac-project/fulfillment-service.git
cd fulfillment-service

# Check the Go module configuration
cat go.mod
# Expected to see: module github.com/osac-project/fulfillment-service

# Download dependencies
go mod download

# Build the CLI with optimizations
go build -ldflags "-X github.com/osac-project/fulfillment-service/internal/version.id=$(git describe --tags --always)" -o osac ./cmd/osac

# Verify the build
./osac --help
```

**Expected Output:**
```
Command line interface for the fulfillment API

Usage:
  osac [command]

Available Commands:
  completion  Generate the autocompletion script for the specified shell
  create      Create objects
  delete      Delete objects
  describe    Describe a resource
  edit        Edit objects
  get         Get objects
  help        Help about any command
  login       Save connection and authentication details
  logout      Discard connection and authentication details
```

### 2. Install System-Wide (Optional)

```bash
# Install to system path
sudo cp osac /usr/local/bin/
sudo chmod +x /usr/local/bin/osac

# Verify system installation
which osac
# Expected output: /usr/local/bin/osac

# Test system installation
osac --version
```

### 3. Development Environment Setup

For development work, set up the environment:

```bash
# Create development workspace
mkdir -p ~/workspace/fulfillment
cd ~/workspace/fulfillment

# Clone related repositories
git clone https://github.com/osac-project/fulfillment-service.git
git clone https://github.com/osac-project/osac-installer.git

# Set up development environment variables
export OSAC_CLI_ROOT=~/workspace/fulfillment/fulfillment-service
export OSAC_INSTALLER_ROOT=~/workspace/fulfillment/osac-installer
export KUBECONFIG=~/workspace/fulfillment/kubeconfig

# Add to ~/.bashrc for persistence
echo 'export OSAC_CLI_ROOT=~/workspace/fulfillment/fulfillment-service' >> ~/.bashrc
echo 'export OSAC_INSTALLER_ROOT=~/workspace/fulfillment/osac-installer' >> ~/.bashrc
```

## Configuration

### 1. Configuration File Structure

The CLI stores configuration in `~/.config/osac/config.json`:

```bash
# Create configuration directory
mkdir -p ~/.config/osac

# View current configuration location
osac --help | grep -A5 -B5 config
```

### 2. Basic Configuration Examples

**Development Environment Configuration:**
```json
{
  "token_script": "KUBECONFIG=/root/labs/acm/deploy/auth/kubeconfig oc create token dev-admin -n foobar --duration 1h",
  "insecure": true,
  "address": "dev-fulfillment-api-foobar.apps.acm.local.lab:443"
}
```

**Production Environment Configuration:**
```json
{
  "token_script": "oc create token fulfillment-admin -n fulfillment-system --duration 8h",
  "insecure": false,
  "address": "fulfillment-api.apps.production.example.com:443"
}
```

**Local Development with Port Forwarding:**
```json
{
  "token_script": "oc create token dev-admin -n development --duration 1h",
  "plaintext": true,
  "address": "localhost:8443"
}
```

### 3. Configuration Parameters Explained

| Parameter | Type | Description | Example |
|-----------|------|-------------|---------|
| `token_script` | string | Command to generate JWT tokens | `"oc create token admin -n system"` |
| `insecure` | boolean | Skip TLS certificate verification | `true` for self-signed certs |
| `address` | string | Fulfillment service endpoint | `"api.example.com:443"` |
| `token` | string | Cached JWT token (auto-managed) | `"eyJhbGciOiJSUzI1NiIs..."` |
| `plaintext` | boolean | Use unencrypted connection | `false` (not recommended) |

### 4. Authentication Setup with Service Accounts

Create appropriate service accounts with required permissions:

```bash
# Create service account
oc create serviceaccount fulfillment-admin -n your-namespace

# Create ClusterRole with required permissions
cat << 'EOF' | oc apply -f -
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: fulfillment-admin
rules:
- apiGroups: [""]
  resources: ["*"]
  verbs: ["*"]
- apiGroups: ["apps"]
  resources: ["*"]
  verbs: ["*"]
- apiGroups: ["rbac.authorization.k8s.io"]
  resources: ["*"]
  verbs: ["*"]
EOF

# Create ClusterRoleBinding
oc create clusterrolebinding fulfillment-admin \
  --clusterrole=fulfillment-admin \
  --serviceaccount=your-namespace:fulfillment-admin

# Test token generation
oc create token fulfillment-admin -n your-namespace --duration 1h
```

### 5. Environment-Specific Configuration

**Multi-Environment Setup:**
```bash
# Development environment
export OSAC_CONFIG_DEV=~/.config/osac/config-dev.json
cat > $OSAC_CONFIG_DEV << 'EOF'
{
  "token_script": "KUBECONFIG=/path/to/dev/kubeconfig oc create token dev-admin -n dev --duration 1h",
  "insecure": true,
  "address": "dev-fulfillment-api.apps.dev.example.com:443"
}
EOF

# Production environment
export OSAC_CONFIG_PROD=~/.config/osac/config-prod.json
cat > $OSAC_CONFIG_PROD << 'EOF'
{
  "token_script": "oc create token fulfillment-admin -n prod --duration 8h",
  "insecure": false,
  "address": "fulfillment-api.apps.prod.example.com:443"
}
EOF

# Use different configurations
cp $OSAC_CONFIG_DEV ~/.config/osac/config.json  # For dev
cp $OSAC_CONFIG_PROD ~/.config/osac/config.json  # For prod
```

## OpenShift/Kubernetes Deployment

### 1. Complete Deployment Process

**Step 1: Prepare the Environment**
```bash
# Create namespace
oc new-project fulfillment-system

# Verify namespace
oc get projects | grep fulfillment-system

# Set context
oc project fulfillment-system
```

**Step 2: Deploy Prerequisites**
```bash
# Deploy cert-manager (if not already installed)
oc apply -f https://github.com/cert-manager/cert-manager/releases/download/v1.13.0/cert-manager.yaml

# Wait for cert-manager to be ready
oc wait --for=condition=Available deployment/cert-manager -n cert-manager --timeout=300s
oc wait --for=condition=Available deployment/cert-manager-cainjector -n cert-manager --timeout=300s
oc wait --for=condition=Available deployment/cert-manager-webhook -n cert-manager --timeout=300s

# Verify cert-manager installation
oc get pods -n cert-manager
```

**Step 3: Deploy Fulfillment Service**
```bash
# Navigate to installer directory
cd osac-installer

# Deploy via Helm
helm upgrade --install osac charts/osac/ --namespace osac --values values/development/values.yaml --timeout 40m --wait

# Apply the complete development overlay

# Monitor deployment progress
oc get pods -n fulfillment-system -w
```

### 2. Deployment Verification

**Check All Resources:**
```bash
# Verify all pods are running
oc get pods -n fulfillment-system
# Expected output: All pods in Running state

# Check services
oc get services -n fulfillment-system
# Expected output: Services with appropriate ClusterIP assignments

# Check routes
oc get routes -n fulfillment-system
# Expected output: Routes with valid hostnames

# Check certificates
oc get certificates -n fulfillment-system
# Expected output: Certificates in Ready state

# Check certificate details
oc describe certificate fulfillment-api -n fulfillment-system
```

**Detailed Resource Inspection:**
```bash
# Check deployment status
oc rollout status deployment/fulfillment-service -n fulfillment-system

# View deployment configuration
oc get deployment fulfillment-service -n fulfillment-system -o yaml

# Check pod logs
oc logs deployment/fulfillment-service -n fulfillment-system -c server
oc logs deployment/fulfillment-service -n fulfillment-system -c gateway
oc logs deployment/fulfillment-service -n fulfillment-system -c envoy

# Check service endpoints
oc get endpoints -n fulfillment-system
```

### 3. RBAC Configuration Deep Dive

**Understanding Authorization Rules:**
```bash
# View current authorization ConfigMap
oc get configmap fulfillment-service-config-xxx -n fulfillment-system -o yaml

# Check the rules content
oc get configmap fulfillment-service-config-xxx -n fulfillment-system -o jsonpath='{.data.rules\.yaml}'
```

**Example Authorization Rules:**
```yaml
# Allow reflection and health checks to everyone
- name: Allow reflection and health to everyone
  action: allow
  condition: method.startsWith('/grpc.reflection.') || method.startsWith('/grpc.health.')

# Allow specific methods to client service account
- name: Allow specific methods to client
  action: allow
  condition: |
    subject.name == 'system:serviceaccount:fulfillment-system:client' &&
    method in [
      '/fulfillment.v1.ClusterTemplates/Get',
      '/fulfillment.v1.ClusterTemplates/List',
      '/fulfillment.v1.Clusters/Create',
      '/fulfillment.v1.Clusters/Delete',
      '/fulfillment.v1.Clusters/Get',
      '/fulfillment.v1.Clusters/GetKubeconfig',
      '/fulfillment.v1.Clusters/List',
      '/fulfillment.v1.Clusters/Update',
      '/fulfillment.v1.HostTypes/Get',
      '/fulfillment.v1.HostTypes/List'
    ]

# Allow everything to admin service accounts
- name: Allow everything to admin and controller
  action: allow
  condition: |
    subject.name in [
      'system:serviceaccount:fulfillment-system:admin',
      'system:serviceaccount:fulfillment-system:fulfillment-admin',
      'system:serviceaccount:fulfillment-system:controller'
    ]
```

## gRPC and HTTP/2 Call Flow

### 1. Detailed Architecture Flow

```mermaid
graph TD
    A[osac] -->|HTTP/2 + gRPC + TLS| B[OpenShift Router]
    B -->|TLS Passthrough| C[Service/Load Balancer]
    C -->|HTTP/2| D[Envoy Proxy]
    D -->|gRPC over Unix Socket| E[fulfillment-service]

    F[JWT Token] -->|Authentication| A
    G[cert-manager] -->|TLS Certificates| B
    H[ConfigMap] -->|Authorization Rules| D
    I[PostgreSQL] -->|Database| E
```

### 2. Protocol Stack Analysis

**Layer 1: CLI to Router**
- **Protocol**: HTTP/2 with gRPC framing
- **Transport**: TLS 1.2/1.3 with ALPN disabled
- **Authentication**: JWT Bearer tokens in Authorization header
- **Compression**: gzip compression for request/response bodies

```go
// Example from fulfillment-service source code
transportCreds = experimentalcredentials.NewTLSWithALPNDisabled(tlsConfig)
```

**Layer 2: Router to Service**
- **Protocol**: TLS passthrough (no termination at router)
- **Headers**: All gRPC headers preserved
- **Load Balancing**: Round-robin to backend pods

**Layer 3: Service to Envoy**
- **Protocol**: HTTP/2 with gRPC
- **Security**: TLS with service mesh certificates
- **Features**: Connection pooling, circuit breaking

**Layer 4: Envoy to Backend**
- **Protocol**: gRPC over Unix domain socket
- **Security**: Process isolation, no network exposure
- **Performance**: Zero-copy operations, minimal latency

### 3. OpenShift-Specific Considerations

**ALPN (Application-Layer Protocol Negotiation) Issues:**
OpenShift routers may not support ALPN, which is required by newer gRPC versions. The CLI handles this with:

```go
// Special handling for OpenShift ALPN limitations
if !c.Plaintext {
    tlsConfig := &tls.Config{
        InsecureSkipVerify: c.Insecure,
    }
    // Use experimental credentials to disable ALPN
    transportCreds = experimentalcredentials.NewTLSWithALPNDisabled(tlsConfig)
}
```

**Route Configuration Requirements:**
```yaml
apiVersion: route.openshift.io/v1
kind: Route
metadata:
  name: fulfillment-api
spec:
  host: fulfillment-api.apps.example.com
  tls:
    termination: passthrough  # Critical for gRPC
    insecureEdgeTerminationPolicy: Redirect
  to:
    kind: Service
    name: fulfillment-api
    weight: 100
  port:
    targetPort: api
```

### 4. Request/Response Flow Example

**Creating a Cluster:**
```bash
# CLI command
./osac create cluster --template simple

# Generated gRPC request
POST /fulfillment.v1.Clusters/Create HTTP/2
Host: fulfillment-api.apps.example.com
Authorization: Bearer eyJhbGciOiJSUzI1NiIs...
Content-Type: application/grpc+proto
User-Agent: grpc-go/1.70.0

# Request body (protobuf)
{
  "template": "simple",
  "name": "cluster-001",
  "parameters": {}
}

# Response
HTTP/2 200 OK
Content-Type: application/grpc+proto
grpc-status: 0

# Response body (protobuf)
{
  "id": "672c0827-ef03-48d0-b825-689f83ff296b",
  "template": "simple",
  "state": "PROVISIONING"
}
```

## Hub Management

### 1. Understanding Hubs in OSAC Architecture

**What are Hubs?**
Hubs are OpenShift/Kubernetes clusters that have been registered with the fulfillment service to act as target environments for cluster provisioning. When you create a cluster through the `osac` CLI, the system creates a ClusterOrder Custom Resource Definition (CRD) in one of the registered hubs, where the osac-operator processes the request.

**Hub Architecture Flow:**
```mermaid
graph TD
    A[osac] -->|Create cluster request| B[fulfillment-service]
    B -->|Store cluster request| C[PostgreSQL Database]
    B -->|Notify| D[fulfillment-controller]
    D -->|Create ClusterOrder CRD| E[Hub Cluster]
    E -->|Process ClusterOrder| F[osac-operator]
    F -->|Provision cluster| G[New Cluster]
```

### 2. Hub Registration Prerequisites

Before registering a hub, ensure the following requirements are met:

**Hub Cluster Requirements:**
- OpenShift/Kubernetes cluster with admin access
- osac-operator deployed and running
- ClusterOrder CRDs installed
- Network connectivity to fulfillment-service

**Service Account Setup:**
Create a service account with cluster-admin privileges for hub operations:

```bash
# Set your hub cluster context
export KUBECONFIG=/path/to/hub/kubeconfig

# Create namespace for osac-operator if it doesn't exist
oc create namespace osac-operator-system --dry-run=client -o yaml | oc apply -f -

# Create service account for hub access
oc create serviceaccount hub-access -n osac-operator-system

# Grant cluster-admin permissions
oc create clusterrolebinding hub-access-admin \
  --clusterrole=cluster-admin \
  --serviceaccount=osac-operator-system:hub-access

# Generate a long-lived token (24 hours recommended)
HUB_TOKEN=$(oc create token hub-access -n osac-operator-system --duration=24h)
echo "Hub token: $HUB_TOKEN"
```

**Minimal Kubeconfig Creation:**
Create a minimal kubeconfig file to avoid database payload size limits:

```bash
# Get the cluster API server URL
CLUSTER_API=$(oc whoami --show-server)

# Create minimal kubeconfig
cat > /tmp/hub-kubeconfig.yaml << EOF
apiVersion: v1
clusters:
- cluster:
    server: $CLUSTER_API
    insecure-skip-tls-verify: true
  name: hub-cluster
contexts:
- context:
    cluster: hub-cluster
    user: hub-access
  name: default
current-context: default
kind: Config
preferences: {}
users:
- name: hub-access
  user:
    token: $HUB_TOKEN
EOF

echo "Minimal kubeconfig created at /tmp/hub-kubeconfig.yaml"
```

### 3. Hub Registration Process

**Step 1: Login to Fulfillment Service**
```bash
# Configure osac for your environment
cat > ~/.config/osac/config.json << 'EOF'
{
  "token_script": "KUBECONFIG=/root/labs/acm/deploy/auth/kubeconfig oc create token dev-admin -n foobar --duration 1h",
  "insecure": true,
  "address": "dev-fulfillment-api-foobar.apps.acm.local.lab:443"
}
EOF

# Login to the fulfillment service
./osac login
```

**Step 2: Register the Hub**
```bash
# Register hub with fulfillment service
./osac create hub \
  --id production-hub-01 \
  --kubeconfig /tmp/hub-kubeconfig.yaml \
  --namespace osac-operator-system

# Verify hub registration
./osac get hubs
```

**Step 3: Verify Hub Connectivity**
```bash
# Check that the hub is accessible
./osac describe hub production-hub-01

# Test cluster creation to verify end-to-end functionality
./osac create cluster --template example
```

### 4. Hub Management Commands

**List All Hubs:**
```bash
# Basic listing
./osac get hubs

# Detailed output with JSON format
./osac get hubs --output json

# Filter hubs by specific criteria
./osac get hubs --filter "status=active"
```

**Hub Details:**
```bash
# Get detailed information about a specific hub
./osac describe hub production-hub-01

# Check hub connectivity status
./osac get hub production-hub-01 --output yaml
```

**Update Hub Configuration:**
```bash
# Update hub kubeconfig (if token expires)
./osac edit hub production-hub-01 --kubeconfig /tmp/new-hub-kubeconfig.yaml

# Update hub namespace
./osac edit hub production-hub-01 --namespace new-namespace
```

**Remove Hub:**
```bash
# Delete hub registration
./osac delete hub production-hub-01

# Verify deletion
./osac get hubs
```

### 5. Multi-Hub Environment Setup

**Scenario: Multiple Environment Hubs**
```bash
# Development hub
./osac create hub \
  --id dev-hub \
  --kubeconfig /tmp/dev-hub-kubeconfig.yaml \
  --namespace osac-operator-system

# Staging hub
./osac create hub \
  --id staging-hub \
  --kubeconfig /tmp/staging-hub-kubeconfig.yaml \
  --namespace osac-operator-system

# Production hub
./osac create hub \
  --id prod-hub \
  --kubeconfig /tmp/prod-hub-kubeconfig.yaml \
  --namespace osac-operator-system

# List all registered hubs
./osac get hubs --output table
```

**Hub Selection Strategy:**
The fulfillment-service automatically selects an appropriate hub based on:
- Hub availability and health status
- Resource capacity and constraints
- Load balancing policies
- Template requirements and hub capabilities

### 6. Hub Troubleshooting

**Common Hub Issues:**

**Issue: Hub Registration Fails with "payload string too long"**
```bash
# Problem: Kubeconfig contains large certificate data
# Solution: Create minimal kubeconfig with token authentication

# Check current kubeconfig size
wc -c /path/to/kubeconfig
# If > 8KB, create minimal version

# Create service account token
TOKEN=$(oc create token hub-access -n osac-operator-system --duration=24h)

# Create minimal kubeconfig
cat > /tmp/minimal-kubeconfig.yaml << EOF
apiVersion: v1
clusters:
- cluster:
    server: $(oc whoami --show-server)
    insecure-skip-tls-verify: true
  name: hub
contexts:
- context:
    cluster: hub
    user: hub-access
  name: default
current-context: default
kind: Config
users:
- name: hub-access
  user:
    token: $TOKEN
EOF
```

**Issue: Hub Connectivity Problems**
```bash
# Check hub cluster accessibility
KUBECONFIG=/tmp/hub-kubeconfig.yaml oc get nodes

# Verify osac-operator is running
KUBECONFIG=/tmp/hub-kubeconfig.yaml oc get pods -n osac-operator-system

# Check ClusterOrder CRDs
KUBECONFIG=/tmp/hub-kubeconfig.yaml oc get crd | grep clusterorder

# Test hub from fulfillment-service perspective
./osac describe hub hub-id
```

**Issue: Token Expiration**
```bash
# Monitor token expiration
TOKEN_EXPIRY=$(echo $TOKEN | cut -d. -f2 | base64 -d 2>/dev/null | jq -r .exp)
echo "Token expires: $(date -d @$TOKEN_EXPIRY)"

# Refresh token and update hub
NEW_TOKEN=$(oc create token hub-access -n osac-operator-system --duration=24h)
# Update kubeconfig with new token and re-register hub
```

## Cluster Order Management

### 1. Understanding Cluster Orders

**What are Cluster Orders?**
Cluster Orders are Kubernetes Custom Resource Definitions (CRDs) that represent cluster provisioning requests within hub clusters. When you create a cluster via osac, the system:

1. Stores the request in the fulfillment-service database
2. fulfillment-controller creates a ClusterOrder CRD in a selected hub
3. osac-operator in the hub processes the ClusterOrder
4. The actual cluster provisioning begins

**ClusterOrder Lifecycle:**
```
PENDING → ACCEPTED → PROGRESSING → READY
                                 ↓
                              FAILED
```

### 2. Creating Cluster Orders

**Basic Cluster Creation:**
```bash
# Create cluster with default template
./osac create cluster --template example

# Create cluster with specific template
./osac create cluster --template ocp-4-17-small

# Create cluster with custom parameters
./osac create cluster \
  --template ocp-4-17-medium \
  --parameters "region=us-east-1,instance_type=m5.xlarge"
```

**Advanced Cluster Creation:**
```bash
# Create cluster with specific node requirements
./osac create cluster \
  --template ocp-4-17-large \
  --parameters "control_plane_nodes=3,worker_nodes=5,storage_class=gp3"

# Create cluster with custom networking
./osac create cluster \
  --template ocp-4-17-custom \
  --parameters "vpc_cidr=10.1.0.0/16,service_cidr=172.30.0.0/16"
```

### 3. Monitoring Cluster Orders

**Check Cluster Status:**
```bash
# List all clusters
./osac get clusters

# Get specific cluster details
CLUSTER_ID="580594a8-3532-4809-b8ff-dd43d037ec45"
./osac get cluster $CLUSTER_ID

# Monitor cluster creation progress
watch -n 30 "./osac get cluster $CLUSTER_ID"
```

**Detailed Cluster Information:**
```bash
# Get comprehensive cluster details
./osac describe cluster $CLUSTER_ID

# Get cluster status in JSON format
./osac get cluster $CLUSTER_ID --output json | jq '.status'

# Check cluster events and logs
./osac get cluster $CLUSTER_ID --show-events
```

### 4. Direct ClusterOrder Inspection

**View ClusterOrder CRDs in Hub:**
```bash
# Set context to hub cluster
export KUBECONFIG=/path/to/hub/kubeconfig

# List all ClusterOrders
oc get clusterorders -n osac-operator-system

# Get specific ClusterOrder details
oc describe clusterorder order-h9ppt -n osac-operator-system

# View ClusterOrder YAML
oc get clusterorder order-h9ppt -n osac-operator-system -o yaml
```

**Example ClusterOrder Structure:**
```yaml
apiVersion: osac.openshift.io/v1alpha1
kind: ClusterOrder
metadata:
  name: order-h9ppt
  namespace: osac-operator-system
  labels:
    osac.openshift.io/clusterorder-uuid: 0063916a-f82e-4eaa-a5de-f783d05294d4
spec:
  templateID: example
  templateParameters: "{}"
  nodeRequests: []
status:
  phase: ""
  conditions: []
  clusterReference:
    namespace: ""
    hostedClusterName: ""
    serviceAccountName: ""
    roleBindingName: ""
```

### 5. Cluster Order Troubleshooting

**Check OSAC Operator Logs:**
```bash
# View osac-operator logs
oc logs -n foobar deployment/dev-controller-manager --tail=50 -f

# Filter for specific ClusterOrder
oc logs -n foobar deployment/dev-controller-manager | grep order-h9ppt

# Check for reconciliation errors
oc logs -n foobar deployment/dev-controller-manager | grep "Reconciler error"
```

**Check Fulfillment Controller Logs:**
```bash
# View fulfillment-controller logs
oc logs -n foobar deployment/dev-fulfillment-controller --tail=50 -f

# Check for hub connection issues
oc logs -n foobar deployment/dev-fulfillment-controller | grep -i "hub\|connection\|error"
```

**Common ClusterOrder Issues:**

**Issue: ClusterOrder Stuck in Pending State**
```bash
# Check if osac-operator is running
oc get pods -n foobar -l app.kubernetes.io/name=osac-operator

# Verify ClusterOrder CRDs are installed
oc get crd | grep clusterorder

# Check operator permissions
oc describe clusterrole dev-manager-role | grep -A 10 clusterorders

# Check for reconciliation errors
oc logs -n foobar deployment/dev-controller-manager | grep -A 5 -B 5 "failed to reconcile"
```

**Issue: Communication Errors Between Services**
```bash
# Check fulfillment-service connectivity
curl -k -H "Authorization: Bearer $(oc create token dev-admin -n foobar)" \
  https://dev-fulfillment-api-foobar.apps.acm.local.lab:443/

# Verify gRPC communication
oc logs -n foobar deployment/dev-controller-manager | grep -i "grpc\|connection"

# Check TLS issues
oc logs -n foobar deployment/dev-controller-manager | grep -i "tls\|certificate\|handshake"
```

### 6. Cluster Order Automation

**Automated Cluster Provisioning Script:**
```bash
#!/usr/bin/env bash
# cluster-provision-with-monitoring.sh

set -euo pipefail

TEMPLATE=${1:-example}
HUB_KUBECONFIG=${2:-/tmp/hub-kubeconfig.yaml}

echo "Creating cluster with template: $TEMPLATE"

# Create cluster and capture ID
CLUSTER_ID=$(./osac create cluster --template "$TEMPLATE" | grep "ID:" | cut -d' ' -f2)
echo "Cluster ID: $CLUSTER_ID"

# Monitor both osac and ClusterOrder
echo "Monitoring cluster creation..."
while true; do
    # Check cluster status via CLI
    CLI_STATUS=$(./osac get cluster "$CLUSTER_ID" --output json | jq -r '.state // "UNKNOWN"')

    # Check ClusterOrder status in hub
    export KUBECONFIG="$HUB_KUBECONFIG"
    CO_STATUS=$(oc get clusterorders -n osac-operator-system \
        -l "osac.openshift.io/clusterorder-uuid=$CLUSTER_ID" \
        -o jsonpath='{.items[0].status.phase}' 2>/dev/null || echo "NOT_FOUND")

    echo "$(date): CLI Status: $CLI_STATUS, ClusterOrder Status: $CO_STATUS"

    # Check for completion or failure
    if [[ "$CLI_STATUS" == "READY" ]]; then
        echo "Cluster successfully created!"
        ./osac describe cluster "$CLUSTER_ID"
        break
    elif [[ "$CLI_STATUS" == "FAILED" || "$CO_STATUS" == "Failed" ]]; then
        echo "Cluster creation failed"

        # Show error details
        echo "=== Cluster Details ==="
        ./osac describe cluster "$CLUSTER_ID"

        echo "=== ClusterOrder Details ==="
        oc describe clusterorder -n osac-operator-system \
            -l "osac.openshift.io/clusterorder-uuid=$CLUSTER_ID"

        exit 1
    fi

    sleep 30
done
```

**Bulk Cluster Management:**
```bash
#!/usr/bin/env bash
# bulk-cluster-operations.sh

OPERATION=${1:-list}
TEMPLATE=${2:-example}
COUNT=${3:-3}

case $OPERATION in
    create)
        echo "Creating $COUNT clusters with template $TEMPLATE"
        for i in $(seq 1 $COUNT); do
            echo "Creating cluster $i/$COUNT"
            ./osac create cluster --template "$TEMPLATE"
        done
        ;;

    list)
        echo "Listing all clusters:"
        ./osac get clusters --output table
        ;;

    cleanup)
        echo "Cleaning up failed clusters:"
        ./osac get clusters --output json | \
            jq -r '.[] | select(.state == "FAILED") | .id' | \
            while read -r cluster_id; do
                echo "Deleting failed cluster: $cluster_id"
                ./osac delete cluster "$cluster_id"
            done
        ;;

    monitor)
        echo "Monitoring all active clusters:"
        while true; do
            clear
            ./osac get clusters --output table
            echo "Last updated: $(date)"
            sleep 30
        done
        ;;
esac
```

## Networking and Proxy Setup

### 1. HAProxy Configuration for Development

**Installation and Setup:**
```bash
# Install HAProxy
sudo dnf install haproxy -y  # RHEL/CentOS
sudo apt-get install haproxy -y  # Ubuntu/Debian

# Backup original configuration
sudo cp /etc/haproxy/haproxy.cfg /etc/haproxy/haproxy.cfg.backup

# Create new configuration
sudo tee /etc/haproxy/haproxy.cfg << 'EOF'
global
    daemon
    user haproxy
    group haproxy
    log stdout local0
    chroot /var/lib/haproxy
    stats socket /run/haproxy/admin.sock mode 660 level admin
    stats timeout 30s

defaults
    mode http
    timeout connect 5000ms
    timeout client 50000ms
    timeout server 50000ms
    timeout tunnel 1h
    log global
    option tcplog

# Stats interface
frontend stats
    bind *:8404
    stats enable
    stats uri /stats
    stats refresh 30s
    stats admin if TRUE

# Frontend for fulfillment API
frontend fulfillment_frontend
    bind *:8443
    mode tcp
    default_backend fulfillment_backend

# Backend for fulfillment service
backend fulfillment_backend
    mode tcp
    balance roundrobin
    option tcp-check
    server fulfillment1 dev-fulfillment-api-foobar.apps.acm.local.lab:443 check
    server fulfillment2 dev-fulfillment-api-foobar.apps.acm.local.lab:443 check backup
EOF

# Validate configuration
sudo haproxy -c -f /etc/haproxy/haproxy.cfg

# Start HAProxy
sudo systemctl enable haproxy
sudo systemctl start haproxy
sudo systemctl status haproxy

# Check logs
sudo journalctl -u haproxy -f
```

**Advanced HAProxy Configuration with SSL:**
```bash
# Create SSL certificate directory
sudo mkdir -p /etc/haproxy/certs

# Generate self-signed certificate for testing
sudo openssl req -x509 -newkey rsa:4096 -keyout /etc/haproxy/certs/haproxy.key -out /etc/haproxy/certs/haproxy.crt -days 365 -nodes -subj "/CN=localhost"

# Combine certificate and key
sudo cat /etc/haproxy/certs/haproxy.crt /etc/haproxy/certs/haproxy.key > /etc/haproxy/certs/haproxy.pem

# Advanced configuration with SSL termination
sudo tee /etc/haproxy/haproxy.cfg << 'EOF'
global
    daemon
    tune.ssl.default-dh-param 2048
    ssl-default-bind-options no-sslv3 no-tlsv10 no-tlsv11
    ssl-default-bind-ciphers ECDHE-ECDSA-AES128-GCM-SHA256:ECDHE-RSA-AES128-GCM-SHA256

defaults
    mode http
    timeout connect 5000ms
    timeout client 50000ms
    timeout server 50000ms
    timeout tunnel 1h

frontend fulfillment_ssl
    bind *:8443 ssl crt /etc/haproxy/certs/haproxy.pem
    mode tcp
    default_backend fulfillment_backend

backend fulfillment_backend
    mode tcp
    balance roundrobin
    server fulfillment1 dev-fulfillment-api-foobar.apps.acm.local.lab:443 check
EOF

# Restart HAProxy
sudo systemctl restart haproxy
```

### 2. Port Forwarding Strategies

**Simple Port Forwarding:**
```bash
# Forward to service
oc port-forward service/fulfillment-api 8443:8000 -n fulfillment-system

# Forward to specific pod
oc port-forward pod/fulfillment-service-xxx 8443:8000 -n fulfillment-system

# Forward with specific bind address
oc port-forward --address 0.0.0.0 service/fulfillment-api 8443:8000 -n fulfillment-system
```

**Advanced Port Forwarding with SSH Tunneling:**
```bash
# SSH tunnel through bastion host
ssh -L 8443:fulfillment-api.apps.cluster.example.com:443 user@bastion.example.com

# SSH tunnel with compression
ssh -C -L 8443:fulfillment-api.apps.cluster.example.com:443 user@bastion.example.com

# Background SSH tunnel
ssh -f -N -L 8443:fulfillment-api.apps.cluster.example.com:443 user@bastion.example.com
```

### 3. Network Diagnostics and Testing

**Basic Connectivity Tests:**
```bash
# Test DNS resolution
nslookup fulfillment-api.apps.cluster.example.com

# Test TCP connectivity
telnet fulfillment-api.apps.cluster.example.com 443

# Test HTTP/2 support
curl -I --http2 https://fulfillment-api.apps.cluster.example.com/

# Test TLS certificate
openssl s_client -connect fulfillment-api.apps.cluster.example.com:443 -servername fulfillment-api.apps.cluster.example.com
```

**gRPC-Specific Testing:**
```bash
# Install grpcurl for testing
go install github.com/fullstorydev/grpcurl/cmd/grpcurl@latest

# Test gRPC server reflection
grpcurl -insecure fulfillment-api.apps.cluster.example.com:443 list

# Test specific service
grpcurl -insecure fulfillment-api.apps.cluster.example.com:443 list fulfillment.v1.Clusters

# Test with authentication
grpcurl -insecure -H "Authorization: Bearer $(oc create token admin -n fulfillment-system)" \
  fulfillment-api.apps.cluster.example.com:443 \
  fulfillment.v1.ClusterTemplates/List
```

## Real-World Usage Examples

### 1. Complete Cluster Lifecycle Management

**Scenario: Creating a Development Cluster**
```bash
# Step 1: Login and configure CLI
./osac login
# Follow prompts to configure connection

# Step 2: List available templates
./osac get clustertemplates
# Output:
# ID              TITLE                 DESCRIPTION
# ocp_4_17_small  OpenShift 4.17 small  OpenShift 4.17 with small instances

# Step 3: Create cluster
./osac create cluster --template ocp_4_17_small
# Output:
# ID: 672c0827-ef03-48d0-b825-689f83ff296b

# Step 4: Monitor cluster creation
watch -n 30 './osac get cluster 672c0827-ef03-48d0-b825-689f83ff296b'
# Watch until STATE changes to READY

# Step 5: Get cluster details
./osac describe cluster 672c0827-ef03-48d0-b825-689f83ff296b
# Output includes API URL, console URL, and other details

# Step 6: Get kubeconfig
./osac get cluster 672c0827-ef03-48d0-b825-689f83ff296b --kubeconfig > cluster-kubeconfig.yaml

# Step 7: Test cluster access
KUBECONFIG=cluster-kubeconfig.yaml oc get nodes
```

**Scenario: Managing Multiple Clusters**
```bash
# Create multiple clusters
for template in ocp_4_17_small ocp_4_17_medium; do
  echo "Creating cluster with template: $template"
  ./osac create cluster --template $template
done

# List all clusters
./osac get clusters --output table
# Output:
# ID                                    TEMPLATE        STATE    API URL                          CONSOLE URL
# 672c0827-ef03-48d0-b825-689f83ff296b  ocp_4_17_small  READY    https://api.cluster1.example.com https://console.cluster1.example.com
# 773d1938-fe04-59e1-c936-790g94gg407c  ocp_4_17_medium PENDING  -                                -

# Filter clusters by state
./osac get clusters --filter "state=READY"

# Delete specific cluster
./osac delete cluster 672c0827-ef03-48d0-b825-689f83ff296b
```

### 2. Automation Scripts

**Cluster Provisioning Script:**
```bash
#!/usr/bin/env bash
# cluster-provision.sh

set -euo pipefail

TEMPLATE=${1:-ocp_4_17_small}
CLUSTER_NAME=${2:-dev-cluster-$(date +%Y%m%d-%H%M%S)}

echo "Creating cluster: $CLUSTER_NAME with template: $TEMPLATE"

# Create cluster
CLUSTER_ID=$(./osac create cluster --template "$TEMPLATE" | grep "ID:" | cut -d' ' -f2)
echo "Cluster ID: $CLUSTER_ID"

# Wait for cluster to be ready
echo "Waiting for cluster to be ready..."
while true; do
  STATE=$(./osac get cluster "$CLUSTER_ID" --output json | jq -r '.state')
  echo "Current state: $STATE"

  if [[ "$STATE" == "READY" ]]; then
    break
  elif [[ "$STATE" == "FAILED" ]]; then
    echo "Cluster creation failed"
    exit 1
  fi

  sleep 60
done

# Get cluster details
./osac describe cluster "$CLUSTER_ID"

# Save kubeconfig
./osac get cluster "$CLUSTER_ID" --kubeconfig > "${CLUSTER_NAME}-kubeconfig.yaml"
echo "Kubeconfig saved to: ${CLUSTER_NAME}-kubeconfig.yaml"

echo "Cluster $CLUSTER_NAME ($CLUSTER_ID) is ready!"
```

**Cluster Cleanup Script:**
```bash
#!/usr/bin/env bash
# cluster-cleanup.sh

set -euo pipefail

# Delete all clusters older than 24 hours
./osac get clusters --output json | jq -r '.[] | select(.created_at < (now - 86400)) | .id' | while read -r cluster_id; do
  echo "Deleting old cluster: $cluster_id"
  ./osac delete cluster "$cluster_id"
done

# Clean up kubeconfig files
find . -name "*-kubeconfig.yaml" -mtime +7 -delete
echo "Cleanup completed"
```

### 3. Integration with CI/CD

**GitLab CI Example:**
```yaml
# .gitlab-ci.yml
stages:
  - test
  - deploy

variables:
  OSAC_CLI_VERSION: "v1.0.0"
  KUBECONFIG_PATH: "/tmp/kubeconfig"

before_script:
  - curl -L "https://github.com/osac-project/fulfillment-service/releases/download/${OSAC_CLI_VERSION}/osac_Linux_x86_64" -o osac
  - chmod +x osac
  - mkdir -p ~/.config/osac
  - echo "$OSAC_CLI_CONFIG" > ~/.config/osac/config.json

deploy_test_cluster:
  stage: deploy
  script:
    - CLUSTER_ID=$(./osac create cluster --template ocp_4_17_small | grep "ID:" | cut -d' ' -f2)
    - echo "CLUSTER_ID=$CLUSTER_ID" >> deploy.env
    - ./osac get cluster "$CLUSTER_ID" --kubeconfig > "$KUBECONFIG_PATH"
    - export KUBECONFIG="$KUBECONFIG_PATH"
    - oc get nodes
  artifacts:
    reports:
      dotenv: deploy.env
    paths:
      - "$KUBECONFIG_PATH"
  only:
    - main

cleanup_test_cluster:
  stage: cleanup
  script:
    - ./osac delete cluster "$CLUSTER_ID"
  dependencies:
    - deploy_test_cluster
  when: always
```

## Troubleshooting

### 1. Common Issues and Solutions

**Issue: Permission Denied Error**
```bash
# Error message
Error: rpc error: code = PermissionDenied desc = Access denied

# Diagnosis steps
echo "Checking service account permissions..."
oc whoami
oc auth can-i create clusters --as=system:serviceaccount:fulfillment-system:admin

# Check authorization rules
oc get configmap -n fulfillment-system | grep fulfillment-service-config
CONFIG_MAP=$(oc get configmap -n fulfillment-system | grep fulfillment-service-config | cut -d' ' -f1)
oc get configmap "$CONFIG_MAP" -n fulfillment-system -o jsonpath='{.data.rules\.yaml}' | grep -A10 -B10 "$(oc whoami)"

# Solution: Update authorization rules
oc patch configmap "$CONFIG_MAP" -n fulfillment-system --type='merge' -p='{"data":{"rules.yaml":"..."}}'
```

**Issue: Connection Refused**
```bash
# Error message
Error: failed to connect: connection refused

# Diagnosis steps
echo "Checking service status..."
oc get pods -n fulfillment-system -l component=service
oc get services -n fulfillment-system
oc get routes -n fulfillment-system

# Check pod logs
oc logs deployment/fulfillment-service -n fulfillment-system -c server --tail=50
oc logs deployment/fulfillment-service -n fulfillment-system -c envoy --tail=50

# Test connectivity
curl -I -k https://$(oc get route fulfillment-api -n fulfillment-system -o jsonpath='{.spec.host}')

# Solution: Restart deployment if needed
oc rollout restart deployment/fulfillment-service -n fulfillment-system
oc rollout status deployment/fulfillment-service -n fulfillment-system
```

**Issue: Certificate Validation Error**
```bash
# Error message
Error: x509: certificate signed by unknown authority

# Diagnosis steps
echo "Checking certificates..."
oc get certificates -n fulfillment-system
oc describe certificate fulfillment-api -n fulfillment-system

# Check certificate details
ROUTE_HOST=$(oc get route fulfillment-api -n fulfillment-system -o jsonpath='{.spec.host}')
openssl s_client -connect "$ROUTE_HOST:443" -servername "$ROUTE_HOST" </dev/null 2>/dev/null | openssl x509 -noout -text

# Solution options:
# Option 1: Use insecure flag for development
echo '{"insecure": true, "address": "...", "token_script": "..."}' > ~/.config/osac/config.json

# Option 2: Add CA certificate to system trust store
oc get secret fulfillment-api-tls -n fulfillment-system -o jsonpath='{.data.ca\.crt}' | base64 -d > /tmp/ca.crt
sudo cp /tmp/ca.crt /etc/pki/ca-trust/source/anchors/
sudo update-ca-trust
```

### 2. Debug Mode and Logging

**Enable Comprehensive Logging:**
```bash
# Enable all logging options
./osac --log-level debug --log-headers --log-bodies get clusters

# Save logs to file
./osac --log-level debug --log-file fulfillment-debug.log get clusters

# Custom log fields
./osac --log-field "session=$(date +%s)" --log-field "user=$(whoami)" get clusters
```

**Analyze gRPC Communication:**
```bash
# Install grpcurl for manual testing
go install github.com/fullstorydev/grpcurl/cmd/grpcurl@latest

# Test server reflection
grpcurl -insecure -H "Authorization: Bearer $(oc create token admin -n fulfillment-system)" \
  fulfillment-api.apps.cluster.example.com:443 \
  list

# Test specific method
grpcurl -insecure -H "Authorization: Bearer $(oc create token admin -n fulfillment-system)" \
  fulfillment-api.apps.cluster.example.com:443 \
  fulfillment.v1.ClusterTemplates/List
```

### 3. Performance Troubleshooting

**Connection Pooling Issues:**
```bash
# Check connection limits
oc describe deployment fulfillment-service -n fulfillment-system | grep -A5 -B5 resources

# Monitor connection metrics
oc exec deployment/fulfillment-service -n fulfillment-system -c envoy -- curl -s localhost:9901/stats | grep connection

# Tune connection settings
oc patch deployment fulfillment-service -n fulfillment-system --type='merge' -p='{"spec":{"template":{"spec":{"containers":[{"name":"envoy","resources":{"limits":{"memory":"512Mi"},"requests":{"memory":"256Mi"}}}]}}}}'
```

**Timeout Issues:**
```bash
# Increase timeout in CLI configuration
cat > ~/.config/osac/config.json << 'EOF'
{
  "token_script": "...",
  "address": "...",
  "timeout": "300s"
}
EOF

# Check service timeout settings
oc get configmap fulfillment-service-envoy-xxx -n fulfillment-system -o yaml | grep timeout
```

### 4. Advanced Debugging

**Network Packet Capture:**
```bash
# Capture packets on specific interface
sudo tcpdump -i eth0 -w fulfillment-traffic.pcap host fulfillment-api.apps.cluster.example.com

# Analyze captured packets
wireshark fulfillment-traffic.pcap
```

**Container-Level Debugging:**
```bash
# Get shell in fulfillment-service container
oc exec -it deployment/fulfillment-service -n fulfillment-system -c server -- /bin/bash

# Check internal connectivity
oc exec deployment/fulfillment-service -n fulfillment-system -c server -- netstat -tlnp
oc exec deployment/fulfillment-service -n fulfillment-system -c server -- ss -tlnp

# Check file descriptors
oc exec deployment/fulfillment-service -n fulfillment-system -c server -- lsof -p 1
```

## API Reference

### 1. CLI Commands Reference

**Cluster Management:**
```bash
# Create cluster
./osac create cluster --template TEMPLATE_ID [--name NAME] [--parameters KEY=VALUE]

# List clusters
./osac get clusters [--output table|json|yaml] [--filter KEY=VALUE]

# Get specific cluster
./osac get cluster CLUSTER_ID [--output table|json|yaml]

# Describe cluster (detailed info)
./osac describe cluster CLUSTER_ID

# Update cluster
./osac edit cluster CLUSTER_ID [--parameters KEY=VALUE]

# Delete cluster
./osac delete cluster CLUSTER_ID [--force]

# Get kubeconfig
./osac get cluster CLUSTER_ID --kubeconfig [--output FILE]
```

**Hub Management:**
```bash
# Create hub
./osac create hub --id HUB_ID --kubeconfig KUBECONFIG_FILE --namespace NAMESPACE

# List hubs
./osac get hubs [--output table|json|yaml] [--filter KEY=VALUE]

# Get specific hub
./osac get hub HUB_ID [--output table|json|yaml]

# Describe hub (detailed info)
./osac describe hub HUB_ID

# Update hub configuration
./osac edit hub HUB_ID [--kubeconfig KUBECONFIG_FILE] [--namespace NAMESPACE]

# Delete hub
./osac delete hub HUB_ID [--force]
```

**Template Management:**
```bash
# List templates
./osac get clustertemplates [--output table|json|yaml]

# Get specific template
./osac get clustertemplate TEMPLATE_ID [--output table|json|yaml]

# Describe template
./osac describe clustertemplate TEMPLATE_ID
```

**Host Types:**
```bash
# List host types
./osac get hosttypes [--output table|json|yaml]

# Get specific host type
./osac get hosttype HOSTTYPE_ID [--output table|json|yaml]

# Describe host type
./osac describe hosttype HOSTTYPE_ID
```

**Configuration Management:**
```bash
# Login (interactive)
./osac login

# Login (non-interactive)
./osac login --address HOST:PORT --token-script "COMMAND"

# Logout
./osac logout

# Show current configuration
./osac config view
```

### 2. Output Formats

**Table Output (Default):**
```bash
./osac get clusters
# ID                                    TEMPLATE        STATE    API URL                          CONSOLE URL
# 672c0827-ef03-48d0-b825-689f83ff296b  ocp_4_17_small  READY    https://api.cluster1.example.com https://console.cluster1.example.com
```

**JSON Output:**
```bash
./osac get clusters --output json
# [
#   {
#     "id": "672c0827-ef03-48d0-b825-689f83ff296b",
#     "template": "ocp_4_17_small",
#     "state": "READY",
#     "api_url": "https://api.cluster1.example.com",
#     "console_url": "https://console.cluster1.example.com",
#     "created_at": "2025-01-15T10:30:00Z"
#   }
# ]
```

**YAML Output:**
```bash
./osac get clusters --output yaml
# - id: 672c0827-ef03-48d0-b825-689f83ff296b
#   template: ocp_4_17_small
#   state: READY
#   api_url: https://api.cluster1.example.com
#   console_url: https://console.cluster1.example.com
#   created_at: 2025-01-15T10:30:00Z
```

### 3. Global Options

**Logging Options:**
```bash
--log-level string        Log level (debug, info, warn, error) [default: error]
--log-file string         Log file path [default: stdout]
--log-headers             Include HTTP headers in logs
--log-bodies              Include request/response bodies in logs
--log-field stringArray   Additional log fields (key=value or %p for PID)
--log-fields strings      Comma-separated log fields
--log-redact              Enable log redaction [default: true]
```

**Connection Options:**
```bash
--address string          Override server address
--token string            Override authentication token
--insecure               Skip TLS verification
--plaintext              Use plaintext connection
--timeout duration       Request timeout [default: 30s]
```

### 4. Exit Codes

| Code | Description |
|------|-------------|
| 0 | Success |
| 1 | General error |
| 2 | Authentication error |
| 3 | Authorization error |
| 4 | Not found error |
| 5 | Connection error |
| 6 | Configuration error |
| 7 | Timeout error |

## Security Considerations

### 1. Authentication Security

**Token Management Best Practices:**
- Use short-lived tokens (1-8 hours maximum)
- Implement automatic token refresh
- Store tokens securely (mode 600 permissions)
- Use service accounts with minimal required permissions

**Example Secure Token Script:**
```bash
#!/usr/bin/env bash
# secure-token.sh
set -euo pipefail

KUBECONFIG="${KUBECONFIG:-/path/to/kubeconfig}"
NAMESPACE="${NAMESPACE:-fulfillment-system}"
SERVICE_ACCOUNT="${SERVICE_ACCOUNT:-fulfillment-admin}"
DURATION="${DURATION:-1h}"

# Validate inputs
if [[ ! -f "$KUBECONFIG" ]]; then
    echo "Error: KUBECONFIG file not found: $KUBECONFIG" >&2
    exit 1
fi

# Generate token with error handling
if ! token=$(KUBECONFIG="$KUBECONFIG" oc create token "$SERVICE_ACCOUNT" -n "$NAMESPACE" --duration "$DURATION" 2>/dev/null); then
    echo "Error: Failed to create token for $SERVICE_ACCOUNT in $NAMESPACE" >&2
    exit 1
fi

echo "$token"
```

### 2. TLS Security

**Certificate Validation:**
```bash
# Verify certificate chain
openssl s_client -connect fulfillment-api.apps.cluster.example.com:443 -verify_return_error

# Check certificate expiration
openssl s_client -connect fulfillment-api.apps.cluster.example.com:443 2>/dev/null | openssl x509 -noout -dates

# Validate certificate against CA
openssl verify -CAfile /path/to/ca.crt /path/to/server.crt
```

**TLS Configuration Hardening:**
```yaml
# envoy.yaml snippet for TLS hardening
transport_socket:
  name: envoy.transport_sockets.tls
  typed_config:
    "@type": type.googleapis.com/envoy.extensions.transport_sockets.tls.v3.DownstreamTlsContext
    common_tls_context:
      tls_params:
        tls_minimum_protocol_version: TLSv1_2
        tls_maximum_protocol_version: TLSv1_3
        cipher_suites:
          - "ECDHE-ECDSA-AES128-GCM-SHA256"
          - "ECDHE-RSA-AES128-GCM-SHA256"
          - "ECDHE-ECDSA-AES256-GCM-SHA384"
          - "ECDHE-RSA-AES256-GCM-SHA384"
```

### 3. Network Security

**Firewall Rules:**
```bash
# Allow HTTPS traffic
sudo firewall-cmd --add-port=443/tcp --permanent
sudo firewall-cmd --reload

# Restrict to specific sources
sudo firewall-cmd --add-rich-rule='rule family="ipv4" source address="10.0.0.0/8" port protocol="tcp" port="443" accept' --permanent
```

**Network Policies:**
```yaml
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: fulfillment-service-netpol
  namespace: fulfillment-system
spec:
  podSelector:
    matchLabels:
      app: fulfillment-service
  policyTypes:
  - Ingress
  - Egress
  ingress:
  - from:
    - podSelector:
        matchLabels:
          app: fulfillment-client
    ports:
    - protocol: TCP
      port: 8000
  egress:
  - to:
    - podSelector:
        matchLabels:
          app: fulfillment-database
    ports:
    - protocol: TCP
      port: 5432
```

## Best Practices

### 1. Development Workflow

**Local Development Setup:**
```bash
# Create isolated development environment
python -m venv fulfillment-dev
source fulfillment-dev/bin/activate

# Install development tools
go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
go install github.com/fullstorydev/grpcurl/cmd/grpcurl@latest

# Set up pre-commit hooks
cat > .git/hooks/pre-commit << 'EOF'
#!/usr/bin/env bash
go fmt ./...
go vet ./...
golangci-lint run
go test ./...
EOF
chmod +x .git/hooks/pre-commit
```

**Configuration Management:**
```bash
# Use environment-specific configs
export OSAC_ENV=development
config_file="$HOME/.config/osac/config-$OSAC_ENV.json"

# Template for different environments
cat > "$config_file" << 'EOF'
{
  "token_script": "oc create token admin -n {{.namespace}} --duration {{.token_duration}}",
  "address": "{{.api_endpoint}}",
  "insecure": {{.insecure_tls}}
}
EOF

# Use envsubst for variable substitution
envsubst < config-template.json > config.json
```

### 2. Monitoring and Observability

**Health Checks:**
```bash
# Create health check script
cat > health-check.sh << 'EOF'
#!/usr/bin/env bash
set -euo pipefail

echo "Checking osac CLI health..."

# Test basic connectivity
if ! timeout 10s ./osac get clustertemplates >/dev/null 2>&1; then
    echo "ERROR: Cannot connect to fulfillment service"
    exit 1
fi

# Test authentication
if ! timeout 10s ./osac get clusters >/dev/null 2>&1; then
    echo "ERROR: Authentication failed"
    exit 1
fi

echo "Health check passed"
EOF
chmod +x health-check.sh

# Set up monitoring cron job
echo "*/5 * * * * /path/to/health-check.sh" | crontab -
```

**Metrics Collection:**
```bash
# Collect CLI metrics
./osac --log-level info --log-file metrics.log get clusters
grep -o '"duration":"[^"]*"' metrics.log | cut -d'"' -f4 | sort -n

# Service-side metrics
oc exec deployment/fulfillment-service -n fulfillment-system -c envoy -- curl -s localhost:9901/stats | grep -E "(request|response|connection)"
```

### 3. Backup and Recovery

**Configuration Backup:**
```bash
# Backup CLI configuration
mkdir -p ~/.config/osac/backup
cp ~/.config/osac/config.json ~/.config/osac/backup/config-$(date +%Y%m%d-%H%M%S).json

# Backup service configuration
oc get configmap -n fulfillment-system -o yaml > fulfillment-configmaps-backup.yaml
oc get secret -n fulfillment-system -o yaml > fulfillment-secrets-backup.yaml
```

**Disaster Recovery:**
```bash
# Restore from backup
cp ~/.config/osac/backup/config-latest.json ~/.config/osac/config.json

# Restore service configuration
oc apply -f fulfillment-configmaps-backup.yaml
oc apply -f fulfillment-secrets-backup.yaml
```

---

This comprehensive guide provides everything needed to successfully deploy, configure, and operate the OSAC CLI in production environments. For additional support, consult the project documentation or contact the development team.
