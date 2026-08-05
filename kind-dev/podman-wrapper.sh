#!/bin/sh
# Podman wrapper for distrobox — delegates to the host via distrobox-host-exec.
# Set PODMAN_ROOTFUL=1 to use the system (rootful) podman socket.
# The rootful socket requires a one-time host setup (see Containerfile header).
ROOTFUL_SOCKET="/run/podman/podman.sock"

if [ "${PODMAN_ROOTFUL:-0}" = "1" ]; then
  exec distrobox-host-exec env CONTAINER_HOST="unix://${ROOTFUL_SOCKET}" podman "$@"
else
  exec distrobox-host-exec podman "$@"
fi
