#!/usr/bin/env python3
"""Regression test for OSAC-3884: the Keycloak osac-ui client must end up with
absolute redirectUris that match the browser-facing UI URL.

Keycloak does not resolve a relative redirectUri (e.g. "/*") against the Route
hostname, so shipping one makes browser login fail with "Invalid parameter:
redirect_uri".

No CI job drives the osac-ui browser authorization-code flow (CI leaves
Keycloak in-cluster and the e2e suites authenticate through the osac-cli
localhost grant), so this is the guard that keeps the client config from
drifting away from the UI Route again. It needs neither a cluster nor a
browser: it asserts on the rendered chart and then actually runs the
resolve-realm-secrets.sh hook against the committed realm.json.
"""

from __future__ import annotations

import json
import os
import pathlib
import re
import subprocess
import sys
import tempfile

try:
    import yaml
except ImportError:
    sys.exit(
        "ERROR: PyYAML is required to run this script (used to extract the\n"
        "realm ConfigMap and container env from rendered/static manifests).\n"
        "Install it with: pip install pyyaml"
    )

SCRIPT_DIR = pathlib.Path(__file__).resolve().parent
CHART_DIR = SCRIPT_DIR.parent / "charts" / "osac-infra"
PREREQ_DIR = SCRIPT_DIR.parent / "prerequisites" / "keycloak" / "service"
CHART_REALM = CHART_DIR / "files" / "realm.json"
PREREQ_REALM = PREREQ_DIR / "files" / "realm.json"
RESOLVE_HOOK = CHART_DIR / "files" / "hooks" / "resolve-realm-secrets.sh"

TEST_UI_URL = "https://osac-ui-osac.apps.example.com"
TEST_LOCAL_UI_URL = "http://ui.osac.localhost:8080"

# Stub `oc` so the hook's client-secret bootstrap runs without a cluster: the
# existence check reports "not found" (forcing the generate branch), then each
# jsonpath lookup returns a fixed base64 value.
OC_STUB = """#!/usr/bin/env bash
args="$*"
case "${args}" in
  *"-o jsonpath="*osac-controller*) printf '%s' "$(printf 'controller-secret' | base64)" ;;
  *"-o jsonpath="*osac-admin*)      printf '%s' "$(printf 'admin-secret' | base64)" ;;
  *"-o jsonpath="*osac-csi-driver*) printf '%s' "$(printf 'csi-driver-secret' | base64)" ;;
  *"-o jsonpath="*osac-ui-backend*) printf '%s' "$(printf 'ui-backend-secret' | base64)" ;;
  *"create secret"*)                exit 0 ;;
  *"get secret"*)                   exit 1 ;;  # existence check: force the "generate" branch
  *)                                exit 0 ;;
esac
"""

# Mirrors the resolvers' OSAC_UI_URL policy: an absolute scheme://host[:port]
# URL, and http:// only for the local-development hosts the chart documents on
# keycloak.uiUrl (plain HTTP would carry the authorization code in the clear).
UI_URL_RE = re.compile(r"^https?://[A-Za-z0-9._-]+(:[0-9]+)?$")

failures = 0


def fail(message: str) -> None:
    global failures
    print(f"FAIL: {message}", file=sys.stderr)
    failures += 1


def ui_url_is_valid(url: str) -> bool:
    if not UI_URL_RE.match(url):
        return False
    if not url.startswith("http://"):
        return True
    host = url[len("http://") :].split(":", 1)[0]
    return host == "localhost" or host.endswith(".localhost") or host == "127.0.0.1"


def osac_ui_client(realm: dict) -> dict | None:
    return next((c for c in realm.get("clients", []) if c.get("clientId") == "osac-ui"), None)


def resolver_env(bin_dir: pathlib.Path, **overrides: str) -> dict[str, str]:
    env = dict(os.environ)
    env["PATH"] = f"{bin_dir}{os.pathsep}{env['PATH']}"
    env["REALM_ADMIN_USERNAME"] = "admin"
    env["REALM_ADMIN_PASSWORD"] = "admin"
    env.update(overrides)
    return env


def run_script(script: pathlib.Path, env: dict[str, str]) -> int:
    return subprocess.run(
        ["bash", str(script)], env=env, capture_output=True, text=True
    ).returncode


def helm_template(*args: str) -> str:
    return subprocess.run(
        ["helm", "template", str(CHART_DIR), *args],
        check=True,
        capture_output=True,
        text=True,
    ).stdout


def render_ui_url(manifest: str) -> str | None:
    """OSAC_UI_URL declared on the resolve-realm-secrets init container."""
    for doc in yaml.safe_load_all(manifest):
        if not doc or doc.get("kind") != "Deployment":
            continue
        if doc.get("metadata", {}).get("name") != "keycloak-service":
            continue
        for container in doc["spec"]["template"]["spec"].get("initContainers", []):
            if container.get("name") != "resolve-realm-secrets":
                continue
            for env in container.get("env", []):
                if env.get("name") == "OSAC_UI_URL":
                    return env.get("value", "")
    return None


