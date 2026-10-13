#!/usr/bin/python
# -*- coding: utf-8 -*-

# (c) 2019, Simon Dodsley (simon@purestorage.com)
# GNU General Public License v3.0+ (see COPYING or
# https://www.gnu.org/licenses/gpl-3.0.txt)

from __future__ import absolute_import, division, print_function

__metaclass__ = type

ANSIBLE_METADATA = {
    "metadata_version": "1.1",
    "status": ["preview"],
    "supported_by": "community",
}

DOCUMENTATION = r"""
---
module: purefb_info
version_added: '1.0.0'
short_description: Collect information from Everpure FlashBlade
description:
  - Collect information from a Everpure FlashBlade running the
    Purity//FB operating system. By default, the module will collect basic
    information including hosts, host groups, protection
    groups and volume counts. Additional information can be collected
    based on the configured set of arguements.
author:
  - Everpure Ansible Team (@sdodsley) <pure-ansible-team@purestorage.com>
options:
  gather_subset:
    description:
      - When supplied, this argument will define the information to be collected.
        Possible values for this include all, minimum, config, performance,
        capacity, network, subnets, lags, filesystems, snapshots, buckets,
        replication, policies, arrays, accounts, admins, ad, kerberos,
        drives, servers and fleet.
    required: false
    type: list
    elements: str
    default: minimum
extends_documentation_fragment:
  - purestorage.flashblade.purestorage.fb
"""

EXAMPLES = r"""
- name: collect default set of info
  purestorage.flashblade.purefb_info:
    fb_url: 10.10.10.2
    api_token: T-55a68eb5-c785-4720-a2ca-8b03903bf641
  register: blade_info
- name: show default information
  debug:
    msg: "{{ blade_info['purefb_info']['default'] }}"

- name: collect configuration and capacity info
  purestorage.flashblade.purefb_info:
    gather_subset:
      - config
    fb_url: 10.10.10.2
    api_token: T-55a68eb5-c785-4720-a2ca-8b03903bf641
  register: blade_info
- name: show config information
  debug:
    msg: "{{ blade_info['purefb_info']['config'] }}"

- name: collect all info
  purestorage.flashblade.purefb_info:
    gather_subset:
      - all
    fb_url: 10.10.10.2
    api_token: T-55a68eb5-c785-4720-a2ca-8b03903bf641
  register: blade_info
- name: show all information
  debug:
    msg: "{{ blade_info['purefb_info'] }}"
"""

RETURN = r"""
purefb_info:
  description: Returns the information collected from the FlashBlade
  returned: always
  type: dict
"""


from ansible.module_utils.basic import AnsibleModule
from ansible_collections.purestorage.flashblade.plugins.module_utils.purefb import (
    get_system,
    purefb_argument_spec,
)
from ansible_collections.purestorage.flashblade.plugins.module_utils.time_utils import (
    milliseconds_to_time,
)
from datetime import datetime, timezone
import time

DRIVES_API_VERSION = "2.5"
SECURITY_API_VERSION = "2.7"
BUCKET_API_VERSION = "2.8"
SMB_CLIENT_API_VERSION = "2.10"
SPACE_API_VERSION = "2.11"
PUBLIC_API_VERSION = "2.12"
NAP_API_VERSION = "2.13"
RA_DURATION_API_VERSION = "2.14"
SMTP_ENCRYPT_API_VERSION = "2.15"
SERVERS_API_VERSION = "2.16"
FLEET_API_VERSION = "2.17"


def _bytes_to_human(bytes_number):
    if bytes_number:
        labels = ["B/s", "KB/s", "MB/s", "GB/s", "TB/s", "PB/s"]
        i = 0
        double_bytes = bytes_number
        while i < len(labels) and bytes_number >= 1024:
            double_bytes = bytes_number / 1024.0
            i += 1
            bytes_number = bytes_number / 1024
        return str(round(double_bytes, 2)) + " " + labels[i]
    return None


