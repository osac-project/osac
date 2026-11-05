#!/usr/bin/python
"""Manage one OSAC-owned NICo Instance and verify its Machine release.

The REST create operation is deliberately never retried by this module. A
caller must durably record its create intent before setting create_allowed;
after an uncertain response, inspect the Site's Core allocation before
allowing another POST. This module can adopt one REST-visible Instance with
the same BMI UID, but an empty REST list does not prove Core did not allocate.
"""

from __future__ import annotations

import json
import time
from ipaddress import IPv4Address, ip_address, ip_network
from urllib.error import HTTPError, URLError
from urllib.parse import quote, urlencode
from urllib.request import Request, urlopen

from ansible.module_utils.basic import AnsibleModule


DOCUMENTATION = r"""
---
module: bare_metal_instance
short_description: Manage an OSAC-owned NICo bare-metal Instance
description:
  - Creates or adopts exactly one NICo Instance for a BMI UID.
  - Deletes an Instance and verifies its original Machine is unassigned Ready.
  - A create request is never retried after an uncertain outcome.
options:
  state:
    choices: [present, absent]
    required: true
    type: str
  api_url:
    required: true
    type: str
  token_url:
    required: true
    type: str
  token_host:
    type: str
  allow_http:
    type: bool
    default: false
  tenant_org:
    required: true
    type: str
  provider_org:
    required: true
    type: str
  tenant_auth:
    required: true
    type: dict
  provider_auth:
    required: true
    type: dict
  tenant_id:
    required: true
    type: str
  bmi_uid:
    required: true
    type: str
  instance_id:
    type: str
  machine_id:
    type: str
  create_allowed:
    type: bool
    default: false
  name:
    type: str
  vpc_id:
    type: str
  instance_type_id:
    type: str
  target_machine_id:
    type: str
    description:
      - Administrator-selected Machine for targeted creation. Use instead of instance_type_id.
      - The NICo tenant account must permit targeted Instance creation at the VPC's Site.
  subnet_id:
    type: str
  vpc_prefix_id:
    type: str
  network_virtualization_type:
    choices: [ETHERNET_VIRTUALIZER, FNN, FLAT]
    default: ETHERNET_VIRTUALIZER
    type: str
  flat_ipv4_cidr:
    type: str
    description:
      - Expected OSAC Subnet CIDR for the one Flat network attachment.
      - The assigned NICo HostInband address must fall within this range.
  operating_system_id:
    type: str
  ipxe_script:
    type: str
  timeout:
    type: int
    default: 1200
  poll_interval:
    type: int
    default: 10
author:
  - OSAC contributors
"""


class NicoError(Exception):
    pass


class NicoHTTPError(NicoError):
    def __init__(self, status, message):
        super().__init__(f"NICo HTTP {status}: {message}")
        self.status = status


class NicoClient:
    def __init__(self, module):
        self.module = module
        self.params = module.params
        self.tokens = {}
        self.api_url = self.params["api_url"].rstrip("/")
        self.token_url = self.params["token_url"]
        if not self.params["allow_http"]:
            if not self.api_url.startswith("https://") or not self.token_url.startswith("https://"):
                raise NicoError("NICo API and token URLs must use HTTPS")

    @staticmethod
    def _decode(response):
        raw = response.read()
        if not raw:
            return None
        try:
            return json.loads(raw)
        except ValueError as exc:
            raise NicoError("NICo returned invalid JSON") from exc

    def _open(self, request):
        try:
            with urlopen(request, timeout=30) as response:
                return response.status, self._decode(response)
        except HTTPError as exc:
            try:
                body = self._decode(exc)
            except NicoError:
                body = None
            message = body.get("message", "request failed") if isinstance(body, dict) else "request failed"
            raise NicoHTTPError(exc.code, message) from exc
        except (URLError, TimeoutError) as exc:
            raise NicoError("NICo request outcome is uncertain; inspect the Site before retrying") from exc

    def _token(self, scope):
        cached = self.tokens.get(scope)
        if cached and cached[1] > time.monotonic() + 30:
            return cached[0]
        auth = self.params[f"{scope}_auth"]
        grant = auth.get("grant_type", "client_credentials")
        if grant not in ("client_credentials", "password"):
            raise NicoError("unsupported NICo OAuth grant type")
        data = {"grant_type": grant, "client_id": auth.get("client_id", ""),
                "client_secret": auth.get("client_secret", "")}
        if grant == "password":
            data.update(username=auth.get("username", ""), password=auth.get("password", ""))
        if not data["client_id"] or not data["client_secret"]:
            raise NicoError(f"{scope} NICo client credentials are missing")
        request = Request(self.token_url, data=urlencode(data).encode(), method="POST")
        request.add_header("Content-Type", "application/x-www-form-urlencoded")
        if self.params["token_host"]:
            request.add_header("Host", self.params["token_host"])
        _, response = self._open(request)
        if not isinstance(response, dict) or not response.get("access_token"):
            raise NicoError(f"{scope} token response has no access token")
        self.tokens[scope] = (response["access_token"], time.monotonic() + int(response.get("expires_in", 300)))
        return response["access_token"]

    def request(self, scope, method, resource, body=None):
        org = self.params[f"{scope}_org"]
        path = f"/v2/org/{quote(org, safe='')}/nico/{resource}"
        data = json.dumps(body).encode() if body is not None else None
        request = Request(self.api_url + path, data=data, method=method)
        request.add_header("Authorization", "Bearer " + self._token(scope))
        request.add_header("Accept", "application/json")
        if data is not None:
            request.add_header("Content-Type", "application/json")
        try:
            return self._open(request)
        except NicoHTTPError as exc:
            if exc.status == 401:
                self.tokens.pop(scope, None)
                request.remove_header("Authorization")
                request.add_header("Authorization", "Bearer " + self._token(scope))
                return self._open(request)
            raise


