"""Disposable, opt-in transport for the default Docker builder's Go mounts."""

import argparse
import base64
import hashlib
import json
import os
import re
import shutil
import stat
import subprocess
import tempfile
import time
from pathlib import Path

SCHEMA = "osac-image-go-v1"
LIMIT = 3 * 1024**3
LOG_LIMIT = 16 * 1024**2
COMPONENTS = ("fulfillment-service", "osac-metering/metering-service", "osac-operator")
MOUNTS = {
    "pkg": ("osac-go-pkg", "/opt/app-root/src/go/pkg"),
    "build": ("osac-go-build", "/opt/app-root/src/.cache/go-build"),
}
ROOT = Path(__file__).resolve().parents[2]


def input_paths(root: Path) -> list[Path]:
    """List workspace manifests and integration build inputs included in the cache key."""
    paths = {
        Path("go.work"),
        Path("osac-installer/Makefile"),
        Path(".dockerignore"),
        Path(".github/scripts/image_go_cache.py"),
        Path(".github/actions/go-image-cache/action.yaml"),
    }
    modules = re.findall(r"^\s*\./([^\s)]+)", (root / "go.work").read_text(), re.MULTILINE)
    if not modules or any(".." in Path(module).parts for module in modules):
        raise ValueError("unsupported workspace")
    for module in modules:
        paths.update(Path(module) / manifest for manifest in ("go.mod", "go.sum"))
    for component in COMPONENTS:
        paths.add(Path(component) / "Containerfile")
    return sorted(paths)


def fingerprint(root: Path, settings: dict[str, object]) -> str:
    """Hash versioned settings and file contents for an exact, prefix-free cache key."""
    digest = hashlib.sha256(json.dumps({"schema": SCHEMA, **settings}, sort_keys=True).encode())
    for relative in input_paths(root):
        digest.update(str(relative).encode() + b"\0" + (root / relative).read_bytes())
    return digest.hexdigest()


def payload_size(payload: Path, limit: int = LIMIT) -> int:
    """Reject unexpected entries and links while bounding regular-file payload bytes."""
    if payload.is_symlink() or not payload.is_dir() or {entry.name for entry in payload.iterdir()} != set(MOUNTS):
        raise ValueError("payload must contain only pkg/ and build/")
    size = 0
    for name in MOUNTS:
        directory = payload / name
        if directory.is_symlink() or not directory.is_dir():
            raise ValueError("invalid mount directory")
        for entry in directory.rglob("*"):
            info = entry.lstat()
            if stat.S_ISLNK(info.st_mode) or not (stat.S_ISREG(info.st_mode) or stat.S_ISDIR(info.st_mode)):
                raise ValueError("payload contains a link or special file")
            if stat.S_ISREG(info.st_mode):
                if info.st_nlink != 1:
                    raise ValueError("payload contains a hard link")
                size += info.st_size
                if size > limit:
                    raise ValueError("payload exceeds 3 GiB")
    return size


def check_ziphashes(root: Path, payload: Path) -> None:
    """Compare cached ziphash and module-metadata checksums with committed workspace sums."""
    expected = {}
    for relative in input_paths(root):
        if relative.name == "go.sum":
            for line in (root / relative).read_text().splitlines():
                module, version, checksum = line.split()
                escaped = re.sub(r"[A-Z]", lambda match: "!" + match[0].lower(), module)
                escaped_version = re.sub(r"[A-Z]", lambda match: "!" + match[0].lower(), version)
                filename = (
                    escaped_version.removesuffix("/go.mod") + ".mod"
                    if version.endswith("/go.mod")
                    else escaped_version + ".ziphash"
                )
                expected[f"{escaped}/@v/{filename}"] = checksum
    downloads = payload / "pkg/mod/cache/download"
    if downloads.is_dir():
        for path in downloads.rglob("*"):
            if path.suffix == ".ziphash":
                checksum = path.read_text().strip()
            elif path.suffix == ".mod":
                summary = hashlib.sha256(path.read_bytes()).hexdigest() + "  go.mod\n"
                checksum = "h1:" + base64.b64encode(hashlib.sha256(summary.encode()).digest()).decode()
            else:
                continue
            if expected.get(path.relative_to(downloads).as_posix()) != checksum:
                raise ValueError("download checksum is not in committed workspace sums")