def generate_default_dict(blade):
    default_info = {}
    api_version = list(blade.get_versions().items)[0]
    defaults = list(blade.get_arrays().items)[0]
    default_info["flashblade_name"] = defaults.name
    default_info["purity_version"] = defaults.version
    default_info["filesystems"] = blade.get_file_systems().total_item_count
    default_info["snapshots"] = blade.get_file_system_snapshots().total_item_count
    default_info["buckets"] = blade.get_buckets().total_item_count
    default_info["object_store_users"] = blade.get_object_store_users().total_item_count
    default_info["object_store_accounts"] = (
        blade.get_object_store_accounts().total_item_count
    )
    default_info["blades"] = blade.get_blades().total_item_count
    default_info["certificates"] = blade.get_certificates().total_item_count
    default_info["total_capacity"] = list(blade.get_arrays_space().items)[0].capacity
    default_info["api_versions"] = api_version
    default_info["policies"] = blade.get_policies_all().total_item_count
    # Count policies by type
    all_policies = list(blade.get_policies_all().items)
    default_info["snapshot_policies"] = sum(
        1 for p in all_policies if p.policy_type == "snapshot"
    )
    default_info["access_policies"] = sum(
        1 for p in all_policies if p.policy_type == "access"
    )
    default_info["nfs_policies"] = sum(
        1 for p in all_policies if p.policy_type == "nfs"
    )
    default_info["smb_share_policies"] = sum(
        1 for p in all_policies if p.policy_type == "smb_share"
    )
    default_info["smb_client_policies"] = sum(
        1 for p in all_policies if p.policy_type == "smb_client"
    )
    default_info["network_policies"] = sum(
        1 for p in all_policies if p.policy_type == "network"
    )
    default_info["worm_policies"] = sum(
        1 for p in all_policies if p.policy_type == "worm"
    )
    default_info["telemetry_metrics_policies"] = sum(
        1 for p in all_policies if p.policy_type == "telemetry-metrics"
    )
    default_info["certificate_groups"] = blade.get_certificate_groups().total_item_count
    default_info["fs_replicas"] = blade.get_file_system_replica_links().total_item_count
    default_info["remote_credentials"] = (
        blade.get_object_store_remote_credentials().total_item_count
    )
    default_info["bucket_replicas"] = blade.get_bucket_replica_links().total_item_count
    default_info["connected_arrays"] = blade.get_array_connections().total_item_count
    default_info["targets"] = blade.get_targets().total_item_count
    default_info["kerberos_keytabs"] = blade.get_keytabs().total_item_count
    default_info["syslog_servers"] = len(blade.get_syslog_servers().items)
    default_info["object_store_virtual_hosts"] = len(
        blade.get_object_store_virtual_hosts().items
    )
    default_info["api_clients"] = len(blade.get_api_clients().items)
    default_info["idle_timeout"] = int(defaults.idle_timeout / 60000)
    if list(blade.get_arrays_eula().items)[0].signature.accepted:
        default_info["EULA"] = "Signed"
    else:
        default_info["EULA"] = "Not Signed"
    admin_settings = list(blade.get_admins_settings().items)[0]
    default_info["max_login_attempts"] = admin_settings.max_login_attempts
    default_info["min_password_length"] = admin_settings.min_password_length
    if admin_settings.lockout_duration:
        default_info["lockout_duration"] = (
            str(admin_settings.lockout_duration / 1000) + " seconds"
        )
    default_info["smb_mode"] = getattr(defaults, "smb_mode", None)
    default_info["timezone"] = defaults.time_zone
    default_info["product_type"] = getattr(defaults, "product_type", "Unknown")
    if SECURITY_API_VERSION in api_version:
        dar = defaults.encryption.data_at_rest
        default_info["encryption"] = {
            "data_at_rest_enabled": dar.enabled,
            "data_at_rest_algorithms": dar.algorithms,
            "data_at_rest_entropy_source": dar.entropy_source,
        }
        keys = list(blade.get_support_verification_keys().items)
        default_info["support_keys"] = {}
        for key in keys:
            keyname = key.name
            default_info["support_keys"][keyname] = {key.verification_key}
        default_info["security_update"] = getattr(defaults, "security_update", None)
    if NAP_API_VERSION in api_version:
        default_info["network_access_protocol"] = getattr(
            defaults.network_access_policy, "name", "None"
        )
    ra_info = list(blade.get_support().items)[0]
    if ra_info.remote_assist_active:
        ra_expires = datetime.fromtimestamp(
            int(ra_info.remote_assist_expires) / 1000
        ).strftime("%Y-%m-%d %H:%M:%S")
        ra_opened = datetime.fromtimestamp(
            int(ra_info.remote_assist_opened) / 1000
        ).strftime("%Y-%m-%d %H:%M:%S")
    else:
        ra_expires = ra_opened = None
    default_info["remote_assist"] = {
        "phonehome_enabled": ra_info.phonehome_enabled,
        "proxy": ra_info.proxy,
        "ra_active": ra_info.remote_assist_active,
        "ra_expires": ra_expires,
        "ra_opened": ra_opened,
        "ra_status": ra_info.remote_assist_status,
    }
    if RA_DURATION_API_VERSION in api_version:
        default_info["remote_assist"]["ra_duration"] = ra_info.remote_assist_duration
    return default_info


def generate_perf_dict(blade):
    perf_info = {}
    total_perf = list(blade.get_arrays_performance().items)[0]
    http_perf = list(blade.get_arrays_performance(protocol="http").items)[0]
    s3_perf = list(blade.get_arrays_performance(protocol="s3").items)[0]
    nfs_perf = list(blade.get_arrays_performance(protocol="nfs").items)[0]
    perf_info["aggregate"] = {
        "bytes_per_op": total_perf.bytes_per_op,
        "bytes_per_read": total_perf.bytes_per_read,
        "bytes_per_write": total_perf.bytes_per_write,
        "read_bytes_per_sec": total_perf.read_bytes_per_sec,
        "reads_per_sec": total_perf.reads_per_sec,
        "usec_per_other_op": total_perf.usec_per_other_op,
        "usec_per_read_op": total_perf.usec_per_read_op,
        "usec_per_write_op": total_perf.usec_per_write_op,
        "write_bytes_per_sec": total_perf.write_bytes_per_sec,
        "writes_per_sec": total_perf.writes_per_sec,
    }
    perf_info["http"] = {
        "bytes_per_op": http_perf.bytes_per_op,
        "bytes_per_read": http_perf.bytes_per_read,
        "bytes_per_write": http_perf.bytes_per_write,
        "read_bytes_per_sec": http_perf.read_bytes_per_sec,
        "reads_per_sec": http_perf.reads_per_sec,
        "usec_per_other_op": http_perf.usec_per_other_op,
        "usec_per_read_op": http_perf.usec_per_read_op,
        "usec_per_write_op": http_perf.usec_per_write_op,
        "write_bytes_per_sec": http_perf.write_bytes_per_sec,
        "writes_per_sec": http_perf.writes_per_sec,
    }
    perf_info["s3"] = {
        "bytes_per_op": s3_perf.bytes_per_op,
        "bytes_per_read": s3_perf.bytes_per_read,
        "bytes_per_write": s3_perf.bytes_per_write,
        "read_bytes_per_sec": s3_perf.read_bytes_per_sec,
        "reads_per_sec": s3_perf.reads_per_sec,
        "usec_per_other_op": s3_perf.usec_per_other_op,
        "usec_per_read_op": s3_perf.usec_per_read_op,
        "usec_per_write_op": s3_perf.usec_per_write_op,
        "write_bytes_per_sec": s3_perf.write_bytes_per_sec,
        "writes_per_sec": s3_perf.writes_per_sec,
    }
    perf_info["nfs"] = {
        "bytes_per_op": nfs_perf.bytes_per_op,
        "bytes_per_read": nfs_perf.bytes_per_read,
        "bytes_per_write": nfs_perf.bytes_per_write,
        "read_bytes_per_sec": nfs_perf.read_bytes_per_sec,
        "reads_per_sec": nfs_perf.reads_per_sec,
        "usec_per_other_op": nfs_perf.usec_per_other_op,
        "usec_per_read_op": nfs_perf.usec_per_read_op,
        "usec_per_write_op": nfs_perf.usec_per_write_op,
        "write_bytes_per_sec": nfs_perf.write_bytes_per_sec,
        "writes_per_sec": nfs_perf.writes_per_sec,
    }
    if blade.get_array_connections_performance_replication().total_item_count > 0:
        file_repl_perf = list(
            blade.get_array_connections_performance_replication(
                type="file-system"
            ).items
        )[0]
        obj_repl_perf = list(
            blade.get_array_connections_performance_replication(
                type="object-store"
            ).items
        )[0]
        perf_info["file_replication"] = {
            "received_bytes_per_sec": getattr(
                file_repl_perf.periodic, "received_bytes_per_sec", None
            ),
            "transmitted_bytes_per_sec": getattr(
                file_repl_perf.periodic, "transmitted_bytes_per_sec", None
            ),
        }
        perf_info["object_replication"] = {
            "received_bytes_per_sec": getattr(
                obj_repl_perf.periodic, "received_bytes_per_sec", None
            ),
            "transmitted_bytes_per_sec": getattr(
                obj_repl_perf.periodic, "transmitted_bytes_per_sec", None
            ),
        }
    return perf_info