def instance_path(instance_id):
    return "instance/" + quote(instance_id, safe="")


def machine_path(machine_id):
    return "machine/" + quote(machine_id, safe="")


def get_or_none(client, scope, path):
    try:
        return client.request(scope, "GET", path)[1]
    except NicoHTTPError as exc:
        if exc.status == 404:
            return None
        raise


def machine_has_blocking_health_alert(machine):
    health = machine.get("health")
    if not isinstance(health, dict) or not isinstance(health.get("alerts"), list):
        raise NicoError("Ready Machine has no aggregate health alerts; retain BMI finalizer")
    for alert in health["alerts"]:
        if not isinstance(alert, dict):
            raise NicoError("Ready Machine has invalid aggregate health alerts; retain BMI finalizer")
        classifications = alert.get("classifications")
        if not isinstance(classifications, list) or not all(isinstance(item, str) for item in classifications):
            raise NicoError("Ready Machine has invalid health alert classifications; retain BMI finalizer")
        if "PreventAllocations" in classifications:
            return True
    return False


def assert_owned(instance, params, machine_id=None):
    if instance.get("tenantId") != params["tenant_id"]:
        raise NicoError("Instance tenant does not match configured NICo tenant")
    if (instance.get("labels") or {}).get("osacBmiUid") != params["bmi_uid"]:
        raise NicoError("Instance BMI UID ownership label does not match")
    if machine_id and instance.get("machineId") != machine_id:
        raise NicoError("Instance Machine ID differs from the saved BMI Machine ID")


def wait_for_instance(client, instance_id, timeout, interval):
    deadline = time.monotonic() + timeout
    while True:
        instance = get_or_none(client, "tenant", instance_path(instance_id))
        if instance is None:
            raise NicoError("Instance vanished while waiting for Ready")
        status = instance.get("status")
        if status in ("Ready", "BootCompleted"):
            return instance
        if status in ("Error", "Terminating", "Terminated"):
            raise NicoError(f"Instance {instance_id} reached {status} while waiting for Ready")
        if time.monotonic() >= deadline:
            raise NicoError(f"Instance {instance_id} did not reach Ready before timeout")
        time.sleep(interval)


def validate_network_configuration(params):
    mode = params["network_virtualization_type"]
    subnet_id, prefix_id = params["subnet_id"], params["vpc_prefix_id"]
    flat_cidr = params["flat_ipv4_cidr"]
    if mode == "FLAT":
        if subnet_id or prefix_id:
            raise NicoError("FLAT requires no NICo Subnet or VPC Prefix ID")
        if not flat_cidr:
            raise NicoError("FLAT requires the expected OSAC Subnet IPv4 CIDR")
        try:
            network = ip_network(flat_cidr, strict=True)
        except ValueError as exc:
            raise NicoError("FLAT expected IPv4 CIDR is invalid") from exc
        if network.version != 4:
            raise NicoError("FLAT expected CIDR must be IPv4")
    elif mode == "ETHERNET_VIRTUALIZER":
        if not subnet_id or prefix_id or flat_cidr:
            raise NicoError("ETHERNET_VIRTUALIZER requires only a NICo Subnet ID")
    elif mode == "FNN":
        if not prefix_id or subnet_id or flat_cidr:
            raise NicoError("FNN requires only a NICo VPC Prefix ID")
    else:
        raise NicoError("unsupported NICo VPC network virtualization type")


