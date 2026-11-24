"""Exercise the rendered worker route-selection service with fake host tools."""

import os
import subprocess
from pathlib import Path


SCRIPT = (
    Path(__file__).resolve().parents[2]
    / "collections/ansible_collections/osac/service/roles/worker_network/templates/"
    / "worker_network_machine_config.sh.j2"
)


def _install_fake_tools(tmp_path: Path, selected_macs: list[str]) -> Path:
    log = tmp_path / "nmcli.log"
    bin_dir = tmp_path / "bin"
    bin_dir.mkdir()
    (bin_dir / "ip").write_text(
        "#!/usr/bin/env bash\n"
        "exit 0\n"
    )
    (bin_dir / "nmcli").write_text(
        "#!/usr/bin/env bash\n"
        f"printf '%s\\n' \"$*\" >> {str(log)!r}\n"
        "case \"$*\" in\n"
        "  '-t -f DEVICE,TYPE device status')\n"
        "    printf '%s\\n' 'ens6:ethernet' 'ens7:ethernet' 'br-ex:bridge'\n"
        "    ;;\n"
        "  '-g GENERAL.HWADDR device show ens6') printf '%s\\n' 'aa:bb:cc:dd:ee:06' ;;&\n"
        "  '-g GENERAL.HWADDR device show ens7') printf '%s\\n' 'aa:bb:cc:dd:ee:07' ;;&\n"
        "  '-g GENERAL.CONNECTION device show ens6') printf '%s\\n' tenant6 ;;&\n"
        "  '-g GENERAL.CONNECTION device show ens7') printf '%s\\n' tenant7 ;;&\n"
        "  '-g GENERAL.CONNECTION device show br-ex') printf '%s\\n' management ;;&\n"
        "  '-g ipv4.method connection show tenant6') printf '%s\\n' auto ;;&\n"
        "  '-g ipv4.method connection show tenant7') printf '%s\\n' auto ;;&\n"
        "  '-g ipv4.method connection show management') printf '%s\\n' manual ;;&\n"
        "esac\n"
    )
    for tool in (bin_dir / "ip", bin_dir / "nmcli"):
        tool.chmod(0o755)
    rendered = tmp_path / "worker-network.sh"
    rendered.write_text(
        SCRIPT.read_text().replace(
            "{{ worker_network_selected_macs | map('lower') | join(' ') }}",
            " ".join(selected_macs),
        )
    )
    rendered.chmod(0o755)
    return log


def test_route_selection_uses_selected_mac(tmp_path: Path) -> None:
    log = _install_fake_tools(tmp_path, ["aa:bb:cc:dd:ee:06"])
    env = os.environ | {"PATH": f"{tmp_path / 'bin'}:/usr/bin:/bin"}

    result = subprocess.run(
        ["bash", str(log.parent / "worker-network.sh")], env=env, capture_output=True, text=True, timeout=5
    )

    assert result.returncode == 0, result.stderr
    calls = log.read_text()
    assert "connection modify tenant6 ipv4.never-default no ipv4.route-metric 10" in calls


def test_route_selection_does_not_follow_another_nic_metric(
    tmp_path: Path,
) -> None:
    log = _install_fake_tools(tmp_path, ["aa:bb:cc:dd:ee:06"])
    env = os.environ | {"PATH": f"{tmp_path / 'bin'}:/usr/bin:/bin"}

    result = subprocess.run(
        ["bash", str(log.parent / "worker-network.sh")], env=env, capture_output=True, text=True, timeout=5
    )

    assert result.returncode == 0, result.stderr
    calls = log.read_text()
    assert "connection modify tenant6 ipv4.never-default no ipv4.route-metric 10" in calls
    assert "connection modify tenant7 ipv4.never-default yes ipv4.route-metric 100" in calls
