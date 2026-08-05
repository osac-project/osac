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
