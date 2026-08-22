FROM registry.access.redhat.com/ubi9/ubi:latest

ARG GRPCURL_VERSION=1.9.1
ARG OSAC_VERSION=""
ARG OSAC_CLI_BIN=""

RUN dnf install -y python3.11 python3.11-pip make jq openssh-clients && dnf clean all

SHELL ["/bin/bash", "-o", "pipefail", "-c"]
RUN curl --retry 5 --retry-delay 2 -Lsf "https://mirror.openshift.com/pub/openshift-v4/x86_64/clients/ocp/stable/openshift-client-linux.tar.gz" \
    | tar xz --no-same-owner -C /usr/local/bin oc kubectl

COPY --from=ghcr.io/astral-sh/uv:latest /uv /usr/local/bin/uv

RUN curl --retry 5 --retry-delay 2 -Lsf "https://github.com/fullstorydev/grpcurl/releases/download/v${GRPCURL_VERSION}/grpcurl_${GRPCURL_VERSION}_linux_x86_64.tar.gz" \
    | tar xz --no-same-owner -C /usr/local/bin grpcurl

COPY ${OSAC_CLI_BIN:-.dockerignore} /tmp/osac-cli-candidate

RUN set -euo pipefail; \
    if [ -n "${OSAC_CLI_BIN}" ]; then \
      mv /tmp/osac-cli-candidate /usr/local/bin/osac; \
      chmod +x /usr/local/bin/osac; \
      echo "Using pre-built OSAC CLI binary"; \
    else \
      rm -f /tmp/osac-cli-candidate; \
      if [ -z "${OSAC_VERSION}" ]; then \
        OSAC_TAG=$(curl --retry 5 --retry-delay 2 -Lsf "https://api.github.com/repos/osac-project/osac/releases?per_page=100" | jq -r '[.[] | select(.tag_name | test("^fulfillment-service/v[0-9]+\\.[0-9]+\\.[0-9]+$"))][0].tag_name // empty'); \
        [ -n "${OSAC_TAG}" ] || { echo "ERROR: no fulfillment-service release (fulfillment-service/vX.Y.Z tag) found on osac-project/osac"; exit 1; }; \
        OSAC_VERSION="${OSAC_TAG#fulfillment-service/v}"; \
        echo "Resolved latest OSAC CLI version: ${OSAC_VERSION}"; \
      else \
        OSAC_TAG="fulfillment-service/v${OSAC_VERSION}"; \
      fi; \
      curl --retry 5 --retry-delay 2 -Lsfo /usr/local/bin/osac "https://github.com/osac-project/osac/releases/download/${OSAC_TAG}/osac_Linux_x86_64" \
        || { echo "ERROR: osac binary download failed"; exit 1; }; \
      curl --retry 5 --retry-delay 2 -Lsfo /tmp/checksums.txt "https://github.com/osac-project/osac/releases/download/${OSAC_TAG}/osac_${OSAC_VERSION}_checksums.txt" \
        || { echo "ERROR: checksums file download failed"; exit 1; }; \
      line="$(grep -E '[[:space:]]osac_Linux_x86_64$' /tmp/checksums.txt || true)"; \
      [ -n "$line" ] || { echo "ERROR: osac_Linux_x86_64 entry not found in checksums file"; exit 1; }; \
      echo "$line" | sed 's|osac_Linux_x86_64|/usr/local/bin/osac|' | sha256sum -c - \
        || { echo "ERROR: checksum mismatch"; exit 1; }; \
      rm -f /tmp/checksums.txt; \
      chmod +x /usr/local/bin/osac; \
    fi

WORKDIR /tests

COPY pyproject.toml uv.lock ./
RUN uv sync --frozen --python python3.11

COPY . .
RUN rm -f osac-cli-bin

ENV PATH="/tests/.venv/bin:$PATH"