def generate_config_dict(blade):
    config_info = {}
    api_version = list(blade.get_versions().items)
    config_info["dns"] = {}
    dns_configs = list(blade.get_dns().items)
    for config in dns_configs:
        config_info["dns"][config.name] = {
            "nameservers": config.nameservers,
            "domain": config.domain,
            "services": getattr(config, "services", None),
        }
        if hasattr(config, "sources"):
            config_info["dns"][config.name]["source"] = getattr(
                config.sources, "name", None
            )
    smtp_config = list(blade.get_smtp_servers().items)
    config_info["smtp"] = {}
    for config in smtp_config:
        config_info["smtp"][config.name] = {
            "relay_host": getattr(config, "relay_host", None),
            "sender_domain": getattr(config, "sender_domain", None),
            "encryption_mode": getattr(config, "encryption_mode", None),
        }
    alert_config = list(blade.get_alert_watchers().items)
    config_info["alert_watchers"] = {}
    for config in alert_config:
        config_info["alert_watchers"][config.name] = {
            "enabled": config.enabled,
            "minimum_notification_severity": config.minimum_notification_severity,
        }
    directory_services = list(blade.get_directory_services().items)
    for ds_service in directory_services:
        if ds_service.name in {"management", "nfs", "smb"}:
            key = f"{ds_service.name}_directory_service"
            config_info[key] = {
                "base_dn": ds_service.base_dn,
                "bind_user": ds_service.bind_user,
                "ca_certificate": ds_service.ca_certificate.name,
                "ca_certificate_group": ds_service.ca_certificate_group.name,
                "enabled": ds_service.enabled,
                "management": {
                    "user_login_attribute": ds_service.management.user_login_attribute,
                    "user_object_class": ds_service.management.user_object_class,
                },
                "nis_servers": ds_service.nfs.nis_servers,
                "nis_domains": ds_service.nfs.nis_domains,
                "services": ds_service.services,
                "join_ou": ds_service.smb.join_ou,
                "uris": ds_service.uris,
            }
    # Forward backwards compatability
    config_info["array_management"] = config_info["management_directory_service"]

    config_info["directory_service_roles"] = {}
    roles = list(blade.get_directory_services_roles().items)
    for ds_role in roles:
        role_name = ds_role.name
        config_info["directory_service_roles"][role_name] = {
            "group": ds_role.group,
            "group_base": ds_role.group_base,
            "role": ds_role.role.name,
        }
    config_info["ntp"] = list(blade.get_arrays().items)[0].ntp_servers
    certs = list(blade.get_certificates().items)
    config_info["ssl_certs"] = {}
    for cert in certs:
        cert_name = cert.name
        valid_from = time.strftime(
            "%a, %d %b %Y %H:%M:%S %Z",
            time.localtime(cert.valid_from / 1000),
        )
        valid_to = time.strftime(
            "%a, %d %b %Y %H:%M:%S %Z",
            time.localtime(cert.valid_to / 1000),
        )
        config_info["ssl_certs"][cert_name] = {
            "certificate": getattr(cert, "certificate", None),
            "certificate_type": getattr(cert, "certificatei_type", None),
            "common_name": getattr(cert, "common_name", None),
            "country": getattr(cert, "country", None),
            "email": getattr(cert, "email", None),
            "intermediate_certificate": getattr(
                cert, "intermeadiate_certificate", None
            ),
            "issued_by": getattr(cert, "issued_by", None),
            "issued_to": getattr(cert, "issued_to", None),
            "key_size": getattr(cert, "key_size", None),
            "locality": getattr(cert, "locality", None),
            "organization": getattr(cert, "organization", None),
            "organizational_unit": getattr(cert, "organizational_unit", None),
            "state": getattr(cert, "state", None),
            "status": getattr(cert, "status", None),
            "subject_alternative_names": getattr(
                cert, "subject_alternative_names", None
            ),
            "valid_from": valid_from,
            "valid_to": valid_to,
        }
    crt_grps = list(blade.get_certificate_groups().items)
    config_info["certificate_groups"] = []
    for crt_grp in crt_grps:
        config_info["certificate_groups"].append(crt_grp.name)
    config_info["syslog_servers"] = {}
    syslog_servers = list(blade.get_syslog_servers().items)
    for server in syslog_servers:
        server_name = server.name
        config_info["syslog_servers"][server_name] = {
            "uri": server.uri,
            "services": getattr(server, "services", None),
        }
    snmp_agents = list(blade.get_snmp_agents().items)
    config_info["snmp_agents"] = {}
    for agent in snmp_agents:
        agent_name = agent.name
        config_info["snmp_agents"][agent_name] = {
            "version": agent.version,
            "engine_id": agent.engine_id,
        }
        if config_info["snmp_agents"][agent_name]["version"] == "v3":
            config_info["snmp_agents"][agent_name]["auth_protocol"] = getattr(
                agent.v3, "auth_protocol", None
            )
            config_info["snmp_agents"][agent_name]["privacy_protocol"] = getattr(
                agent.v3, "privacy_protocol", None
            )
            config_info["snmp_agents"][agent_name]["user"] = getattr(
                agent.v3, "user", None
            )
    config_info["snmp_managers"] = {}
    snmp_managers = list(blade.get_snmp_managers().items)
    for manager in snmp_managers:
        mgr_name = manager.name
        config_info["snmp_managers"][mgr_name] = {
            "version": manager.version,
            "host": manager.host,
            "notification": manager.notification,
        }
        if config_info["snmp_managers"][mgr_name]["version"] == "v3":
            config_info["snmp_managers"][mgr_name]["auth_protocol"] = getattr(
                manager.v3, "auth_protocol", None
            )
            config_info["snmp_managers"][mgr_name]["privacy_protocol"] = getattr(
                manager.v3, "privacy_protocol", None
            )
            config_info["snmp_managers"][mgr_name]["user"] = getattr(
                manager.v3, "user", None
            )
    if SMTP_ENCRYPT_API_VERSION in api_version:
        config_info["saml2sso"] = {}
        saml2 = list(blade.get_sso_saml2_idps().items)
        if saml2:
            config_info["saml2sso"] = {
                "enabled": saml2[0].enabled,
                "array_url": saml2[0].array_url,
                "name": saml2[0].name,
                "idp": {
                    "url": getattr(saml2[0].idp, "url", None),
                    "encrypt_enabled": saml2[0].idp.encrypt_assertion_enabled,
                    "sign_enabled": saml2[0].idp.sign_request_enabled,
                    "metadata_url": saml2[0].idp.metadata_url,
                },
                "sp": {
                    "decrypt_cred": getattr(
                        saml2[0].sp.decryption_credential, "name", None
                    ),
                    "sign_cred": getattr(saml2[0].sp.signing_credential, "name", None),
                },
            }
    return config_info


