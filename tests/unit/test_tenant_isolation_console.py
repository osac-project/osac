from __future__ import annotations

import pytest

from tests.e2e.vmaas.regression.tenant_isolation_console import _canonical_dest_ip, _logged_in


@pytest.mark.parametrize(
    ("dest_ip", "expected"),
    [
        ("10.1.2.3", "10.1.2.3"),
        (" 10.1.2.3 ", "10.1.2.3"),
        ("::1", "::1"),
        ("2001:db8::1", "2001:db8::1"),
        ("::ffff:10.1.2.3", "::ffff:10.1.2.3"),
    ],
)
def test_canonical_dest_ip_accepts_unscoped_addresses(dest_ip: str, expected: str) -> None:
    """Unscoped IPv4 and IPv6 destinations canonicalize for guest ping interpolation."""
    assert _canonical_dest_ip(dest_ip) == expected


@pytest.mark.parametrize(
    "dest_ip",
    [
        "not-an-ip",
        "fe80::1%eth0",
        "fe80::1%1",
        "fe80::1%;reboot",
        "fe80::1%eth0;id",
        "fe80::1%$(id)",
        "fe80::1%`id`",
        "fe80::1%eth0 && id",
        "fe80::1%eth0|id",
    ],
)
def test_canonical_dest_ip_rejects_invalid_and_scoped_addresses(dest_ip: str) -> None:
    """Reject non-addresses and IPv6 zone IDs that would reach the guest shell."""
    with pytest.raises(ValueError, match="invalid dest_ip"):
        _canonical_dest_ip(dest_ip)


@pytest.mark.parametrize(
    "text",
    [
        "[fedora@localhost ~]$ ",
        "fedora@localhost:~$ ",
        "root@localhost:~# ",
        "$",
        "# ",
        "banner with $ inside\n[fedora@localhost ~]$",
    ],
)
def test_logged_in_accepts_prompt_on_final_nonempty_line(text: str) -> None:
    """A prompt-shaped last line, including username@, counts as a logged-in shell."""
    assert _logged_in(text, "fedora") is True


@pytest.mark.parametrize(
    "text",
    [
        "MOTD: packages cost $0\nlogin: ",
        "##### cloud-init #####\nlocalhost login: ",
        "Login incorrect\n[fedora@localhost ~]$ ",
        "login incorrect\nfedora@localhost:~$ ",
        "",
        "\n\n",
    ],
)
def test_logged_in_rejects_markers_not_on_final_prompt_line(text: str) -> None:
    """Shell markers elsewhere, login-incorrect, and empty output are not a shell."""
    assert _logged_in(text, "fedora") is False


def test_logged_in_escapes_username_in_prompt_match() -> None:
    """Usernames with regex metacharacters still match username@ on the last line."""
    assert _logged_in("user.name@host", "user.name") is True
    assert _logged_in("username@host", "user.name") is False