def assigned_ip(instance, params):
    if params["network_virtualization_type"] == "FLAT":
        if instance.get("autoNetwork") is not True:
            raise NicoError("Ready FLAT Instance does not report autoNetwork true")
        expected = ip_network(params["flat_ipv4_cidr"])
        matching = []
        for interface in instance.get("interfaces") or []:
            for value in interface.get("ipAddresses") or []:
                try:
                    address = ip_address(value)
                except ValueError as exc:
                    raise NicoError("Ready FLAT Instance reports an invalid IP address") from exc
                if isinstance(address, IPv4Address) and address in expected:
                    matching.append(value)
        if len(matching) != 1:
            raise NicoError("Ready FLAT Instance has no unique address in the bound OSAC Subnet CIDR")
        return matching[0]

    network_key = "subnetId" if params["network_virtualization_type"] == "ETHERNET_VIRTUALIZER" else "vpcPrefixId"
    network_id = params["subnet_id"] or params["vpc_prefix_id"]
    matching = [interface for interface in instance.get("interfaces") or []
                if interface.get(network_key) == network_id]
    if len(matching) != 1 or not matching[0].get("ipAddresses"):
        raise NicoError("Ready Instance has no unique configured interface with an assigned IP address")
    return matching[0]["ipAddresses"][0]


def ensure_present(client):
    params = client.params
    for key in ("name", "vpc_id"):
        if not params[key]:
            raise NicoError(f"{key} is required for Instance create")
    target_machine_id = params["target_machine_id"]
    if bool(params["instance_type_id"]) == bool(target_machine_id):
        raise NicoError("configure exactly one of instance_type_id or target_machine_id")
    validate_network_configuration(params)
    if not params["operating_system_id"] and not params["ipxe_script"]:
        raise NicoError("operating_system_id or ipxe_script is required")
    vpc = get_or_none(client, "tenant", "vpc/" + quote(params["vpc_id"], safe=""))
    if vpc is None or vpc.get("tenantId") != params["tenant_id"]:
        raise NicoError("configured NICo VPC is absent or belongs to another tenant")
    if vpc.get("networkVirtualizationType") != params["network_virtualization_type"]:
        raise NicoError("configured NICo VPC network type differs from BMI backend mode")

    if params["instance_id"]:
        instance = get_or_none(client, "tenant", instance_path(params["instance_id"]))
        if instance is None:
            raise NicoError("saved Instance ID is absent; refusing a second create")
        assert_owned(instance, params, params["machine_id"])
        changed = False
    else:
        # Query the stable name so a large tenant's paginated Instance list
        # cannot hide an existing BMI-owned Instance on a later page.
        _, listed = client.request("tenant", "GET", "instance?name=" + quote(params["name"], safe=""))
        matches = [item for item in listed if (item.get("labels") or {}).get("osacBmiUid") == params["bmi_uid"]]
        if len(matches) > 1:
            raise NicoError("multiple NICo Instances have this BMI UID label")
        if matches:
            instance = matches[0]
            assert_owned(instance, params)
            changed = False
        else:
            if not params["create_allowed"]:
                raise NicoError("Instance not found; create intent must be durably authorized before POST")
            if target_machine_id:
                machine = get_or_none(client, "provider", machine_path(target_machine_id))
                if machine is None:
                    raise NicoError("target Machine is absent from provider inventory")
                if not vpc.get("siteId") or machine.get("siteId") != vpc["siteId"]:
                    raise NicoError("target Machine and configured VPC are not at the same Site")
                if machine.get("status") != "Ready" or machine.get("instanceId") is not None:
                    raise NicoError("target Machine is not unassigned Ready")
            body = {"name": params["name"], "tenantId": params["tenant_id"],
                    "vpcId": params["vpc_id"],
                    "labels": {"osacBmiUid": params["bmi_uid"]},
                    "phoneHomeEnabled": False}
            body["machineId" if target_machine_id else "instanceTypeId"] = target_machine_id or params["instance_type_id"]
            if params["network_virtualization_type"] == "FLAT":
                body["autoNetwork"] = True
            else:
                interface = {"isPhysical": True}
                interface["subnetId" if params["subnet_id"] else "vpcPrefixId"] = params["subnet_id"] or params["vpc_prefix_id"]
                body["interfaces"] = [interface]
            if params["operating_system_id"]:
                body["operatingSystemId"] = params["operating_system_id"]
            if params["ipxe_script"]:
                body["ipxeScript"] = params["ipxe_script"]
            try:
                _, instance = client.request("tenant", "POST", "instance", body)
            except NicoError as exc:
                raise NicoError("Instance create outcome requires Site verification before another POST: " + str(exc)) from exc
            changed = True
    instance_id = instance.get("id")
    if not instance_id:
        raise NicoError("NICo Instance response has no ID; verify Site allocation before retrying")
    instance = wait_for_instance(client, instance_id, params["timeout"], params["poll_interval"])
    assert_owned(instance, params, params["machine_id"])
    if instance.get("vpcId") != params["vpc_id"]:
        raise NicoError("Ready Instance VPC differs from the configured BMI backend")
    if target_machine_id:
        if instance.get("machineId") != target_machine_id:
            raise NicoError("Ready Instance Machine differs from the configured target Machine")
    elif instance.get("instanceTypeId") != params["instance_type_id"]:
        raise NicoError("Ready Instance Type differs from the configured BMI backend")
    address = assigned_ip(instance, params)
    machine_id = instance.get("machineId")
    if not machine_id:
        raise NicoError("Ready NICo Instance has no Machine ID")
    return {"changed": changed, "instance_id": instance_id, "machine_id": machine_id,
            "ip_address": address,
            "status": instance["status"]}