def generate_subnet_dict(blade):
    sub_info = {}
    subnets = list(blade.get_subnets().items)
    for sub in subnets:
        sub_name = sub.name
        if sub.enabled:
            sub_info[sub_name] = {
                "gateway": sub.gateway,
                "mtu": sub.mtu,
                "vlan": sub.vlan,
                "prefix": sub.prefix,
                "services": sub.services,
            }
            sub_info[sub_name]["lag"] = sub.link_aggregation_group.name
            sub_info[sub_name]["interfaces"] = []
            for iface in range(len(sub.interfaces)):
                sub_info[sub_name]["interfaces"].append(
                    {"name": sub.interfaces[iface].name}
                )
    return sub_info


def generate_lag_dict(blade):
    lag_info = {}
    groups = list(blade.get_link_aggregation_groups().items)
    for groupcnt in groups:
        lag_name = groupcnt.name
        lag_info[lag_name] = {
            "lag_speed": groupcnt.lag_speed,
            "port_speed": groupcnt.port_speed,
            "mac_address": getattr(groupcnt, "mac_address", None),
            "status": groupcnt.status,
        }
        lag_info[lag_name]["ports"] = []
        for port in groupcnt.ports:
            lag_info[lag_name]["ports"].append({"name": port.name})
    return lag_info


def generate_admin_dict(blade):
    admin_info = {}
    admins = list(blade.get_admins().items)
    for admin in admins:
        admin_name = admin.name
        admin_info[admin_name] = {
            "public_key": admin.public_key,
            "local": admin.is_local,
            "role": admin.role.name,
            "locked": admin.locked,
            "lockout_remaining": getattr(admin, "lockout_remaining", None),
        }
        if hasattr(admin.api_token, "expires_at"):
            if admin.api_token.expires_at:
                admin_info[admin_name]["token_expires"] = datetime.fromtimestamp(
                    admin.api_token.expires_at / 1000
                ).strftime("%Y-%m-%d %H:%M:%S")
        else:
            admin_info[admin_name]["token_expires"] = None
        if hasattr(admin.api_token, "created_at"):
            if admin.api_token.created_at:
                admin_info[admin_name]["token_created"] = datetime.fromtimestamp(
                    admin.api_token.created_at / 1000
                ).strftime("%Y-%m-%d %H:%M:%S")
        else:
            admin_info[admin_name]["token_created"] = None
    return admin_info


def generate_targets_dict(blade):
    targets_info = {}
    targets = list(blade.get_targets().items)
    for target in targets:
        target_name = target.name
        targets_info[target_name] = {
            "address": target.address,
            "status": target.status,
            "status_details": target.status_details,
            "ca_certificate_group": getattr(
                getattr(target, "ca_certificate_group", None), "name", None
            ),
        }
    return targets_info


def generate_remote_creds_dict(blade):
    remote_creds_info = {}
    remote_creds = list(blade.get_object_store_remote_credentials().items)
    for remote_cred in remote_creds:
        cred_name = remote_cred.name
        remote_creds_info[cred_name] = {
            "access_key": remote_cred.access_key_id,
            "remote_array": remote_cred.remote.name,
            "secret_access_key": remote_cred.secret_access_key,
        }
    return remote_creds_info


def generate_file_repl_dict(blade):
    file_repl_info = {}
    file_links = list(blade.get_file_system_replica_links().items)
    for file_link in file_links:
        fs_name = file_link.local_file_system.name
        file_repl_info[fs_name] = {
            "direction": file_link.direction,
            "link_type": file_link.link_type,
            "lag": file_link.lag,
            "status": file_link.status,
            "status_detail": file_link.status_detail,
            "remote_fs": file_link.remote.name
            + ":"
            + file_link.remote_file_system.name,
            "recovery_point": file_link.recovery_point,
        }
        file_repl_info[fs_name]["policies"] = []
        for policy_cnt in file_link.policies:
            file_repl_info[fs_name]["policies"].append(policy_cnt.display_name)
    return file_repl_info


def generate_bucket_repl_dict(blade):
    bucket_repl_info = {}
    bucket_links = list(blade.get_bucket_replica_links().items)
    for bucket_link in bucket_links:
        bucket_name = bucket_link.local_bucket.name
        bucket_repl_info[bucket_name] = {
            "direction": bucket_link.direction,
            "lag": bucket_link.lag,
            "paused": bucket_link.paused,
            "status": bucket_link.status,
            "status_details": bucket_link.status_details,
            "remote_bucket": bucket_link.remote_bucket.name,
            "remote_array": bucket_link.remote.name,
            "remote_credentials": bucket_link.remote_credentials.name,
            "recovery_point": bucket_link.recovery_point,
            "object_backlog": {
                "bytes_count": bucket_link.object_backlog.bytes_count,
                "delete_ops_count": bucket_link.object_backlog.delete_ops_count,
                "other_ops_count": bucket_link.object_backlog.other_ops_count,
                "put_ops_count": bucket_link.object_backlog.put_ops_count,
            },
            "cascading_enabled": bucket_link.cascading_enabled,
        }
    return bucket_repl_info


def generate_network_dict(blade):
    net_info = {}
    ports = list(blade.get_network_interfaces().items)
    for port in ports:
        int_name = port.name
        net_info[int_name] = {
            "type": getattr(port, "type", None),
            "mtu": getattr(port, "mtu", None),
            "vlan": getattr(port, "vlan", None),
            "address": getattr(port, "address", None),
            "services": getattr(port, "services", None),
            "gateway": getattr(port, "gateway", None),
            "netmask": getattr(port, "netmask", None),
            "server": getattr(getattr(port, "server", None), "name", None),
            "subnet": getattr(getattr(port, "subnet", None), "name", None),
            "enabled": port.enabled,
        }
    return net_info


