# OSAC AAP execution environment

Tools and configuration to run playbooks that interact with both OpenStack/ESI and OpenShift.

## Building the execution environment

1. Update `requirements.txt` from `pyproject.toml` in the top directory:

    ```
    uv pip compile ../pyproject.toml > requirements.txt
    ```

2. Build the execution environment:

    ```
    ansible-builder build --tag osac-aap-ee
    ```

The build is architecture-native by default, so an Apple Silicon host produces
a `linux/arm64` image. To build a multi-architecture image with Podman, use:

```bash
make execution-environment-build \
  EE_CONTAINER_PLATFORM=linux/amd64,linux/arm64
```

The definition selects the matching Helm binary for `amd64` or `arm64`.
