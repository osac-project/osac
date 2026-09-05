#!/usr/bin/env bash
# common.sh — Shared helpers for OSAC developer scripts.

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[0;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

command_exists() {
    command -v "$1" &>/dev/null
}

print_error() {
    echo -e "${RED}ERROR: $*${NC}" >&2
}

print_warn() {
    echo -e "${YELLOW}WARN: $*${NC}" >&2
}