def generate_capacity_dict(blade):
    capacity_info = {}
    total_cap = list(blade.get_arrays_space().items)[0]
    file_cap = list(blade.get_arrays_space(type="file-system").items)[0]
    object_cap = list(blade.get_arrays_space(type="object-store").items)[0]
    capacity_info["total"] = total_cap.capacity
    capacity_info["aggregate"] = {
        "data_reduction": total_cap.space.data_reduction,
        "snapshots": total_cap.space.snapshots,
        "total_physical": total_cap.space.total_physical,
        "unique": total_cap.space.unique,
        "virtual": total_cap.space.virtual,
        "total_provisioned": total_cap.space.total_provisioned,
        "available_provisioned": total_cap.space.available_provisioned,
        "available_ratio": total_cap.space.available_ratio,
        "destroyed": total_cap.space.destroyed,
        "destroyed_virtual": total_cap.space.destroyed_virtual,
        "shared": getattr(total_cap.space, "shared", None),
    }
    capacity_info["file-system"] = {
        "data_reduction": file_cap.space.data_reduction,
        "snapshots": file_cap.space.snapshots,
        "total_physical": file_cap.space.total_physical,
        "unique": file_cap.space.unique,
        "virtual": file_cap.space.virtual,
        "total_provisioned": total_cap.space.total_provisioned,
        "available_provisioned": total_cap.space.available_provisioned,
        "available_ratio": total_cap.space.available_ratio,
        "destroyed": total_cap.space.destroyed,
        "destroyed_virtual": total_cap.space.destroyed_virtual,
        "shared": getattr(total_cap.space, "shared", None),
    }
    capacity_info["object-store"] = {
        "data_reduction": object_cap.space.data_reduction,
        "snapshots": object_cap.space.snapshots,
        "total_physical": object_cap.space.total_physical,
        "unique": object_cap.space.unique,
        "virtual": file_cap.space.virtual,
        "total_provisioned": total_cap.space.total_provisioned,
        "available_provisioned": total_cap.space.available_provisioned,
        "available_ratio": total_cap.space.available_ratio,
        "destroyed": total_cap.space.destroyed,
        "destroyed_virtual": total_cap.space.destroyed_virtual,
        "shared": getattr(total_cap.space, "shared", None),
    }

    return capacity_info


def generate_snap_dict(blade):
    snap_info = {}
    snaps = list(blade.get_file_system_snapshots().items)
    api_version = list(blade.get_versions().items)
    for snap in snaps:
        snapshot = snap.name
        snap_info[snapshot] = {
            "destroyed": snap.destroyed,
            "source": snap.source.location.name,
            "suffix": snap.suffix,
            "created": datetime.fromtimestamp(
                snap.created / 1000,
                tz=timezone.utc,
            ).strftime("%Y-%m-%dT%H:%M:%SZ"),
            "time_remaining": getattr(snap, "time_remaining", None),
            "policy": getattr(getattr(snap, "policy", None), "name", None),
            "owner": snap.owner.name,
            "owner_destroyed": snap.owner_destroyed,
            "source_display_name": snap.source.display_name,
            "source_is_local": snap.source.is_local,
            "source_location": snap.source.location.name,
            "policies": [],
        }
        if PUBLIC_API_VERSION in api_version:
            if hasattr(snap, "policies"):
                for policy in snap.policies:
                    snap_info[snapshot]["policies"].append(
                        {
                            "name": policy.name,
                            "location": policy.location.name,
                        }
                    )
    return snap_info


def generate_snap_transfer_dict(blade):
    snap_transfer_info = {}
    snap_transfers = list(blade.get_file_system_snapshots_transfer().items)
    for snap_transfer in snap_transfers:
        transfer = snap_transfer.name
        snap_transfer_info[transfer] = {
            "completed": snap_transfer.completed,
            "data_transferred": snap_transfer.data_transferred,
            "progress": snap_transfer.progress,
            "direction": snap_transfer.direction,
            "remote": snap_transfer.remote.name,
            "remote_snapshot": snap_transfer.remote_snapshot.name,
            "started": snap_transfer.started,
            "status": snap_transfer.status,
        }
    return snap_transfer_info


def generate_array_conn_dict(blade):
    array_conn_info = {}
    arrays = list(blade.get_array_connections().items)
    for array in arrays:
        array = array.remote.name
        array_conn_info[array] = {
            "encrypted": array.encrypted,
            "replication_addresses": array.replication_addresses,
            "management_address": array.management_address,
            "status": array.status,
            "version": array.version,
            "ca_certificate_group": array.ca_certificate_group.name,
            "throttle": {
                "default_limit": _bytes_to_human(
                    getattr(array.throttle, "default_limit", None)
                ),
                "window_limit": _bytes_to_human(
                    getattr(array.throttle, "window_limit", None)
                ),
                "window_start": (
                    milliseconds_to_time(getattr(array.throttle.window, "start", None))
                    if getattr(array.throttle.window, "start", None)
                    else None
                ),
                "window_end": (
                    milliseconds_to_time(getattr(array.throttle.window, "end", None))
                    if getattr(array.throttle.window, "end", None)
                    else None
                ),
            },
        }
    return array_conn_info


def generate_policies_dict(blade):
    policies_info = {}
    policies = list(blade.get_policies_all().items)
    for policy in policies:
        policy_name = policy.name
        policies_info[policy_name] = {
            "enabled": policy.enabled,
            "retention_lock": getattr(policy, "retention_lock", None),
            "policy_type": policy.policy_type,
            "rules": {},
        }
        if policy.rules:
            policies_info[policy_name]["rules"] = {
                "at": getattr(policy.rules[0], "at", None),
                "every": getattr(policy.rules[0], "every", None),
                "keep_for": getattr(policy.rules[0], "keep_for", None),
                "time_zone": getattr(policy.rules[0], "time_zone", None),
            }
    return policies_info