def trusted_save(context: dict[str, object]) -> bool:
    """Allow only public manual saves on the default branch or an isolated trial branch."""
    branch = context["ref"] == f"refs/heads/{context['default_branch']}"
    trial = context.get("trial", False) and context["ref"].startswith("refs/heads/")
    return context["event"] == "workflow_dispatch" and not context["private"] and (branch or trial)


def run(arguments: list[str], **kwargs: object) -> subprocess.CompletedProcess[str]:
    """Run a checked text subprocess without suppressing command failures."""
    return subprocess.run(arguments, check=True, text=True, **kwargs)


def capture(arguments: list[str]) -> str:
    """Capture stripped stdout from a checked command."""
    return run(arguments, stdout=subprocess.PIPE).stdout.strip()


def transport_file(root: Path, image: str, operation: str) -> str:
    """Render an isolated Dockerfile for module verification or named-mount transport."""
    mounts = " ".join(
        f"--mount=type=cache,id={identity},target={target},uid=1001,gid=0" for identity, target in MOUNTS.values()
    )
    header = f"FROM {image} AS transfer\nUSER 1001:0\nWORKDIR /opt/app-root/src\n"
    if operation == "verify":
        for relative in input_paths(root):
            if relative.name in ("go.work", "go.mod", "go.sum"):
                header += f"COPY --chown=1001:0 {relative} {relative}\n"
        return (
            header + "COPY --chown=1001:0 payload/ /osac-restore/\nRUN GOMODCACHE=/osac-restore/pkg/mod go mod verify\n"
        )
    if operation == "inject":
        header += "COPY --chown=1001:0 payload/ /osac-restore/\n"
        command = " && ".join(f"cp -R /osac-restore/{name}/. {target}/" for name, (_, target) in MOUNTS.items())
    elif operation == "extract":
        paths = " ".join(target for _, target in MOUNTS.values())
        command = f"test $(du -sb {paths} | awk '{{total += $1}} END {{print total}}') -le {LIMIT}"
        command += " && mkdir -p /tmp/osac-export/pkg /tmp/osac-export/build"
        command += " && " + " && ".join(
            f"cp -R {target}/. /tmp/osac-export/{name}/" for name, (_, target) in MOUNTS.items()
        )
    elif operation == "clear":
        command = " && ".join(
            f"chmod -R u+w {target} && find {target} -mindepth 1 -maxdepth 1 -exec rm -rf {{}} +"
            for _, target in MOUNTS.values()
        )
    else:
        raise ValueError("unknown transport operation")
    result = header + f"RUN {mounts} {command}\n"
    if operation == "extract":
        result += "FROM scratch\nCOPY --from=transfer /tmp/osac-export/ /\n"
    return result


def transport(root: Path, state: Path, operation: str) -> None:
    """Execute a transport Dockerfile on the existing default Docker builder."""
    with tempfile.TemporaryDirectory(prefix="osac-transfer-") as temporary:
        context = Path(temporary)
        if operation in ("verify", "inject"):
            shutil.copytree(state / "payload", context / "payload")
        if operation == "verify":
            for relative in input_paths(root):
                if relative.name in ("go.work", "go.mod", "go.sum"):
                    (context / relative).parent.mkdir(parents=True, exist_ok=True)
                    shutil.copyfile(root / relative, context / relative)
        metadata = json.loads((state / "state.json").read_text())
        (context / "Dockerfile").write_text(transport_file(root, metadata["toolset"], operation))
        command = ["docker", "buildx", "build", "--builder", "default", "--no-cache", "--progress=plain"]
        if operation == "extract":
            shutil.rmtree(state / "payload", ignore_errors=True)
            command += ["--output", f"type=local,dest={state / 'payload'}"]
        else:
            command += ["--load", "-t", "localhost/osac-cache-transfer:trial"]
        run([*command, str(context)])


def output(name: str, value: str) -> None:
    """Append an action output when running with a GitHub output file."""
    if os.environ.get("GITHUB_OUTPUT"):
        with open(os.environ["GITHUB_OUTPUT"], "a") as stream:
            stream.write(f"{name}={value}\n")