def static_init_container() -> dict | None:
    """The resolve-realm-secrets init container in the static reference manifest."""
    with open(PREREQ_DIR / "deployment.yaml") as handle:
        for doc in yaml.safe_load_all(handle):
            if not doc or doc.get("kind") != "Deployment":
                continue
            for container in doc["spec"]["template"]["spec"]["initContainers"]:
                if container.get("name") == "resolve-realm-secrets":
                    return container
    return None


def assert_resolved_ui_client(realm_file: pathlib.Path, expected_url: str, description: str) -> None:
    """The osac-ui client in a *resolved* realm.json must be browser-usable: every
    redirect/origin absolute and rooted at the expected UI URL, no placeholder left."""
    try:
        raw = realm_file.read_text()
        realm = json.loads(raw)
    except Exception as exc:  # noqa: BLE001 - reported, not raised
        fail(f"{description} -- resolved realm.json is not valid JSON: {exc}")
        return

    client = osac_ui_client(realm)
    if client is None:
        fail(f"{description} -- realm.json has no osac-ui client")
        return

    errors = []
    if client.get("rootUrl") != expected_url:
        errors.append(f"rootUrl is {client.get('rootUrl')!r}, expected {expected_url!r}")
    redirects = client.get("redirectUris") or []
    if not redirects:
        errors.append("redirectUris is empty")
    for uri in redirects:
        if not uri.startswith(expected_url + "/"):
            errors.append(f"redirectUri {uri!r} is not absolute under {expected_url!r}")
    if f"{expected_url}/callback" not in redirects:
        errors.append(f"redirectUris is missing the UI callback {expected_url}/callback")
    if client.get("webOrigins") != [expected_url]:
        errors.append(f"webOrigins is {client.get('webOrigins')!r}, expected [{expected_url!r}]")
    if "__OSAC_UI_URL__" in raw:
        errors.append("__OSAC_UI_URL__ placeholder survived substitution")

    if errors:
        fail(f"{description} -- {'; '.join(errors)}")


def test_committed_realms_template_the_url() -> None:
    print("=== Test 1: committed realm.json templates the UI URL instead of hardcoding it ===")
    for realm_path in (CHART_REALM, PREREQ_REALM):
        client = osac_ui_client(json.loads(realm_path.read_text()))
        if client is None:
            fail(f"{realm_path}: realm.json has no osac-ui client")
            continue
        relative = [u for u in (client.get("redirectUris") or []) if not u.startswith("__OSAC_UI_URL__/")]
        if (
            relative
            or client.get("rootUrl") != "__OSAC_UI_URL__"
            or client.get("webOrigins") != ["__OSAC_UI_URL__"]
        ):
            fail(
                f"{realm_path}: osac-ui rootUrl/redirectUris/webOrigins must all be "
                "built from the __OSAC_UI_URL__ placeholder"
            )


def test_chart_feeds_a_ui_url() -> None:
    print("=== Test 2: the rendered chart feeds a non-empty UI URL into the resolver ===")
    default_url = render_ui_url(helm_template())
    if default_url is None:
        fail("resolve-realm-secrets init container has no OSAC_UI_URL env var")
    elif not ui_url_is_valid(default_url):
        fail(
            "Default keycloak.uiUrl must render as an absolute scheme://host[:port] URL, "
            f"https unless it is a local-development host, got '{default_url}'"
        )

    override_url = render_ui_url(helm_template("--set", f"keycloak.uiUrl={TEST_UI_URL}"))
    if override_url is None:
        fail(
            "resolve-realm-secrets init container has no OSAC_UI_URL env var when "
            "keycloak.uiUrl is overridden"
        )
    elif override_url != TEST_UI_URL:
        fail(f"keycloak.uiUrl override must reach OSAC_UI_URL, got '{override_url}'")


def test_hook_produces_absolute_redirects(tmp: pathlib.Path, bin_dir: pathlib.Path) -> None:
    print("=== Test 3: the chart's resolver hook produces absolute osac-ui redirect URIs ===")

    def accepts(url: str, description: str, slug: str) -> None:
        out = tmp / f"resolved-{slug}.json"
        env = resolver_env(
            bin_dir,
            REALM_RAW_PATH=str(CHART_REALM),
            REALM_OUTPUT_PATH=str(out),
            OSAC_UI_URL=url,
        )
        if run_script(RESOLVE_HOOK, env) != 0:
            fail(f"resolve-realm-secrets.sh exited non-zero with {description} OSAC_UI_URL '{url}'")
            return
        if not out.is_file():
            fail(f"resolve-realm-secrets.sh did not produce a resolved realm.json for '{url}'")
            return
        assert_resolved_ui_client(out, url, f"Chart resolver output for {description} OSAC_UI_URL")

    accepts(TEST_UI_URL, "an https", "https")
    # Local development runs the UI over plain HTTP (chart default), so the HTTPS
    # requirement must not strip that host out of the realm.
    accepts(TEST_LOCAL_UI_URL, "a local-development http", "local-http")