def generate_bucket_dict(blade):
    bucket_info = {}
    buckets = list(blade.get_buckets().items)
    for bucket in buckets:
        bucket_name = bucket.name
        bucket_info[bucket_name] = {
            "versioning": bucket.versioning,
            "bucket_type": getattr(bucket, "bucket_type", None),
            "object_count": bucket.object_count,
            "id": bucket.id,
            "account_name": bucket.account.name,
            "data_reduction": getattr(bucket.space, "data_reduction", None),
            "snapshot_space": bucket.space.snapshots,
            "total_physical_space": bucket.space.total_physical,
            "unique_space": bucket.space.unique,
            "virtual_space": bucket.space.virtual,
            "total_provisioned_space": getattr(bucket.space, "total_provisioned", None),
            "available_provisioned_space": getattr(
                bucket.space, "available_provisioned", None
            ),
            "available_ratio": getattr(bucket.space, "available_ratio", None),
            "destroyed_space": getattr(bucket.space, "destroyed", None),
            "destroyed_virtual_space": getattr(bucket.space, "destroyed_virtual", None),
            "created": bucket.created,
            "destroyed": bucket.destroyed,
            "time_remaining": getattr(bucket, "time_remaining", None),
            "time_remaining_status": getattr(bucket, "time_remaining_status", None),
            "retention_lock": bucket.retention_lock,
            "quota_limit": bucket.quota_limit,
            "object_lock_config": {
                "enabled": bucket.object_lock_config.enabled,
                "freeze_locked_objects": bucket.object_lock_config.freeze_locked_objects,
                "default_retention": getattr(
                    bucket.object_lock_config, "default_retention", None
                ),
                "default_retention_mode": getattr(
                    bucket.object_lock_config,
                    "default_retention_mode",
                    None,
                ),
            },
            "eradication_config": {
                "eradication_delay": getattr(
                    bucket.eradication_config, "eradication_delay", None
                ),
                "eradication_mode": getattr(
                    bucket.eradication_config, "eradication_mode", None
                ),
                "manual_eradication": bucket.eradication_config.manual_eradication,
            },
            "public_status": getattr(bucket, "public_status", None),
            "public_access_config": {
                "block_new_public_policies": getattr(
                    getattr(bucket, "public_access_config", None),
                    "block_new_public_policies",
                    None,
                ),
                "block_public_access": getattr(
                    getattr(bucket, "public_access_config", None),
                    "block_public_access",
                    None,
                ),
            },
            "lifecycle_rules": {},
        }
        if not bucket.destroyed:
            all_rules = list(blade.get_lifecycle_rules(bucket_ids=[bucket.id]).items)
            for rule in all_rules:
                bucket_name = rule.bucket.name
                rule_id = rule.rule_id
                if rule.keep_previous_version_for:
                    keep_previous_version_for = int(
                        rule.keep_previous_version_for / 86400000
                    )
                else:
                    keep_previous_version_for = None
                if rule.keep_current_version_for:
                    keep_current_version_for = int(
                        rule.keep_current_version_for / 86400000
                    )
                else:
                    keep_current_version_for = None
                if rule.abort_incomplete_multipart_uploads_after:
                    abort_incomplete_multipart_uploads_after = int(
                        rule.abort_incomplete_multipart_uploads_after / 86400000
                    )
                else:
                    abort_incomplete_multipart_uploads_after = None
                if rule.keep_current_version_until:
                    keep_current_version_until = datetime.fromtimestamp(
                        rule.keep_current_version_until / 1000
                    ).strftime("%Y-%m-%d")
                else:
                    keep_current_version_until = None
                bucket_info[bucket_name]["lifecycle_rules"][rule_id] = {
                    "keep_previous_version_for (days)": keep_previous_version_for,
                    "keep_current_version_for (days)": keep_current_version_for,
                    "keep_current_version_until": keep_current_version_until,
                    "prefix": rule.prefix,
                    "enabled": rule.enabled,
                    "abort_incomplete_multipart_uploads_after (days)": abort_incomplete_multipart_uploads_after,
                    "cleanup_expired_object_delete_marker": rule.cleanup_expired_object_delete_marker,
                }

    return bucket_info


def generate_kerb_dict(blade):
    kerb_info = {}

    for keytab in blade.get_keytabs().items:
        prefix = keytab.prefix
        suffix = keytab.suffix

        if prefix not in kerb_info:
            kerb_info[prefix] = {}

        kerb_info[prefix][suffix] = {
            "fqdn": keytab.fqdn,
            "kvno": keytab.kvno,
            "principal": keytab.principal,
            "realm": keytab.realm,
            "encryption_type": keytab.encryption_type,
            "server": getattr(getattr(keytab, "server", None), "name", None),
            "source": getattr(getattr(keytab, "source", None), "name", None),
        }

    return kerb_info


def generate_ad_dict(blade):
    ad_info = {}

    active_directory = blade.get_active_directory()
    if active_directory.total_item_count == 0:
        return ad_info

    for ad in active_directory.items:
        ad_info[ad.name] = {
            "computer": ad.computer_name,
            "domain": ad.domain,
            "directory_servers": ad.directory_servers,
            "kerberos_servers": ad.kerberos_servers,
            "service_principals": ad.service_principal_names,
            "join_ou": ad.join_ou,
            "encryption_types": ad.encryption_types,
            "global_catalog_servers": getattr(ad, "global_catalog_servers", None),
            "server": getattr(getattr(ad, "server", None), "name", None),
        }

    return ad_info


def generate_bucket_access_policies_dict(blade):
    policies_info = {}

    for bucket in blade.get_buckets().items:
        res = blade.get_buckets_bucket_access_policies(bucket_names=[bucket.name])

        if res.status_code == 200 and res.total_item_count != 0:
            for policy in res.items:
                policies_info[policy.name] = {
                    "description": policy.description,
                    "enabled": policy.enabled,
                    "local": policy.is_local,
                    "rules": [],
                }

                for rule in policy.rules:
                    policies_info[policy.name]["rules"].append(
                        {
                            "actions": rule.actions,
                            "resources": rule.resources,
                            "all_principals": rule.principals.all,
                            "effect": rule.effect,
                            "name": rule.name,
                        }
                    )

    return policies_info


def generate_bucket_cross_object_policies_dict(blade):
    policies_info = {}

    for bucket in blade.get_buckets().items:
        res = blade.get_buckets_cross_origin_resource_sharing_policies(
            bucket_names=[bucket.name]
        )

        for policy in res.items:
            policies_info[policy.name] = {
                "allowed_headers": policy.allowed_headers,
                "allowed_methods": policy.allowed_methods,
                "allowed_origins": policy.allowed_origins,
            }

    return policies_info


def generate_object_store_access_policies_dict(blade):
    policies_info = {}

    for policy in blade.get_object_store_access_policies().items:
        policy_name = policy.name
        policies_info[policy_name] = {
            "ARN": policy.arn,
            "description": policy.description,
            "enabled": policy.enabled,
            "local": policy.is_local,
            "rules": [],
        }

        for rule in policy.rules:
            policies_info[policy_name]["rules"].append(
                {
                    "actions": rule.actions,
                    "conditions": {
                        "source_ips": rule.conditions.source_ips,
                        "s3_delimiters": rule.conditions.s3_delimiters,
                        "s3_prefixes": rule.conditions.s3_prefixes,
                    },
                    "effect": rule.effect,
                    "name": rule.name,
                }
            )

    return policies_info