def prepare(root: Path, state: Path, namespace: str) -> None:
    """Resolve builder and base-image identities, fingerprint inputs, and select the default builder."""
    state.mkdir(parents=True, exist_ok=True)
    if not re.fullmatch(r"[a-zA-Z0-9-]{1,100}", namespace):
        raise ValueError("invalid namespace")
    if capture(["docker", "context", "show"]) != "default":
        raise ValueError("only the default Docker context is supported")
    builder = capture(["docker", "buildx", "inspect", "default"])
    if not re.search(r"^Driver:\s+docker\s*$", builder, re.MULTILINE):
        raise ValueError("default builder must use the docker driver")
    if not re.search(r"BuildKit version:\s+\S+", builder):
        raise ValueError("default builder did not report its BuildKit version")
    for variable in ("GOPRIVATE", "GONOSUMDB", "GONOPROXY"):
        if os.environ.get(variable):
            raise ValueError("private dependency settings prohibit cache transport")
    images = {}
    for component in COMPONENTS:
        for image in re.findall(r"^FROM (\S+)", (root / component / "Containerfile").read_text(), re.MULTILINE):
            if "/" not in image or image in images:
                continue
            run(["docker", "pull", image], stdout=subprocess.DEVNULL)
            details = json.loads(capture(["docker", "image", "inspect", image]))[0]
            for setting in details["Config"].get("Env", []):
                name, _, value = setting.partition("=")
                if name in ("GOPRIVATE", "GONOSUMDB", "GONOPROXY", "GOFLAGS") and value:
                    raise ValueError("toolset has unsupported dependency settings")
                if name == "GOSUMDB" and value != "sum.golang.org":
                    raise ValueError("toolset disables public checksum verification")
                if name == "GOPROXY" and value not in ("https://proxy.golang.org,direct", "https://proxy.golang.org"):
                    raise ValueError("toolset has a non-public module proxy")
            images[image] = details["RepoDigests"][0]
    toolset_tag = re.search(r"^FROM (\S+)", (root / "fulfillment-service/Containerfile").read_text(), re.MULTILINE)[1]
    stable_builder = "\n".join(
        line.strip()
        for line in builder.splitlines()
        if re.match(r"^(Driver|BuildKit version|Platforms):", line.strip())
    )
    settings = {
        "builder": stable_builder,
        "engine": capture(["docker", "version", "--format", "{{json .Server}}"]),
        "buildx": capture(["docker", "buildx", "version"]),
        "images": images,
        "platform": [
            os.environ.get("RUNNER_OS"),
            os.environ.get("RUNNER_ARCH"),
            os.environ.get("DOCKER_DEFAULT_PLATFORM", "native"),
        ],
        "runner_image": [os.environ.get(name) for name in ("ImageOS", "ImageVersion")],
        "build_environment_sha256": hashlib.sha256(os.environ.get("GODEBUG", "http2client=0").encode()).hexdigest(),
        "build_flags": "integration-dev GODEBUG=http2client=0 VERSION=default DEBUG=false TARGETOS/ARCH=default",
    }
    metadata = {
        "key": f"{SCHEMA}-{namespace}-{fingerprint(root, settings)}",
        "settings": settings,
        "toolset": images[toolset_tag],
        "source_sha": capture(["git", "-C", str(root), "rev-parse", "HEAD"]),
        "namespace": namespace,
        "restore_started": time.time(),
        "restore": "not-attempted",
        "injection": "not-attempted",
    }
    (state / "state.json").write_text(json.dumps(metadata, indent=2) + "\n")
    if os.environ.get("GITHUB_ENV"):
        with open(os.environ["GITHUB_ENV"], "a") as stream:
            stream.write("DOCKER_BUILDKIT=1\nBUILDX_BUILDER=default\nBUILDKIT_PROGRESS=plain\n")
    output("key", metadata["key"])
    output("ready", "true")


def inject(root: Path, state: Path) -> None:
    """Validate restored data before seeding mounts, or record a disposable-cache fallback."""
    metadata = json.loads((state / "state.json").read_text())
    metadata["restore_seconds"] = time.time() - metadata["restore_started"]
    metadata["restore"] = {
        "cache_hit": os.environ.get("CACHE_HIT", ""),
        "matched_key": os.environ.get("CACHE_MATCHED_KEY", ""),
    }
    try:
        metadata["restored_bytes"] = payload_size(state / "payload")
        check_ziphashes(root, state / "payload")
        transport(root, state, "verify")
        transport(root, state, "inject")
        metadata["injection"] = "verified-and-injected"
    except (OSError, ValueError, KeyError, TypeError, IndexError, subprocess.CalledProcessError) as error:
        metadata["injection"] = "cold-fallback"
        print(f"::warning::Optional image cache unavailable ({type(error).__name__}); executing normal build")
    (state / "state.json").write_text(json.dumps(metadata, indent=2) + "\n")


