"""Keep fulfillment image inputs aligned with its workspace dependencies."""

from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]


def test_fulfillment_image_copies_pull_secret_workspace_source() -> None:
    source = "osac-operator/pkg/pullsecret/"
    containerfile = (ROOT / "fulfillment-service/Containerfile").read_text()

    assert (ROOT / source / "pull_secret.go").is_file()
    assert f"COPY --chown=1001:1001 {source} {source}" in containerfile