def generate_nfs_export_policies_dict(blade):
    policies_info = {}

    for policy in blade.get_nfs_export_policies().items:
        policy_name = policy.name
        policies_info[policy_name] = {
            "local": policy.is_local,
            "enabled": policy.enabled,
            "rules": [],
        }

        for rule in policy.rules:
            policies_info[policy_name]["rules"].append(
                {
                    "access": rule.access,
                    "anongid": rule.anongid,
                    "anonuid": rule.anonuid,
                    "atime": rule.atime,
                    "client": rule.client,
                    "fileid_32bit": rule.fileid_32bit,
                    "permission": rule.permission,
                    "secure": rule.secure,
                    "security": rule.security,
                    "index": rule.index,
                }
            )

    return policies_info


def generate_smb_client_policies_dict(blade):
    policies_info = {}

    for policy in blade.get_smb_client_policies().items:
        policy_name = policy.name
        policies_info[policy_name] = {
            "local": policy.is_local,
            "enabled": policy.enabled,
            "version": policy.version,
            "rules": [],
        }

        for rule in policy.rules:
            policies_info[policy_name]["rules"].append(
                {
                    "name": rule.name,
                    "change": getattr(rule, "change", None),
                    "full_control": getattr(rule, "full_control", None),
                    "principal": getattr(rule, "principal", None),
                    "read": getattr(rule, "read", None),
                    "client": getattr(rule, "client", None),
                    "index": getattr(rule, "index", None),
                    "policy_version": getattr(rule, "policy_version", None),
                    "encryption": getattr(rule, "encryption", None),
                    "permission": getattr(rule, "permission", None),
                }
            )

    return policies_info


def generate_object_store_accounts_dict(blade):
    account_info = {}

    for account in blade.get_object_store_accounts().items:
        acc_name = account.name
        space = account.space

        account_info[acc_name] = {
            "object_count": account.object_count,
            "data_reduction": space.data_reduction,
            "snapshots_space": space.snapshots,
            "total_physical_space": space.total_physical,
            "unique_space": space.unique,
            "virtual_space": space.virtual,
            "total_provisioned_space": getattr(space, "total_provisioned", None),
            "available_provisioned_space": getattr(
                space, "available_provisioned", None
            ),
            "available_ratio": getattr(space, "available_ratio", None),
            "destroyed_space": getattr(space, "destroyed", None),
            "destroyed_virtual_space": getattr(space, "destroyed_virtual", None),
            "quota_limit": getattr(account, "quota_limit", None),
            "hard_limit_enabled": getattr(account, "hard_limit_enabled", None),
            "total_provisioned": getattr(space, "total_provisioned", None),
            "users": {},
            "bucket_defaults": {
                "hard_limit_enabled": getattr(
                    getattr(account, "bucket_defaults", None),
                    "hard_limit_enabled",
                    None,
                ),
                "quota_limit": getattr(
                    getattr(account, "bucket_defaults", None),
                    "quota_limit",
                    None,
                ),
            },
            "public_access_config": {
                "block_new_public_policies": getattr(
                    getattr(account, "public_access_config", None),
                    "block_new_public_policies",
                    None,
                ),
                "block_public_access": getattr(
                    getattr(account, "public_access_config", None),
                    "block_public_access",
                    None,
                ),
            },
        }

        # Users for this account
        acc_users = blade.get_object_store_users(filter=f'name="{acc_name}/*"').items

        for acc_user in acc_users:
            user_name = acc_user.name.split("/")[1]
            account_info[acc_name]["users"][user_name] = {
                "keys": [],
                "policies": [],
            }

            # Access Keys
            keys_res = blade.get_object_store_access_keys(
                filter=f'user.name="{acc_user.name}"'
            )
            if keys_res.total_item_count != 0:
                for key in keys_res.items:
                    account_info[acc_name]["users"][user_name]["keys"].append(
                        {
                            "name": key.name,
                            "enabled": bool(key.enabled),
                        }
                    )

            # Policies
            policies_res = blade.get_object_store_access_policies_object_store_users(
                member_names=[acc_user.name]
            )
            if policies_res.total_item_count != 0:
                for policy in policies_res.items:
                    account_info[acc_name]["users"][user_name]["policies"].append(
                        policy.policy.name
                    )

    return account_info


def generate_fs_dict(blade):
    fs_info = {}
    for fsystem in blade.get_file_systems().items:
        share = fsystem.name

        nfs = getattr(fsystem, "nfs", None)
        smb = getattr(fsystem, "smb", None)
        multi = getattr(fsystem, "multi_protocol", None)
        source = getattr(fsystem, "source", None)
        location = getattr(fsystem, "location", None)

        fs_info[share] = {
            "fast_remove": fsystem.fast_remove_directory_enabled,
            "snapshot_enabled": fsystem.snapshot_directory_enabled,
            "provisioned": fsystem.provisioned,
            "destroyed": fsystem.destroyed,
            "nfs_rules": getattr(nfs, "rules", None),
            "nfs_v3": getattr(nfs, "v3_enabled", False),
            "nfs_v4_1": getattr(nfs, "v4_1_enabled", False),
            "user_quotas": {},
            "group_quotas": {},
            "http": fsystem.http.enabled,
            "smb_mode": getattr(smb, "acl_mode", None),
            "multi_protocol": {
                "safegaurd_acls": getattr(multi, "safeguard_acls", None),
                "access_control_style": getattr(multi, "access_control_style", None),
            },
            "hard_limit": fsystem.hard_limit_enabled,
            "promotion_status": fsystem.promotion_status,
            "requested_promotion_state": fsystem.requested_promotion_state,
            "writable": fsystem.writable,
            "source": {
                "is_local": getattr(source, "is_local", None),
                "name": getattr(source, "name", None),
                "location": getattr(location, "name", None),
            },
            "default_group_quota": fsystem.default_group_quota,
            "default_user_quota": fsystem.default_user_quota,
            "export_policy": getattr(getattr(nfs, "export_policy", None), "name", None),
            "smb_client_policy": getattr(
                getattr(smb, "client_policy", None), "name", None
            ),
            "smb_share_policy": getattr(
                getattr(smb, "share_policy", None), "name", None
            ),
            "smb_continuous_availability_enabled": getattr(
                smb, "continuous_availability_enabled", False
            ),
            "multi_protocol_access_control_style": getattr(
                multi, "access_control_style", None
            ),
            "multi_protocol_safeguard_acls": getattr(multi, "safeguard_acls", None),
        }

        # Group quotas
        for group_quota in blade.get_quotas_groups(file_system_names=[share]).items:
            group_name = group_quota.name.rsplit("/", 1)[1]
            fs_info[share]["group_quotas"][group_name] = {
                "group_id": getattr(group_quota.group, "id", None),
                "group_name": getattr(group_quota.group, "name", None),
                "quota": group_quota.quota,
                "usage": group_quota.usage,
            }

        # User quotas
        for user_quota in blade.get_quotas_users(file_system_names=[share]).items:
            user_name = user_quota.name.rsplit("/", 1)[1]
            fs_info[share]["user_quotas"][user_name] = {
                "user_id": getattr(user_quota.user, "id", None),
                "user_name": getattr(user_quota.user, "name", None),
                "quota": user_quota.quota,
                "usage": user_quota.usage,
            }

    return fs_info


