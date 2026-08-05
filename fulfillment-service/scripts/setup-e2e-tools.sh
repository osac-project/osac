#!/bin/bash
#
# Setup script for running integration/e2e tests locally
# This installs all required tools for running: ginkgo run -v it
#
# Copyright (c) 2025 Red Hat Inc.
# Licensed under the Apache License, Version 2.0
#

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

info() {
    echo -e "${GREEN}[INFO]${NC} $1"
}

warn() {
    echo -e "${YELLOW}[WARN]${NC} $1"
}

error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

check_tool() {
    local tool=$1
    local install_func=$2

    if command -v "${tool}" &> /dev/null; then
        info "${tool} is already installed ($(command -v ${tool}))"
    else
        warn "${tool} is not installed"
        if [ -n "${install_func}" ]; then
            ${install_func}
        fi
    fi
}

install_ginkgo() {
    info "Installing Ginkgo..."
    cd "${PROJECT_ROOT}"

    # Get the version from go.mod
    GINKGO_MODULE="github.com/onsi/ginkgo/v2"
    GINKGO_VERSION=$(go list -f '{{ .Version }}' -m "${GINKGO_MODULE}")

    info "Installing Ginkgo ${GINKGO_VERSION}..."
    go install "${GINKGO_MODULE}/ginkgo@${GINKGO_VERSION}"

    if command -v ginkgo &> /dev/null; then
        info "Ginkgo installed successfully: $(ginkgo version)"
    else
        error "Ginkgo installation failed"
        exit 1
    fi
}

install_kind() {
    info "Installing kind..."

    # Detect OS
    OS=$(uname -s | tr '[:upper:]' '[:lower:]')
    ARCH=$(uname -m)

    case "${ARCH}" in
        x86_64) ARCH="amd64" ;;
        aarch64) ARCH="arm64" ;;
    esac

    KIND_VERSION="v0.20.0"
    KIND_URL="https://kind.sigs.k8s.io/dl/${KIND_VERSION}/kind-${OS}-${ARCH}"

    info "Downloading kind from ${KIND_URL}..."
    curl -sSLo /tmp/kind "${KIND_URL}"
    chmod +x /tmp/kind

    # Install to user's local bin
    mkdir -p "${HOME}/.local/bin"
    mv /tmp/kind "${HOME}/.local/bin/kind"

    # Add to PATH if not already there
    if [[ ":$PATH:" != *":${HOME}/.local/bin:"* ]]; then
        warn "Add ${HOME}/.local/bin to your PATH:"
        echo "  export PATH=\"\${HOME}/.local/bin:\${PATH}\""
    fi

    info "kind installed to ${HOME}/.local/bin/kind"
}

install_kubectl() {
    info "Installing kubectl..."

    # Detect OS
    OS=$(uname -s | tr '[:upper:]' '[:lower:]')
    ARCH=$(uname -m)

    case "${ARCH}" in
        x86_64) ARCH="amd64" ;;
        aarch64) ARCH="arm64" ;;
    esac

    # Get latest stable version
    KUBECTL_VERSION=$(curl -L -s https://dl.k8s.io/release/stable.txt)
    KUBECTL_URL="https://dl.k8s.io/release/${KUBECTL_VERSION}/bin/${OS}/${ARCH}/kubectl"

    info "Downloading kubectl ${KUBECTL_VERSION} from ${KUBECTL_URL}..."
    curl -sSLo /tmp/kubectl "${KUBECTL_URL}"
    chmod +x /tmp/kubectl

    # Install to user's local bin
    mkdir -p "${HOME}/.local/bin"
    mv /tmp/kubectl "${HOME}/.local/bin/kubectl"

    info "kubectl installed to ${HOME}/.local/bin/kubectl"
}

install_helm() {
    info "Installing Helm..."

    # Use the official helm install script
    curl -fsSL https://raw.githubusercontent.com/helm/helm/main/scripts/get-helm-3 | bash

    if command -v helm &> /dev/null; then
        info "Helm installed successfully: $(helm version --short)"
    else
        error "Helm installation failed"
        exit 1
    fi
}

install_openssl() {
    # Detect OS
    if [[ "$OSTYPE" == "darwin"* ]]; then
        info "Installing openssl via Homebrew..."
        if command -v brew &> /dev/null; then
            brew install openssl
        else
            error "Homebrew not found. Please install Homebrew first: https://brew.sh"
            exit 1
        fi
    elif [ -f /etc/fedora-release ] || [ -f /etc/redhat-release ]; then
        info "Installing openssl via dnf..."
        sudo dnf install -y openssl
    elif [ -f /etc/debian_version ]; then
        info "Installing openssl via apt..."
        sudo apt-get update && sudo apt-get install -y openssl
    else
        error "Unknown OS. Please install openssl manually."
        exit 1
    fi
}

check_openssl() {
    if command -v openssl &> /dev/null; then
        info "openssl is installed: $(openssl version)"
        return 0
    else
        warn "openssl is not installed (required for helm checksum verification)"
        read -p "Would you like to install openssl now? (y/n) " -r REPLY
        if [[ $REPLY =~ ^[Yy]$ ]]; then
            install_openssl
        else
            error "Cannot proceed with helm installation without openssl"
            exit 1
        fi
    fi
}

check_podman_or_docker() {
    if command -v podman &> /dev/null; then
        info "podman is installed: $(podman --version)"
        return 0
    elif command -v docker &> /dev/null; then
        info "docker is installed: $(docker --version)"
        return 0
    else
        warn "Neither podman nor docker is installed"
        warn "kind requires a container runtime. Please install podman or docker:"
        warn "  Fedora/RHEL: sudo dnf install podman"
        warn "  Ubuntu: sudo apt install docker.io"
        warn "  macOS: brew install podman"
        return 1
    fi
}

main() {
    info "Setting up tools for integration tests..."
    info "Project root: ${PROJECT_ROOT}"
    echo ""

    # Check for openssl first (required for helm)
    check_openssl

    # Check and install tools
    check_tool "ginkgo" "install_ginkgo"
    check_tool "kind" "install_kind"
    check_tool "kubectl" "install_kubectl"
    check_tool "helm" "install_helm"
    check_podman_or_docker

    echo ""
    info "Setup complete! You should now be able to run:"
    info "  cd ${PROJECT_ROOT}"
    info "  ginkgo run -v it"
    echo ""
    info "If you see 'command not found', add to your PATH:"
    info "  export PATH=\"\${HOME}/.local/bin:\${PATH}\""
    echo ""
    info "To keep the test cluster for debugging:"
    info "  IT_KEEP_KIND=true ginkgo run -v it"
}

main "$@"