def extract(root: Path, state: Path) -> None:
    """Enforce save trust, skip verified exact hits, and export only validated bounded payloads."""
    metadata = json.loads((state / "state.json").read_text())
    if os.environ.get("GITHUB_ACTIONS") == "true":
        context = {
            "event": os.environ.get("GITHUB_EVENT_NAME"),
            "ref": os.environ.get("GITHUB_REF", ""),
            "default_branch": os.environ.get("CACHE_DEFAULT_BRANCH"),
            "private": os.environ.get("CACHE_PUBLIC") != "true",
            "trial": metadata["namespace"] == f"trial-{os.environ.get('GITHUB_RUN_ID')}",
        }
        if not trusted_save(context):
            raise ValueError("cache save requires a public trusted manual context")
    restore = metadata.get("restore")
    if (
        isinstance(restore, dict)
        and isinstance(metadata.get("key"), str)
        and bool(metadata["key"])
        and restore.get("cache_hit") == "true"
        and restore.get("matched_key") == metadata.get("key")
        and metadata.get("injection") == "verified-and-injected"
    ):
        output("ready", "false")
        return
    transport(root, state, "extract")
    size = payload_size(state / "payload")
    check_ziphashes(root, state / "payload")
    transport(root, state, "verify")
    metadata = json.loads((state / "state.json").read_text())
    metadata["saved_bytes"] = size
    (state / "state.json").write_text(json.dumps(metadata, indent=2) + "\n")
    output("key", metadata["key"])
    output("ready", "true")


def consumer_command(root: Path, containerfile: Path) -> list[str]:
    """Build argv for the installer image-build macro without deployment prerequisites."""
    target = (
        "image-cache-trial: ; $(call kind-container-build,--no-cache --progress=plain "
        "--build-arg GODEBUG=$(GODEBUG) -t localhost/fulfillment-service:it -f $(CACHE_TRIAL_CONTAINERFILE) ..)"
    )
    return [
        "make",
        "-C",
        str(root / "osac-installer"),
        "--eval",
        target,
        "image-cache-trial",
        "PLATFORM=kind",
        "PROFILE=dev",
        "NS=osac",
        "CONTAINER_TOOL=docker",
        f"CACHE_TRIAL_CONTAINERFILE={containerfile}",
    ]


def observe(log: str) -> dict[str, object]:
    """Count lower-bound log observations without treating progress or restore hits as errors."""
    lines = log.splitlines()
    return {
        "observed_download_lines": sum("go: downloading " in line for line in lines),
        "observed_dependency_error_lines": sum(
            not re.search(r"(?:^|\s)go:\s+downloading\s+\S+\s+\S+\s*$", line, re.I)
            and bool(
                re.search(
                    r"(proxy\.golang\.org|sum\.golang\.org|go:).*"
                    r"(error|timeout|INTERNAL_ERROR|reset|failed)",
                    line,
                    re.I,
                )
            )
            for line in lines
        ),
        "image_build_observations": [line for line in lines if re.match(r"^#\d+ (?:[\d.]+ )?OSAC_IMAGE_CACHE_", line)],
        "limitations": "Log observations are lower bounds, not HTTP telemetry or reliability estimates.",
    }


def frozen_source(root: Path, source_sha: str) -> None:
    """Require the exact frozen commit and refuse reruns beyond the fresh-job budget."""
    if (
        not re.fullmatch(r"[0-9a-f]{40}", source_sha)
        or capture(["git", "-C", str(root), "rev-parse", "HEAD"]) != source_sha
    ):
        raise ValueError("sample must check out the exact frozen commit")
    if os.environ.get("GITHUB_RUN_ATTEMPT", "1") != "1":
        raise ValueError("reruns exceed the approved fresh-job budget")


def dependency_variant(root: Path, state: Path, source_sha: str) -> None:
    """Generate a valid public dependency-input change using the matching toolset."""
    frozen_source(root, source_sha)
    metadata = json.loads((state / "state.json").read_text())
    if any(
        "rsc.io/binaryregexp" in (root / relative).read_text()
        for relative in input_paths(root)
        if relative.name == "go.mod"
    ):
        raise ValueError("variant dependency already exists; choose a new valid fixture")
    run(
        [
            "docker",
            "run",
            "--rm",
            "--user",
            f"{os.getuid()}:{os.getgid()}",
            "-e",
            "GOWORK=off",
            "-e",
            "GOPROXY=https://proxy.golang.org",
            "-e",
            "GOSUMDB=sum.golang.org",
            "-v",
            f"{root}:/src",
            "-w",
            "/src/fulfillment-service",
            metadata["toolset"],
            "sh",
            "-ec",
            "go mod edit -require=rsc.io/binaryregexp@v0.2.0; go mod download rsc.io/binaryregexp@v0.2.0",
        ]
    )
    (state / "dependency-variant.diff").write_text(
        capture(["git", "-C", str(root), "diff", "--", "**/go.mod", "**/go.sum"])
    )


