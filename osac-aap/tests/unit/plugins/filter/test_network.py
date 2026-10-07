import pytest

from network import dhcp_range_excluding_cidr


@pytest.mark.parametrize(
    ("subnet_cidr", "reserved_cidr", "expected"),
    [
        ("10.0.1.0/24", "", ("10.0.1.2", "10.0.1.254")),
        ("10.0.1.0/24", "10.0.1.240/28", ("10.0.1.2", "10.0.1.239")),
        ("10.0.1.0/24", "10.0.1.252/30", ("10.0.1.2", "10.0.1.251")),
    ],
    ids=["no-vip-range", "default-vip-prefix", "custom-vip-prefix"],
)
def test_dhcp_range_excludes_reserved_vip_cidr(subnet_cidr, reserved_cidr, expected):
    assert dhcp_range_excluding_cidr(subnet_cidr, reserved_cidr) == expected


@pytest.mark.parametrize(
    ("subnet_cidr", "reserved_cidr"),
    [("10.0.1.0/24", "10.0.2.240/28"), ("10.0.1.0/24", "10.0.1.224/28"), ("10.0.1.0/30", "10.0.1.0/30")],
    ids=["outside-subnet", "not-at-subnet-end", "no-dhcp-space"],
)
def test_dhcp_range_rejects_invalid_reserved_vip_cidr(subnet_cidr, reserved_cidr):
    with pytest.raises(ValueError):
        dhcp_range_excluding_cidr(subnet_cidr, reserved_cidr)