def test_hook_rejects_bad_urls(tmp: pathlib.Path, bin_dir: pathlib.Path) -> None:
    print("=== Test 4: the resolver rejects a relative, missing, or insecure UI URL ===")
    cases = [
        ("", "resolve-realm-secrets.sh must reject an unset OSAC_UI_URL"),
        ("/", "resolve-realm-secrets.sh must reject a relative OSAC_UI_URL"),
        ("osac-ui.apps.example.com", "resolve-realm-secrets.sh must reject a scheme-less OSAC_UI_URL"),
        (
            "http://osac-ui-osac.apps.example.com",
            "resolve-realm-secrets.sh must reject a remote http:// OSAC_UI_URL "
            "(the authorization code would travel unencrypted)",
        ),
        (
            "http://localhost.apps.example.com",
            "resolve-realm-secrets.sh must reject a remote http:// OSAC_UI_URL "
            "that merely mentions localhost",
        ),
    ]
    for url, description in cases:
        env = resolver_env(
            bin_dir,
            REALM_RAW_PATH=str(CHART_REALM),
            REALM_OUTPUT_PATH=str(tmp / "rejected-realm.json"),
            OSAC_UI_URL=url,
        )
        if run_script(RESOLVE_HOOK, env) == 0:
            fail(description)


def test_static_manifest_matches(tmp: pathlib.Path, bin_dir: pathlib.Path) -> None:
    print("=== Test 5: the static reference manifest resolves the UI URL the same way ===")
    # prerequisites/keycloak/service/deployment.yaml carries its own hand-maintained
    # copy of the resolver (no shared script file to reference), so it needs direct
    # coverage or it can silently diverge from the chart's hook.
    container = static_init_container()
    if container is None or not container.get("command") or len(container["command"]) < 3:
        fail("Could not extract resolve-realm-secrets from the static Deployment manifest")
        return

    resolved_out = tmp / "static-realm-resolved.json"
    # The static copy hardcodes /realm-raw and /realm paths; redirect them into
    # the temp dir for this run only. The committed file is never touched.
    script = (
        container["command"][2]
        .replace("/realm-raw/realm.json", str(PREREQ_REALM))
        .replace("/realm/realm.json", str(resolved_out))
    )
    extracted = tmp / "extracted-static-resolve.sh"
    extracted.write_text(script)

    if run_script(extracted, resolver_env(bin_dir, OSAC_UI_URL=TEST_UI_URL)) != 0:
        fail("Static reference manifest's resolver exited non-zero with a valid OSAC_UI_URL")
    elif resolved_out.is_file():
        assert_resolved_ui_client(resolved_out, TEST_UI_URL, "Static reference manifest resolver output")
    else:
        fail("Static reference manifest's resolver did not produce a resolved realm.json")

    # The hand-maintained copy must enforce the same HTTPS rule as the hook.
    if run_script(extracted, resolver_env(bin_dir, OSAC_UI_URL="http://osac-ui-osac.apps.example.com")) == 0:
        fail("Static reference manifest's resolver must reject a remote http:// OSAC_UI_URL")

    if run_script(extracted, resolver_env(bin_dir, OSAC_UI_URL=TEST_LOCAL_UI_URL)) != 0:
        fail("Static reference manifest's resolver must accept the local-development http:// OSAC_UI_URL")

    # The manifest must declare OSAC_UI_URL -- the checks above only prove the
    # extracted logic works when the env var is supplied by hand.
    declared = next(
        (e.get("value", "") for e in container.get("env", []) if e.get("name") == "OSAC_UI_URL"),
        None,
    )
    if declared is None:
        fail("Static deployment.yaml's resolve-realm-secrets container must declare OSAC_UI_URL")
        return

    # ...and must not ship a usable default: a stand-in hostname would import a
    # realm whose osac-ui redirectUris point somewhere the operator never chose,
    # and only surface later as "Invalid parameter: redirect_uri" in a browser.
    # Applying the manifest unedited has to fail in the initContainer instead.
    if run_script(extracted, resolver_env(bin_dir, OSAC_UI_URL=declared)) == 0:
        fail(
            f"Static deployment.yaml ships OSAC_UI_URL='{declared}', which the resolver "
            "accepts; it must be empty so an unedited apply fails instead of importing "
            "a placeholder URL"
        )


def main() -> int:
    with tempfile.TemporaryDirectory() as tmpdir:
        tmp = pathlib.Path(tmpdir)
        bin_dir = tmp / "bin"
        bin_dir.mkdir()
        oc_stub = bin_dir / "oc"
        oc_stub.write_text(OC_STUB)
        oc_stub.chmod(0o755)

        test_committed_realms_template_the_url()
        test_chart_feeds_a_ui_url()
        test_hook_produces_absolute_redirects(tmp, bin_dir)
        test_hook_rejects_bad_urls(tmp, bin_dir)
        test_static_manifest_matches(tmp, bin_dir)

    print()
    if failures:
        print(f"{failures} check(s) failed.")
        return 1
    print("All Keycloak osac-ui redirect URI checks passed.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