def ensure_absent(client):
    params = client.params
    instance_id, machine_id = params["instance_id"], params["machine_id"]
    if not instance_id or not machine_id:
        raise NicoError("saved Instance and Machine IDs are both required for safe delete")
    instance = get_or_none(client, "tenant", instance_path(instance_id))
    if instance is not None:
        assert_owned(instance, params, machine_id)
        if instance.get("status") not in ("Terminating", "Terminated"):
            client.request("tenant", "DELETE", instance_path(instance_id))
    changed = instance is not None
    deadline = time.monotonic() + params["timeout"]
    while True:
        instance = get_or_none(client, "tenant", instance_path(instance_id))
        if instance is None:
            machine = get_or_none(client, "provider", machine_path(machine_id))
            if machine is None:
                raise NicoError("saved Machine is absent from provider inventory")
            if machine.get("status") == "Ready" and machine.get("instanceId") is None:
                # Machine GET includes aggregate health. The separate health-report
                # endpoint is diagnostic and may be unavailable during cleanup.
                if not machine_has_blocking_health_alert(machine):
                    return {"changed": changed, "instance_id": instance_id, "machine_id": machine_id,
                            "status": "Released"}
        if time.monotonic() >= deadline:
            raise NicoError(f"NICo cleanup incomplete for Instance {instance_id} and Machine {machine_id}; retain BMI finalizer")
        time.sleep(params["poll_interval"])


def main():
    auth_options = dict(type="dict", required=True, no_log=True)
    module = AnsibleModule(argument_spec=dict(
        state=dict(type="str", required=True, choices=["present", "absent"]),
        api_url=dict(type="str", required=True), token_url=dict(type="str", required=True),
        token_host=dict(type="str", default=""), allow_http=dict(type="bool", default=False),
        tenant_org=dict(type="str", required=True), provider_org=dict(type="str", required=True),
        tenant_auth=auth_options, provider_auth=auth_options,
        tenant_id=dict(type="str", required=True), bmi_uid=dict(type="str", required=True),
        instance_id=dict(type="str", default=""), machine_id=dict(type="str", default=""),
        create_allowed=dict(type="bool", default=False), name=dict(type="str", default=""),
        vpc_id=dict(type="str", default=""), instance_type_id=dict(type="str", default=""),
        target_machine_id=dict(type="str", default=""),
        subnet_id=dict(type="str", default=""), vpc_prefix_id=dict(type="str", default=""),
        network_virtualization_type=dict(type="str", default="ETHERNET_VIRTUALIZER",
                                         choices=["ETHERNET_VIRTUALIZER", "FNN", "FLAT"]),
        flat_ipv4_cidr=dict(type="str", default=""),
        operating_system_id=dict(type="str", default=""), ipxe_script=dict(type="str", default=""),
        timeout=dict(type="int", default=1200), poll_interval=dict(type="int", default=10),
    ), supports_check_mode=False)
    try:
        if module.params["timeout"] <= 0 or module.params["poll_interval"] <= 0:
            raise NicoError("timeout and poll_interval must be positive")
        client = NicoClient(module)
        result = ensure_present(client) if module.params["state"] == "present" else ensure_absent(client)
        module.exit_json(**result)
    except NicoError as exc:
        module.fail_json(msg=str(exc))


if __name__ == "__main__":
    main()