def observed_containerfile(root: Path, kind: str) -> str:
    """Instrument a temporary production Containerfile to observe actual Go mount consumption."""
    original = (root / "fulfillment-service/Containerfile").read_text()
    before = (
        "printf 'OSAC_IMAGE_CACHE_BEFORE phase=%s pkg=%s build=%s\\n' PHASE "
        '"$(find /opt/app-root/src/go/pkg -type f | wc -l)" '
        '"$(find /opt/app-root/src/.cache/go-build -type f 2>/dev/null | wc -l)"; '
    )
    variant = "$(go env GOMODCACHE)/cache/download/rsc.io/binaryregexp/@v/v0.2.0.zip"
    instrumentation = (
        before.replace("PHASE", "download") + "go env GOPATH GOMODCACHE GOCACHE GOVERSION; "
        'test "$(go env GOMODCACHE)" = /opt/app-root/src/go/pkg/mod; '
        'test "$(go env GOCACHE)" = /opt/app-root/src/.cache/go-build; '
        'test "$(id -u):$(id -g)" = 1001:0; '
        "find /opt/app-root/src -name go.sum -exec sha256sum {} + > /tmp/osac-sums; "
        + (f'test ! -f "{variant}"; ' if kind == "invalidation" else "")
        + "GODEBUG=http2client=0 go mod download; go mod verify; sha256sum -c /tmp/osac-sums; "
        + (f'test -f "{variant}"; echo OSAC_IMAGE_CACHE_VARIANT_DOWNLOADED; ' if kind == "invalidation" else "")
        + "echo OSAC_IMAGE_CACHE_VERIFIED"
    )
    needle = "GODEBUG=http2client=0 go mod download"
    if original.count(needle) != 1:
        raise ValueError("unsupported production Containerfile; revise instrumentation")
    observed = original.replace(needle, instrumentation)
    observed = observed.replace(
        'go build -gcflags="${gcflags}"', before.replace("PHASE", "compile") + 'go build -gcflags="${gcflags}"', 1
    )
    return observed.replace("./cmd/osac\n", "./cmd/osac; echo OSAC_IMAGE_CACHE_COMPILED\n", 1)


def sample(root: Path, state: Path, kind: str, source_sha: str) -> None:
    """Run the real installer build macro, retain bounded evidence, and propagate build failures."""
    frozen_source(root, source_sha)
    state.mkdir(parents=True, exist_ok=True)
    sums = {
        str(path): hashlib.sha256((root / path).read_bytes()).hexdigest()
        for path in input_paths(root)
        if path.name == "go.sum"
    }
    containerfile = state / "Containerfile.observed"
    containerfile.write_text(observed_containerfile(root, kind))
    command = consumer_command(root, containerfile)
    started = time.monotonic()
    logged_bytes = 0
    log_truncated = False
    with (
        open(state / "build.log", "wb") as stream,
        subprocess.Popen(
            command,
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
            env=os.environ | {"DOCKER_BUILDKIT": "1", "BUILDX_BUILDER": "default"},
        ) as result,
    ):
        while chunk := result.stdout.read(65536):
            retained = chunk[: max(0, LOG_LIMIT - logged_bytes)]
            stream.write(retained)
            logged_bytes += len(retained)
            log_truncated |= len(retained) != len(chunk)
        result.wait()
    report = {
        "source_sha": source_sha,
        "consumer_argv": command,
        "sample": kind,
        "build_seconds": time.monotonic() - started,
        "exit_code": result.returncode,
        "log_truncated": log_truncated,
        "sum_sha256": sums,
        "input_fingerprint": fingerprint(root, {}),
        **observe((state / "build.log").read_text(errors="replace")),
    }
    if result.returncode == 0:
        report["image_id"] = capture(
            ["docker", "image", "inspect", "localhost/fulfillment-service:it", "--format", "{{.Id}}"]
        )
        report["image_version"] = capture(
            [
                "docker",
                "image",
                "inspect",
                "localhost/fulfillment-service:it",
                "--format",
                '{{index .Config.Labels "org.opencontainers.image.version"}}',
            ]
        )
    (state / "measurement.json").write_text(json.dumps(report, indent=2) + "\n")
    for relative, checksum in sums.items():
        if hashlib.sha256((root / relative).read_bytes()).hexdigest() != checksum:
            raise ValueError("build changed a committed checksum file")
    if result.returncode:
        raise subprocess.CalledProcessError(result.returncode, "integration image build")
    observations = "\n".join(report["image_build_observations"])
    if "OSAC_IMAGE_CACHE_VERIFIED" not in observations or "OSAC_IMAGE_CACHE_COMPILED" not in observations:
        raise ValueError("no verified image-build execution")
    if kind == "warm" and not re.search(r"phase=compile pkg=[1-9]\d* build=[1-9]\d*", observations):
        raise ValueError("warm payload was not observed inside the image compilation")
    if kind == "warm" and not re.search(r"phase=download pkg=[1-9]\d*", observations):
        raise ValueError("warm modules were not observed before the image download step")
    if kind == "invalidation" and "OSAC_IMAGE_CACHE_VARIANT_DOWNLOADED" not in observations:
        raise ValueError("new dependency download was not observed")