def generate_drives_dict(blade):
    """
    Drives information is only available for the Legend chassis.
    The Legend chassis product_name has // in it so only bother if
    that is the case.
    """
    drives_info = {}
    arrays = list(blade.get_arrays().items)
    if not arrays:
        return drives_info

    product_type = getattr(arrays[0], "product_type", "")

    if "//" not in product_type:
        return drives_info

    for drive in blade.get_drives().items:
        drives_info[drive.name] = {
            "progress": getattr(drive, "progress", None),
            "raw_capacity": getattr(drive, "raw_capacity", None),
            "status": getattr(drive, "status", None),
            "details": getattr(drive, "details", None),
            "type": getattr(drive, "type", None),
        }
    return drives_info


def generate_servers_dict(blade):
    servers_info = {}
    for server in blade.get_servers().items:
        servers_info[server.name] = {
            "created": datetime.fromtimestamp(server.created / 1000).strftime(
                "%Y-%m-%d %H:%M:%S"
            ),
            "dns": [dns.name for dns in server.dns],
            "directory_services": [ds.name for ds in server.directory_services],
        }
    return servers_info


def generate_fleet_dict(blade):
    fleet_items = list(blade.get_fleets().items)
    if not fleet_items:
        return {}

    fleet_name = fleet_items[0].name
    members = blade.get_fleets_members().items

    fleet_info = {
        fleet_name: {
            "members": {
                member.member.name: {
                    "status": member.status,
                    "status_details": member.status_details,
                }
                for member in members
            }
        }
    }
    return fleet_info


def main():
    argument_spec = purefb_argument_spec()
    argument_spec.update(
        dict(gather_subset=dict(default="minimum", type="list", elements="str"))
    )

    module = AnsibleModule(argument_spec, supports_check_mode=True)

    blade = get_system(module)
    api_versions = list(blade.get_versions().items)

    if not module.params["gather_subset"]:
        module.params["gather_subset"] = ["minimum"]
    subset = [test.lower() for test in module.params["gather_subset"]]
    valid_subsets = (
        "all",
        "minimum",
        "config",
        "performance",
        "capacity",
        "network",
        "subnets",
        "lags",
        "filesystems",
        "snapshots",
        "buckets",
        "arrays",
        "replication",
        "policies",
        "accounts",
        "admins",
        "ad",
        "kerberos",
        "drives",
        "servers",
        "fleet",
    )
    subset_test = (test in valid_subsets for test in subset)
    if not all(subset_test):
        module.fail_json(
            msg="value must gather_subset must be one or more of: %s, got: %s"
            % (",".join(valid_subsets), ",".join(subset))
        )

    info = {}

    if "minimum" in subset or "all" in subset:
        info["default"] = generate_default_dict(blade)
    if "performance" in subset or "all" in subset:
        info["performance"] = generate_perf_dict(blade)
    if "config" in subset or "all" in subset:
        info["config"] = generate_config_dict(blade)
    if "capacity" in subset or "all" in subset:
        info["capacity"] = generate_capacity_dict(blade)
    if "lags" in subset or "all" in subset:
        info["lag"] = generate_lag_dict(blade)
    if "network" in subset or "all" in subset:
        info["network"] = generate_network_dict(blade)
    if "subnets" in subset or "all" in subset:
        info["subnet"] = generate_subnet_dict(blade)
    if "filesystems" in subset or "all" in subset:
        info["filesystems"] = generate_fs_dict(blade)
    if "admins" in subset or "all" in subset:
        info["admins"] = generate_admin_dict(blade)
    if "snapshots" in subset or "all" in subset:
        info["snapshots"] = generate_snap_dict(blade)
    if "buckets" in subset or "all" in subset:
        info["buckets"] = generate_bucket_dict(blade)
    if "policies" in subset or "all" in subset:
        info["policies"] = generate_policies_dict(blade)
        info["snapshot_policies"] = generate_policies_dict(blade)
    if "arrays" in subset or "all" in subset:
        info["arrays"] = generate_array_conn_dict(blade)
    if "replication" in subset or "all" in subset:
        info["file_replication"] = generate_file_repl_dict(blade)
        info["bucket_replication"] = generate_bucket_repl_dict(blade)
        info["snap_transfers"] = generate_snap_transfer_dict(blade)
        info["remote_credentials"] = generate_remote_creds_dict(blade)
        info["targets"] = generate_targets_dict(blade)
    if "accounts" in subset or "all" in subset:
        info["accounts"] = generate_object_store_accounts_dict(blade)
    if "ad" in subset or "all" in subset:
        info["active_directory"] = generate_ad_dict(blade)
    if "kerberos" in subset or "all" in subset:
        info["kerberos"] = generate_kerb_dict(blade)
    if "policies" in subset or "all" in subset:
        info["access_policies"] = generate_object_store_access_policies_dict(blade)
        if PUBLIC_API_VERSION in api_versions:
            info["bucket_access_policies"] = generate_bucket_access_policies_dict(blade)
            info["bucket_cross_origin_policies"] = (
                generate_bucket_cross_object_policies_dict(blade)
            )
        info["export_policies"] = generate_nfs_export_policies_dict(blade)
        if SMB_CLIENT_API_VERSION in api_versions:
            info["share_policies"] = generate_smb_client_policies_dict(blade)
        if FLEET_API_VERSION in api_versions:
            info["fleet"] = generate_fleet_dict(blade)
    if "drives" in subset or "all" in subset and DRIVES_API_VERSION in api_versions:
        info["drives"] = generate_drives_dict(blade)
    if "servers" in subset or "all" in subset and SERVERS_API_VERSION in api_versions:
        info["servers"] = generate_servers_dict(blade)
    module.exit_json(changed=False, purefb_info=info)


if __name__ == "__main__":
    main()