def compatibility(root: Path, state: Path) -> None:
    """Gate mount round trips through ordinary default Docker builds, then clear trial data."""
    with tempfile.TemporaryDirectory(prefix="osac-gate-") as temporary:
        context = Path(temporary)
        for name in MOUNTS:
            directory = state / "payload" / name
            directory.mkdir(parents=True)
            (directory / "osac-compatibility").write_text("same-default-builder\n")
        metadata = json.loads((state / "state.json").read_text())
        try:
            transport(root, state, "inject")
            mounts = " ".join(
                f"--mount=type=cache,id={identity},target={target},uid=1001,gid=0"
                for identity, target in MOUNTS.values()
            )
            checks = " && ".join(
                f"grep -qx same-default-builder {target}/osac-compatibility" for _, target in MOUNTS.values()
            )
            (context / "Dockerfile").write_text(f"FROM {metadata['toolset']}\nUSER 1001:0\nRUN {mounts} {checks}\n")
            run(
                [
                    "docker",
                    "build",
                    "--no-cache",
                    "--progress=plain",
                    "-t",
                    "localhost/osac-cache-gate:trial",
                    str(context),
                ],
                env=os.environ | {"DOCKER_BUILDKIT": "1", "BUILDX_BUILDER": "default"},
            )
            transport(root, state, "extract")
            for name in MOUNTS:
                if (state / "payload" / name / "osac-compatibility").read_text() != "same-default-builder\n":
                    raise ValueError("default builder mount round trip failed")
            (state / "compatibility.json").write_text(json.dumps({"same_default_builder_roundtrip": True}) + "\n")
        finally:
            transport(root, state, "clear")
            shutil.rmtree(state / "payload", ignore_errors=True)


def main() -> None:
    """Dispatch optional-cache operations while preserving trial and real-build failures."""
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "operation", choices=("prepare", "inject", "extract", "sample", "invalidation", "compatibility")
    )
    parser.add_argument("--state", type=Path, required=True)
    parser.add_argument("--namespace", default="fulfillment-integration")
    parser.add_argument("--sample", default="baseline")
    parser.add_argument("--source-sha", default="")
    arguments = parser.parse_args()
    started = time.monotonic()
    try:
        if arguments.operation == "prepare":
            prepare(ROOT, arguments.state, arguments.namespace)
        elif arguments.operation == "sample":
            sample(ROOT, arguments.state, arguments.sample, arguments.source_sha)
        elif arguments.operation == "invalidation":
            dependency_variant(ROOT, arguments.state, arguments.source_sha)
        elif arguments.operation == "inject":
            inject(ROOT, arguments.state)
        elif arguments.operation == "compatibility":
            compatibility(ROOT, arguments.state)
        else:
            extract(ROOT, arguments.state)
    except (OSError, ValueError, KeyError, TypeError, IndexError, subprocess.CalledProcessError) as error:
        if arguments.operation in ("sample", "invalidation", "compatibility"):
            raise
        print(
            f"::warning::Optional image cache {arguments.operation} unavailable "
            f"({type(error).__name__}); normal build retained"
        )
        output("ready", "false")
    finally:
        arguments.state.mkdir(parents=True, exist_ok=True)
        with open(arguments.state / "timings.jsonl", "a") as stream:
            stream.write(json.dumps({"operation": arguments.operation, "seconds": time.monotonic() - started}) + "\n")


if __name__ == "__main__":
    main()
