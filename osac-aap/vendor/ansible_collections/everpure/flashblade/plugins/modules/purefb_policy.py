#!/usr/bin/python
# -*- coding: utf-8 -*-

# (c) 2020, Simon Dodsley (simon@everpuredata.com)
# GNU General Public License v3.0+ (see COPYING or https://www.gnu.org/licenses/gpl-3.0.txt)

from __future__ import absolute_import, division, print_function

__metaclass__ = type

ANSIBLE_METADATA = {
    "metadata_version": "1.1",
    "status": ["preview"],
    "supported_by": "community",
}

DOCUMENTATION = r"""
---
module: purefb_policy
version_added: '1.0.0'
short_description: Manage FlashBlade policies
description:
- Manage policies for filesystem, file replica links and object store access.
- To update an existing snapshot policy rule, you must first delete the
  original rule and then add the new rule to replace it. Purity's best-fit
  will try to ensure that any required snapshots deleted on the deletion of
  the first rule will be recovered as long replacement rule is added before
  the snapshot eradication period is exceeded (usuually 24 hours).
- Network access policies cannot be created or deleted on FlashBlade.
  I(policy_type=network) manages the rules of an existing network access
  policy (normally C(default-network-access-policy)).
- Rule fields that are not set in a task keep their current value when an
  existing rule is updated; the documented defaults are only applied when a
  new rule is created.
author:
- Everpure Ansible Team (@sdodsley) <pure-ansible-team@everpuredata.com>
options:
  state:
    description:
    - Create or delete policy.
    - I(copy) copies a single rule for Object Store Access Policies
      (I(rule) and I(target) in C(account/policy) form are required).
    - For snapshot, NFS, SMB and WORM policies I(copy) creates I(target) as a
      new policy with the same settings and rules as I(name).
    - Network access policies cannot be copied.
    default: present
    type: str
    choices: [ absent, present, copy ]
  target:
    description:
    - Name of policy to copy rule to, or name of the new policy to create
      when copying a whole policy.
    type: str
    version_added: "1.9.0"
  target_rule:
    description:
    - Name of the rule to copy the exisitng rule to.
    - If not defined the existing rule name is used.
    type: str
    version_added: "1.9.0"
  policy_type:
    description:
    - Type of policy
    default: snapshot
    type: str
    choices: [ snapshot, access, nfs, smb_share, smb_client, network, worm ]
    version_added: "1.9.0"
  account:
    description:
    - Name of Object Store account policy applies to.
    - B(Special Case) I(pure policy) is used for the system-wide S3 policies
    type: str
    version_added: "1.9.0"
  rule:
    description:
    - Name of the rule for the Object Store Access Policy
    - Rules in system wide policies cannot be deleted or modified
    type: str
    version_added: "1.9.0"
  effect:
    description:
    - Allow S3 requests that match all of the I(actions) item selected.
      Rules are additive.
    - Also the effect of a network access policy rule.
    - Defaults to I(allow) when a new rule is created.
    type: str
    choices: [ allow, deny ]
    version_added: "1.9.0"
  actions:
    description:
    - List of permissions to grant.
    - System-wide policy rules cannot be deleted or modified
    type: list
    elements: str
    choices:
      - s3:*
      - s3:AbortMultipartUpload
      - s3:BypassGovernanceRetention
      - s3:CreateBucket
      - s3:DeleteBucket
      - s3:DeleteObject
      - s3:DeleteObjectVersion
      - s3:ExtendSafemodeRetentionPeriod
      - s3:GetBucketAcl
      - s3:GetBucketLocation
      - s3:GetBucketVersioning
      - s3:GetLifecycleConfiguration
      - s3:GetObject
      - s3:GetObjectAcl
      - s3:GetObjectLegalHold
      - s3:GetObjectLockConfiguration
      - s3:GetObjectRetention
      - s3:GetObjectTagging
      - s3:GetObjectVersion
      - s3:GetObjectVersionTagging
      - s3:ListAllMyBuckets
      - s3:ListBucket
      - s3:ListBucketMultipartUploads
      - s3:ListBucketVersions
      - s3:ListMultipartUploadParts
      - s3:PutBucketVersioning
      - s3:PutLifecycleConfiguration
      - s3:PutObject
      - s3:PutObjectLegalHold
      - s3:PutObjectLockConfiguration
      - s3:PutObjectRetention
      - s3:ResolveSafemodeConflicts
    version_added: "1.9.0"
  object_resources:
    description:
    - List of bucket names and object paths, with a wildcard (*) to
      specify objects in a bucket; e.g., bucket1, bucket1/*, bucket2,
      bucket2/*.
    - System-wide policy rules cannot be deleted or modified
    type: list
    elements: str
    version_added: "1.9.0"
  source_ips:
    description:
    - List of IPs and subnets from which this rule should allow requests;
      e.g., 10.20.30.40, 10.20.30.0/24, 2001:DB8:1234:5678::/64.
    - System-wide policy rules cannot be deleted or modified
    type: list
    elements: str
    version_added: "1.9.0"
  s3_prefixes:
    description:
    - List of 'folders' (object key prefixes) for which object listings
      may be requested.
    - System-wide policy rules cannot be deleted or modified
    type: list
    elements: str
    version_added: "1.9.0"
  s3_delimiters:
    description:
    - List of delimiter characters allowed in object list requests.
    - Grants permissions to list 'folder names' (prefixes ending in a
      delimiter) instead of object keys.
    - System-wide policy rules cannot be deleted or modified
    type: list
    elements: str
    version_added: "1.9.0"
  ignore_enforcement:
    description:
    - Certain combinations of actions and other rule elements are inherently
      ignored if specified together in a rule.
    - If set to true, operations which attempt to set these combinations will fail.
    - If set to false, such operations will instead be allowed.
    type: bool
    default: true
    version_added: "1.9.0"
  user:
    description:
    - User in the I(account) that the policy is granted to.
    type: str
    version_added: "1.9.0"
  force_delete:
    description:
    - Force the deletion of a Object Store Access Policy is this
      has attached users.
    - WARNING This can have undesired side-effects.
    - System-wide policies cannot be deleted
    type: bool
    default: false
    version_added: "1.9.0"
  name:
    description:
    - Name of the policy
    type: str
  enabled:
    description:
    - State of policy
    type: bool
    default: true
  every:
    description:
    - Interval between snapshots in seconds
    - Range available 300 - 31536000 (equates to 5m to 365d)
    type: int
  keep_for:
    description:
    - How long to keep snapshots for
    - Range available 300 - 31536000 (equates to 5m to 365d)
    - Must not be set less than I(every)
    type: int
  at:
    description:
    - Provide a time in 12-hour AM/PM format, eg. 11AM
    type: str
  timezone:
    description:
    - Time Zone used for the I(at) parameter
    - If not provided, the module will attempt to get the current local timezone from the server
    type: str
  filesystem:
    description:
    - List of filesystems to add to a policy on creation
    - To amend policy members use the I(everpure.flashblade.purefb_fs) module
    type: list
    elements: str
  replica_link:
    description:
    - List of filesystem replica links to add to a policy on creation
    - To amend policy members use the I(everpure.flashblade.purefb_fs_replica) module
    type: list
    elements: str
  access:
    description:
    - Specifies access control for the export policy rule
    - Defaults to I(root-squash) when a new rule is created.
    type: str
    choices: [ root-squash, all-squash, no-squash ]
    version_added: "1.9.0"
  anonuid:
    description:
    - Any user whose UID is affected by an I(access) of `root_squash` or `all_squash`
      will have their UID mapped to anonuid.
      The default is null, which means 65534.
    type: int
    version_added: "1.9.0"
  anongid:
    description:
    - Any user whose GID is affected by an I(access) of `root_squash` or `all_squash`
      will have their GID mapped to anongid.
      The default anongid is null, which means 65534.
    type: int
    version_added: "1.9.0"
  atime:
    description:
    - After a read operation has occurred, the inode access time is updated only if any
      of the following conditions is true; the previous access time is less than the
      inode modify time, the previous access time is less than the inode change time,
      or the previous access time is more than 24 hours ago.
    - If set to false, disables the update of inode access times after read operations.
    - Defaults to true when a new rule is created.
    type: bool
    version_added: "1.9.0"
  client:
    description:
    - Specifies the clients that will be permitted to access the export.
    - Accepted notation is a single IP address, subnet in CIDR notation, netgroup, or
      anonymous (*).
    type: str
    version_added: "1.9.0"
  fileid_32bit:
    description:
    - Whether the file id is 32 bits or not.
    - Defaults to false when a new rule is created.
    type: bool
    version_added: "1.9.0"
  permission:
    description:
    - Specifies which read-write client access permissions are allowed for the export.
    - Defaults to I(ro) when a new rule is created.
    type: str
    choices: [ rw, ro ]
    version_added: "1.9.0"
  secure:
    description:
    - If true, this prevents NFS access to client connections coming from non-reserved ports.
    - If false, allows NFS access to client connections coming from non-reserved ports.
    - Applies to NFSv3, NFSv4.1, and auxiliary protocols MOUNT and NLM.
    - Defaults to false when a new rule is created.
    type: bool
    version_added: "1.9.0"
  security:
    description:
    - The security flavors to use for accessing files on this mount point.
    - If the server does not support the requested flavor, the mount operation fails.
    - I(sys) trusts the client to specify users identity.
    - I(krb) provides cryptographic proof of a users identity in each RPC request.
    - I(krb5i) adds integrity checking to krb5, to ensure the data has not been tampered with.
    - I(krb5p) adds integrity checking and encryption to krb5.
    - Defaults to C([sys]) when a new rule is created.
    type: list
    elements: str
    choices: [ sys, krb5, krb5i, krb5p ]
    version_added: "1.9.0"
  before_rule:
    description:
    - The index of the client rule to insert or move a client rule before.
    - Not supported for SMB share policies, whose rules are unordered.
    type: int
    version_added: "1.9.0"
  rename:
    description:
    - New name for policy
    - Applies to snapshot, NFS, SMB and network access policies.
    - WORM data policies cannot be renamed.
    - Purity//FB up to at least 4.6.4 (REST 2.21) ignores a new name for
      snapshot policies; the module fails instead of reporting a rename
      the array did not perform.
    type: str
    version_added: "1.10.0"
  destroy_snapshots:
    description:
    - This parameter must be set to true in order to modify a policy such that local or remote snapshots would be destroyed.
    type: bool
    version_added: '1.11.0'
    default: false
  principal:
    description:
     - The user or group who is the subject of this rule, and their domain
    type: str
    version_added: '1.12.0'
  change:
    description:
     - The state of the SMB share principals Change access permission.
     - Setting to "" will clear the current setting
     - Share permissions form the ladder I(read), I(change), I(full_control)
       with a single effect per rule. Setting a level applies its effect to
       the levels below it; a level set to the opposite effect above the
       granted ones, or to "", is not granted, as the array stores it.
    type: str
    choices: [ allow, deny, "" ]
    version_added: '1.12.0'
  read:
    description:
     - The state of the SMB share principals Read access permission.
     - Setting to "" will clear the current setting
     - See I(change) for how the share permission levels interact.
    type: str
    choices: [ allow, deny, "" ]
    version_added: '1.12.0'
  full_control:
    description:
     - The state of the SMB share principals Full Control access permission.
     - Setting to "" will clear the current setting
     - See I(change) for how the share permission levels interact.
    type: str
    choices: [ allow, deny, "" ]
    version_added: '1.12.0'
  smb_encryption:
    description:
     - The status of SMB encryption in a client policy rule
     - Defaults to I(optional) when a new rule is created.
    type: str
    choices: [ disabled, optional, required ]
    version_added: '1.12.0'
  desc:
    description:
    - A description of an object store policy,
      optionally specified when the policy is created.
    - Cannot be modified for an existing policy.
    type: str
    default: ""
    version_added: '1.14.0'
  interfaces:
    description:
    - Specifies which product interfaces the network access policy rule
      applies to, whether it is permitting or denying access.
    - Required for I(policy_type=network); only used when a rule is created
      or updated.
    type: list
    elements: str
    choices: [ "management-ssh", "management-rest-api", "management-web-ui", "snmp", "local-network-superuser-password-access" ]
    version_added: '1.17.0'
  max_retention:
    description:
      - The maximum retention period of the WORM file system.
      - Between 1 second and 100 years.
      - Cannot be less than the I(min_retention).
      - Valid values are weeks (w), days(d), hours(h), minutes(m) and seconds(s).
    type: str
    version_added: '1.19.0'
  min_retention:
    description:
      - The minimum retention period of the WORM file system.
      - Between 1 second and 100 years.
      - Cannot be greater than the I(max_retention).
      - Valid values are weeks (w), days(d), hours(h), minutes(m) and seconds(s).
    type: str
    version_added: '1.19.0'
  default_retention:
    description:
      - The retention period used for committing files to WORM status.
        Will be applied if no access time is provided, or the access time is less than the current server time.
        Between I(min_retention) and I(max_retention) periods.
      - Valid values are weeks (w), days(d), hours(h), minutes(m) and seconds(s).
    type: str
    version_added: '1.19.0'
  retention_lock:
    description:
      - State of policy attributes after creation.
      - If set to I(locked) then values of the policy attributes are not allowed to change.
      - If set to I(locked) then values of the policy attributes can be changed.
      - Changing from I(unlocked) to I(locked) is allowed, but to change from I(locked) to I(unlocked)
        will require support from Everpure Technical Services.
    type: str
    choices: [ locked, unlocked ]
    version_added: '1.19.0'
  context:
    description:
    - Name of fleet member on which to perform the operation.
    - This requires the array receiving the request is a member of a fleet
      and the context name to be a member of the same fleet.
    type: str
    default: ""
    version_added: "1.22.0"
extends_documentation_fragment:
- everpure.flashblade.everpure.fb
"""

EXAMPLES = r"""
- name: Create a simple snapshot policy with no rules
  everpure.flashblade.purefb_policy:
    name: test_policy
    policy_type: snapshot
    fb_url: 10.10.10.2
    api_token: T-9f276a18-50ab-446e-8a0c-666a3529a1b6
- name: Create a snapshot policy and connect to existing filesystems and filesystem replica links
  everpure.flashblade.purefb_policy:
    name: test_policy_with_members
    policy_type: snapshot
    filesystem:
      - fs1
      - fs2
    replica_link:
      - rl1
      - rl2
    fb_url: 10.10.10.2
    api_token: T-9f276a18-50ab-446e-8a0c-666a3529a1b6
- name: Create a snapshot policy with rules
  everpure.flashblade.purefb_policy:
    name: test_policy2
    policy_type: snapshot
    at: 11AM
    keep_for: 86400
    every: 86400
    timezone: Asia/Shanghai
    fb_url: 10.10.10.2
    api_token: T-9f276a18-50ab-446e-8a0c-666a3529a1b6
- name: Delete filesystem foo from snapshot policy
  everpure.flashblade.purefb_policy:
    name: test_policy
    policy_type: snapshot
    filesystem: foo
    state: absent
    fb_url: 10.10.10.2
    api_token: T-9f276a18-50ab-446e-8a0c-666a3529a1b6
- name: Delete a snapshot policy
  everpure.flashblade.purefb_policy:
    name: test_policy
    policy_type: snapshot
    state: absent
    fb_url: 10.10.10.2
    api_token: T-9f276a18-50ab-446e-8a0c-666a3529a1b6
- name: Create an empty object store access policy
  everpure.flashblade.purefb_policy:
    name: test_os_policy
    account: test
    policy_type: access
    fb_url: 10.10.10.2
    api_token: T-9f276a18-50ab-446e-8a0c-666a3529a1b6
- name: Create an empty object store access policy and assign user
  everpure.flashblade.purefb_policy:
    name: test_os_policy
    account: test
    policy_type: access
    user: fred
    fb_url: 10.10.10.2
    api_token: T-9f276a18-50ab-446e-8a0c-666a3529a1b6
- name: Create a object store access policy with simple rule
  everpure.flashblade.purefb_policy:
    name: test_os_policy_rule
    policy_type: access
    account: test
    rule: rule1
    actions: "s3:*"
    object_resources: "*"
    fb_url: 10.10.10.2
    api_token: T-9f276a18-50ab-446e-8a0c-666a3529a1b6
- name: Create an empty SMB client policy
  everpure.flashblade.purefb_policy:
    name: test_smb_client
    policy_type: smb_client
    fb_url: 10.10.10.2
    api_token: T-9f276a18-50ab-446e-8a0c-666a3529a1b6
- name: Create an SMB client policy with a client rule
  everpure.flashblade.purefb_policy:
    name: test_smb_client
    policy_type: smb_client
    client: "10.0.1.0/24"
    permission: rw
    fb_url: 10.10.10.2
    api_token: T-9f276a18-50ab-446e-8a0c-666a3529a1b6
- name: Create an empty NFS export policy
  everpure.flashblade.purefb_policy:
    name: test_nfs_export
    policy_type: nfs
    fb_url: 10.10.10.2
    api_token: T-9f276a18-50ab-446e-8a0c-666a3529a1b6
- name: Create an NFS export policy with a client rule
  everpure.flashblade.purefb_policy:
    name: test_nfs_export
    policy_type: nfs
    atime: true
    client: "10.0.1.0/24"
    secure: true
    security: [sys, krb5]
    permission: rw
    fb_url: 10.10.10.2
    api_token: T-9f276a18-50ab-446e-8a0c-666a3529a1b6
- name: Create a new rule for an existing NFS export policy
  everpure.flashblade.purefb_policy:
    name: test_nfs_export
    policy_type: nfs
    atime: true
    client: "10.0.2.0/24"
    security: sys
    permission: ro
    fb_url: 10.10.10.2
    api_token: T-9f276a18-50ab-446e-8a0c-666a3529a1b6
- name: Delete a client rule from an NFS export policy
  everpure.flashblade.purefb_policy:
    name: test_nfs_export
    client: "10.0.1.0/24"
    policy_type: nfs
    state: absent
    fb_url: 10.10.10.2
    api_token: T-9f276a18-50ab-446e-8a0c-666a3529a1b6
- name: Delete an NFS export policy and all associated rules
  everpure.flashblade.purefb_policy:
    name: test_nfs_export
    state: absent
    policy_type: nfs
    fb_url: 10.10.10.2
    api_token: T-9f276a18-50ab-446e-8a0c-666a3529a1b6
- name: Delete a rule from an object store access policy
  everpure.flashblade.purefb_policy:
    name: test_os_policy_rule
    account: test
    policy_type: access
    rule: rule1
    state: absent
    fb_url: 10.10.10.2
    api_token: T-9f276a18-50ab-446e-8a0c-666a3529a1b6
- name: Delete a user from an object store access policy
  everpure.flashblade.purefb_policy:
    name: test_os_policy_rule
    account: test
    user: fred
    policy_type: access
    state: absent
    fb_url: 10.10.10.2
    api_token: T-9f276a18-50ab-446e-8a0c-666a3529a1b6
- name: Delete an object store access policy with attached users (USE WITH CAUTION)
  everpure.flashblade.purefb_policy:
    name: test_os_policy_rule
    account: test
    policy_type: access
    force_delete: true
    state: absent
    fb_url: 10.10.10.2
    api_token: T-9f276a18-50ab-446e-8a0c-666a3529a1b6
- name: Delete an object store access policy with no attached users
  everpure.flashblade.purefb_policy:
    name: test_os_policy_rule
    account: test
    policy_type: access
    state: absent
    fb_url: 10.10.10.2
    api_token: T-9f276a18-50ab-446e-8a0c-666a3529a1b6
- name: Copy an object store access policy rule to another exisitng policy
  everpure.flashblade.purefb_policy:
    name: test_os_policy_rule
    policy_type: access
    account: test
    target: "account2/anotherpolicy"
    target_rule: new_rule1
    state: copy
    fb_url: 10.10.10.2
    api_token: T-9f276a18-50ab-446e-8a0c-666a3529a1b6
- name: Rename an NFS Export Policy
  everpure.flashblade.purefb_policy:
    name: old_name
    policy_type: nfs
    rename: new_name
    fb_url: 10.10.10.2
    api_token: T-9f276a18-50ab-446e-8a0c-666a3529a1b6
- name: Copy an NFS Export Policy, including its rules, to a new policy
  everpure.flashblade.purefb_policy:
    name: test_nfs_export
    policy_type: nfs
    target: test_nfs_export_copy
    state: copy
    fb_url: 10.10.10.2
    api_token: T-9f276a18-50ab-446e-8a0c-666a3529a1b6
- name: Add a rule to the default network access policy
  everpure.flashblade.purefb_policy:
    name: default-network-access-policy
    policy_type: network
    client: 10.0.1.0/24
    effect: deny
    interfaces:
      - management-ssh
    fb_url: 10.10.10.2
    api_token: T-9f276a18-50ab-446e-8a0c-666a3529a1b6
- name: Create a WORM Data Policy
  everpure.flashblade.purefb_policy:
    name: worm1
    policy_type: worm
    default_retention: 5d
    min_rentetion: 20h
    max_retention: 1y
    fb_url: 10.10.10.2
    api_token: T-9f276a18-50ab-446e-8a0c-666a3529a1b6
"""

RETURN = r"""
"""

HAS_PYPURECLIENT = True
try:
    from pypureclient.flashblade import (
        PolicyRuleObjectAccessCondition,
        PolicyRuleObjectAccessPost,
        NfsExportPolicy,
        NfsExportPolicyRule,
        Policy,
        SnapshotPolicyPatch,
        PolicyRule,
        SmbSharePolicyRule,
        SmbSharePolicy,
        SmbClientPolicyRule,
        SmbClientPolicy,
        ObjectStoreAccessPolicyPost,
        NetworkAccessPolicy,
        NetworkAccessPolicyRule,
        WormDataPolicy,
    )
except ImportError:
    HAS_PYPURECLIENT = False

HAS_PYTZ = True
try:
    import pytz
except ImportError:
    HAS_PYTZ = False

import os
import re
import platform

from ansible.module_utils.common.process import get_bin_path
from ansible.module_utils.facts.utils import get_file_content
from ansible.module_utils.basic import AnsibleModule
from ansible_collections.everpure.flashblade.plugins.module_utils.purefb import (
    get_system,
    purefb_argument_spec,
)
from ansible_collections.everpure.flashblade.plugins.module_utils.version import (
    LooseVersion,
)
from ansible_collections.everpure.flashblade.plugins.module_utils.common import (
    _findstr,
    get_error_message,
    get_rest_api_version,
)
from ansible_collections.everpure.flashblade.plugins.module_utils.time_utils import (
    time_to_milliseconds,
)

NFS_POLICY_API_VERSION = "2.3"
NFS_RENAME_API_VERSION = "2.4"
SMB_POLICY_API_VERSION = "2.10"
SMB_ENCRYPT_API_VERSION = "2.11"
NET_POLICY_API_VERSION = "2.13"
WORM_POLICY_API_VERSION = "2.15"
CONTEXT_API_VERSION = "2.17"
# From REST 2.22 the NFS export rule anonuid/anongid are integers on the wire;
# older arrays declare and accept them only as strings.
NFS_ANON_INT_API_VERSION = "2.22"


def _get_local_tz(module, timezone="UTC"):
    """
    We will attempt to get the local timezone of the server running the module and use that.
    If we can't get the timezone then we will set the default to be UTC

    Linnux has been tested and other opersting systems should be OK.
    Failures cause assumption of UTC

    Windows is not supported and will assume UTC
    """
    if platform.system() == "Linux":
        timedatectl = get_bin_path("timedatectl")
        if timedatectl is not None:
            rcode, stdout, stderr = module.run_command(timedatectl)
            if rcode == 0 and stdout:
                line = _findstr(stdout, "Time zone")
                full_tz = line.split(":", 1)[1].rstrip()
                timezone = full_tz.split()[0]
                return timezone
            else:
                module.warn("Incorrect timedatectl output. Timezone will be set to UTC")
        else:
            if os.path.exists("/etc/timezone"):
                timezone = get_file_content("/etc/timezone")
            else:
                module.warn("Could not find /etc/timezone. Assuming UTC")

    elif platform.system() == "SunOS":
        if os.path.exists("/etc/default/init"):
            for line in get_file_content("/etc/default/init", "").splitlines():
                if line.startswith("TZ="):
                    timezone = line.split("=", 1)[1]
                    return timezone
        else:
            module.warn("Could not find /etc/default/init. Assuming UTC")

    elif re.match("^Darwin", platform.platform()):
        systemsetup = get_bin_path("systemsetup")
        if systemsetup is not None:
            rcode, stdout, stderr = module.execute(systemsetup, "-gettimezone")
            if rcode == 0 and stdout:
                timezone = stdout.split(":", 1)[1].lstrip()
            else:
                module.warn("Could not run systemsetup. Assuming UTC")
        else:
            module.warn("Could not find systemsetup. Assuming UTC")

    elif re.match("^(Free|Net|Open)BSD", platform.platform()):
        if os.path.exists("/etc/timezone"):
            timezone = get_file_content("/etc/timezone")
        else:
            module.warn("Could not find /etc/timezone. Assuming UTC")

    elif platform.system() == "AIX":
        aix_oslevel = int(platform.version() + platform.release())
        if aix_oslevel >= 61:
            if os.path.exists("/etc/environment"):
                for line in get_file_content("/etc/environment", "").splitlines():
                    if line.startswith("TZ="):
                        timezone = line.split("=", 1)[1]
                        return timezone
            else:
                module.warn("Could not find /etc/environment. Assuming UTC")
        else:
            module.warn(
                "Cannot determine timezone when AIX os level < 61. Assuming UTC"
            )

    else:
        module.warn("Could not find /etc/timezone. Assuming UTC")

    return timezone


def _context_kwargs(module, versions):
    """Return the ``context_names`` keyword for fleet-aware SDK calls.

    An empty dict is returned when the array is below the context API version
    or no context is set, so callers can always splat the result.
    """
    if (
        LooseVersion(CONTEXT_API_VERSION) <= LooseVersion(versions)
        and module.params["context"]
    ):
        return {"context_names": [module.params["context"]]}
    return {}


def _default(value, default):
    """Return I(default) when the task did not set I(value)."""
    return default if value is None else value


def _blank_to_none(value):
    """Treat "" (used by the task to clear a setting) the same as unset."""
    return None if value == "" else value


def _sorted_or_none(values):
    """Sorted copy of a list for order-insensitive comparison; [] is unset."""
    return sorted(values) if values else None


# SMB share permissions, lowest level first.
_SHARE_LEVELS = ("read", "change", "full_control")


def _share_rule_perms(current, requested):
    """Return the permissions the array stores once I(requested) is applied.

    Purity//FB keeps an SMB share rule as one access control entry: the
    permissions form the ladder read < change < full_control, every granted
    level carries the single effect of the rule and the levels above the
    highest granted one are reported as None. Setting a level grants (or
    denies) it together with every level below it. A level set to the
    opposite effect above the granted ones (``full_control=deny`` next to
    ``read``/``change`` ``allow``), or set to "", is simply not granted, like
    everything above it. Levels the task leaves unset keep their current
    value.
    """
    merged = {}
    for level in _SHARE_LEVELS:
        value = requested.get(level)
        if value is None:
            value = current.get(level)
        merged[level] = _blank_to_none(value)
    # The task's own values decide the effect; unset levels only inherit.
    effect = None
    explicit_top = -1
    for index, level in enumerate(_SHARE_LEVELS):
        value = requested.get(level)
        if value is None:
            continue
        if effect is None and value in ("allow", "deny"):
            effect = value
        if value != effect:
            break
        explicit_top = index
    if effect is None:
        for level in _SHARE_LEVELS:
            if merged[level] in ("allow", "deny"):
                effect = merged[level]
                break
    result = {}
    granted = effect is not None
    for index, level in enumerate(_SHARE_LEVELS):
        value = effect if index <= explicit_top else merged[level]
        granted = granted and value == effect
        result[level] = effect if granted else None
    return result


def _share_rule_desired(module, live_perms):
    """Permissions the task asks for, applied over I(live_perms)."""
    perms = _share_rule_perms(
        live_perms, {level: module.params[level] for level in _SHARE_LEVELS}
    )
    if not any(perms.values()):
        module.fail_json(
            msg="SMB share rule for principal {0} must allow or deny at least "
            "read; use state=absent to remove the rule".format(
                module.params["principal"]
            )
        )
    return perms


def _share_rule_top(perms):
    """Return (effect, index of the highest granted level) of a rule."""
    top = max(
        index for index, level in enumerate(_SHARE_LEVELS) if perms[level] is not None
    )
    return perms["read"], top


def _share_rule_patch(current, desired):
    """Return the one-field PATCH body that turns I(current) into I(desired).

    The array grants (or denies) a level together with every level below it
    and drops the levels above it when the effect changes, so one field is
    enough: the new top level with its effect, or "" on the level above the
    new top when the rule keeps its effect and only loses levels.
    """
    current_effect, current_top = _share_rule_top(current)
    new_effect, new_top = _share_rule_top(desired)
    if new_effect != current_effect or new_top > current_top:
        return {_SHARE_LEVELS[new_top]: new_effect}
    return {_SHARE_LEVELS[new_top + 1]: ""}


def _anon_id(value):
    """Normalise anonuid/anongid for comparison.

    The array reports the ids as integers, but a PATCH body carries them as
    strings (see _nfs_rule), so compare them as int. None and "" both mean
    "not set".
    """
    if value is None or value == "":
        return None
    return int(value)


def _nfs_rule(versions, patch=False, **fields):
    """Build the request body of an NFS export policy rule.

    The array accepts anonuid/anongid as integers on POST and reports them as
    integers, but rejects integers on PATCH ("invalid value for anonuid") and
    takes only strings there. This holds on every REST version tested (2.21
    and 2.24), so a PATCH body is always sent as a plain dict, which the SDK
    passes through unchanged, with the ids rendered as strings.

    A POST body uses the SDK's ``NfsExportPolicyRule`` from REST 2.22, where
    the model declares the ids as integers; below that the model still
    declares strings and rejects integers, so a dict with string ids is sent
    instead. Fields set to None are left out either way.
    """
    fields = {key: value for key, value in fields.items() if value is not None}
    if not patch and LooseVersion(NFS_ANON_INT_API_VERSION) <= LooseVersion(versions):
        return NfsExportPolicyRule(**fields)
    for key in ("anonuid", "anongid"):
        if key in fields:
            fields[key] = str(fields[key])
    return fields


def _nfs_rule_from_params(module, versions):
    """Build a new NFS export rule from the task.

    The array defaults are applied to any rule field the task did not set.
    """
    return _nfs_rule(
        versions,
        client=module.params["client"],
        permission=_default(module.params["permission"], "ro"),
        access=_default(module.params["access"], "root-squash"),
        anonuid=_anon_id(module.params["anonuid"]),
        anongid=_anon_id(module.params["anongid"]),
        fileid_32bit=_default(module.params["fileid_32bit"], False),
        atime=_default(module.params["atime"], True),
        secure=_default(module.params["secure"], False),
        security=_default(module.params["security"], ["sys"]),
    )


def _smb_client_rule_from_params(module, versions):
    """Build a new SMB client rule from the task.

    The array defaults are applied to any rule field the task did not set.
    """
    if LooseVersion(SMB_ENCRYPT_API_VERSION) <= LooseVersion(versions):
        return SmbClientPolicyRule(
            client=module.params["client"],
            permission=_default(module.params["permission"], "ro"),
            encryption=_default(module.params["smb_encryption"], "optional"),
        )
    return SmbClientPolicyRule(
        client=module.params["client"],
        permission=_default(module.params["permission"], "ro"),
    )


def delete_smb_share_policy(module, blade):
    """Delete SMB Share Policy, or Rule

    If principal is provided then delete the principal rule if it exists.
    """

    changed = False
    versions = get_rest_api_version(blade)
    policy_delete = True
    if module.params["principal"]:
        policy_delete = False
        if (
            LooseVersion(CONTEXT_API_VERSION) <= LooseVersion(versions)
            and module.params["context"]
        ):
            prin_rule = blade.get_smb_share_policies_rules(
                policy_names=[module.params["name"]],
                filter="principal='" + module.params["principal"] + "'",
                context_names=[module.params["context"]],
            )
        else:
            prin_rule = blade.get_smb_share_policies_rules(
                policy_names=[module.params["name"]],
                filter="principal='" + module.params["principal"] + "'",
            )
        # A filter that matches nothing is a 200 with no items, so guard the
        # lookup or the second delete of the same principal raises IndexError.
        if prin_rule.status_code == 200 and prin_rule.total_item_count != 0:
            rule = list(prin_rule.items)[0]
            changed = True
            if not module.check_mode:
                # Share rule names are the principal, unique only within the
                # policy, so the policy must be named as well.
                res = blade.delete_smb_share_policies_rules(
                    policy_names=[module.params["name"]],
                    names=[rule.name],
                    **_context_kwargs(module, versions),
                )
                if res.status_code != 200:
                    module.fail_json(
                        msg="Failed to delete rule for principal {0} in policy {1}. "
                        "Error: {2}".format(
                            module.params["principal"],
                            module.params["name"],
                            get_error_message(res),
                        )
                    )
    if policy_delete:
        changed = True
        if not module.check_mode:
            if (
                LooseVersion(CONTEXT_API_VERSION) <= LooseVersion(versions)
                and module.params["context"]
            ):
                res = blade.delete_smb_share_policies(
                    names=[module.params["name"]],
                    context_names=[module.params["context"]],
                )
            else:
                res = blade.delete_smb_share_policies(names=[module.params["name"]])
            if res.status_code != 200:
                module.fail_json(
                    msg="Failed to delete SMB share policy {0}. Error: {1}".format(
                        module.params["name"], get_error_message(res)
                    )
                )
    module.exit_json(changed=changed)


def rename_smb_share_policy(module, blade):
    """Rename SMB Share Policy"""

    changed = True
    versions = get_rest_api_version(blade)
    if not module.check_mode:
        if (
            LooseVersion(CONTEXT_API_VERSION) <= LooseVersion(versions)
            and module.params["context"]
        ):
            res = blade.patch_smb_share_policies(
                names=[module.params["name"]],
                policy=SmbSharePolicy(name=module.params["rename"]),
                context_names=[module.params["context"]],
            )
        else:
            res = blade.patch_smb_share_policies(
                names=[module.params["name"]],
                policy=SmbSharePolicy(name=module.params["rename"]),
            )
        if res.status_code != 200:
            module.fail_json(
                msg="Failed to rename SMB share policy {0} to {1}. Error: {2}".format(
                    module.params["name"],
                    module.params["rename"],
                    get_error_message(res),
                )
            )
    module.exit_json(changed=changed)


def create_smb_share_policy(module, blade):
    """Create SMB Share Policy"""
    changed = True
    versions = get_rest_api_version(blade)
    if not module.check_mode:
        if (
            LooseVersion(CONTEXT_API_VERSION) <= LooseVersion(versions)
            and module.params["context"]
        ):
            res = blade.post_smb_share_policies(
                names=[module.params["name"]], context_names=[module.params["context"]]
            )
        else:
            res = blade.post_smb_share_policies(names=[module.params["name"]])
        if res.status_code != 200:
            module.fail_json(
                msg="Failed to create SMB share policy {0}.Error: {1}".format(
                    module.params["name"], get_error_message(res)
                )
            )
        if not module.params["enabled"]:
            if (
                LooseVersion(CONTEXT_API_VERSION) <= LooseVersion(versions)
                and module.params["context"]
            ):
                res = blade.patch_smb_share_policies(
                    policy=SmbSharePolicy(enabled=False),
                    names=[module.params["name"]],
                    context_names=[module.params["context"]],
                )
            else:
                res = blade.patch_smb_share_policies(
                    policy=SmbSharePolicy(enabled=False), names=[module.params["name"]]
                )
            if res.status_code != 200:
                if (
                    LooseVersion(CONTEXT_API_VERSION) <= LooseVersion(versions)
                    and module.params["context"]
                ):
                    blade.delete_smb_share_policies(
                        names=[module.params["name"]],
                        context_names=[module.params["context"]],
                    )
                else:
                    blade.delete_smb_share_policies(names=[module.params["name"]])
                module.fail_json(
                    msg="Failed to create SMB share policy {0}.Error: {1}".format(
                        module.params["name"], get_error_message(res)
                    )
                )
        if not module.params["principal"]:
            module.fail_json(msg="principal is required to create a new rule")
        else:
            # Send the permissions as the array stores them (see
            # _share_rule_perms); the raw task values can mix allow and deny,
            # which POST rejects.
            perms = _share_rule_desired(module, {})
            rule = SmbSharePolicyRule(
                principal=module.params["principal"],
                **{level: value for level, value in perms.items() if value},
            )
            if (
                LooseVersion(CONTEXT_API_VERSION) <= LooseVersion(versions)
                and module.params["context"]
            ):
                res = blade.post_smb_share_policies_rules(
                    policy_names=[module.params["name"]],
                    rule=rule,
                    context_names=[module.params["context"]],
                )
            else:
                res = blade.post_smb_share_policies_rules(
                    policy_names=[module.params["name"]],
                    rule=rule,
                )
            if res.status_code != 200:
                module.fail_json(
                    msg="Failed to create rule for policy {0}. Error: {1}".format(
                        module.params["name"],
                        get_error_message(res),
                    )
                )
    module.exit_json(changed=changed)


def update_smb_share_policy(module, blade):
    """Update SMB Share Policy Rule"""

    changed = False
    versions = get_rest_api_version(blade)
    if module.params["principal"]:
        if (
            LooseVersion(CONTEXT_API_VERSION) <= LooseVersion(versions)
            and module.params["context"]
        ):
            current_policy_rule = blade.get_smb_share_policies_rules(
                policy_names=[module.params["name"]],
                filter="principal='" + module.params["principal"] + "'",
                context_names=[module.params["context"]],
            )
        else:
            current_policy_rule = blade.get_smb_share_policies_rules(
                policy_names=[module.params["name"]],
                filter="principal='" + module.params["principal"] + "'",
            )
        if current_policy_rule.status_code != 200:
            module.fail_json(
                msg="Failed to get SMB export policy rules for {0}. Error: {1}".format(
                    module.params["name"],
                    current_policy_rule.errors[0].message,
                )
            )
        elif (
            current_policy_rule.status_code == 200
            and current_policy_rule.total_item_count == 0
        ):
            perms = _share_rule_desired(module, {})
            rule = SmbSharePolicyRule(
                principal=module.params["principal"],
                **{level: value for level, value in perms.items() if value},
            )
            changed = True
            if not module.check_mode:
                # Share rules are unordered; the SDK has no before_rule_name
                # for them.
                res = blade.post_smb_share_policies_rules(
                    policy_names=[module.params["name"]],
                    rule=rule,
                    **_context_kwargs(module, versions),
                )
                if res.status_code != 200:
                    module.fail_json(
                        msg="Failed to create rule for principal {0} "
                        "in policy {1}. Error: {2}".format(
                            module.params["principal"],
                            module.params["name"],
                            get_error_message(res),
                        )
                    )
        else:
            old_policy_rule = list(current_policy_rule.items)[0]
            # The array reports a rule as its ladder of granted levels (see
            # _share_rule_perms), so both sides are normalised: the task's
            # full_control=deny next to read/change=allow equals the None the
            # array stores for "not granted". The principal is not compared;
            # the filter above matched it (case-insensitively) and PATCH
            # cannot change it.
            live_perms = {
                level: getattr(old_policy_rule, level) for level in _SHARE_LEVELS
            }
            current_perms = _share_rule_perms(live_perms, {})
            new_perms = _share_rule_desired(module, live_perms)
            if new_perms != current_perms:
                changed = True
                if not module.check_mode:
                    # PATCH smb-share-policies/rules rejects `principal`
                    # ("Invalid body parameter: principal") and bodies that
                    # mix allow and deny, so send the single field that moves
                    # the ladder (see _share_rule_patch).
                    rule = SmbSharePolicyRule(
                        **_share_rule_patch(current_perms, new_perms)
                    )
                    # Share rules are unordered and carry no index: the rule
                    # is named after its principal, which is only unique
                    # within the policy, so address it by name and policy.
                    res = blade.patch_smb_share_policies_rules(
                        policy_names=[module.params["name"]],
                        names=[old_policy_rule.name],
                        rule=rule,
                        **_context_kwargs(module, versions),
                    )
                    if res.status_code != 200:
                        module.fail_json(
                            msg="Failed to update SMB share rule {0} in policy {1}. "
                            "Error: {2}".format(
                                old_policy_rule.name,
                                module.params["name"],
                                get_error_message(res),
                            )
                        )
    if (
        LooseVersion(CONTEXT_API_VERSION) <= LooseVersion(versions)
        and module.params["context"]
    ):
        current_policy = list(
            blade.get_smb_share_policies(
                names=[module.params["name"]], context_names=[module.params["context"]]
            ).items
        )[0]
    else:
        current_policy = list(
            blade.get_smb_share_policies(names=[module.params["name"]]).items
        )[0]
    if current_policy.enabled != module.params["enabled"]:
        changed = True
        if not module.check_mode:
            if (
                LooseVersion(CONTEXT_API_VERSION) <= LooseVersion(versions)
                and module.params["context"]
            ):
                res = blade.patch_smb_share_policies(
                    policy=SmbSharePolicy(enabled=module.params["enabled"]),
                    names=[module.params["name"]],
                    context_names=[module.params["context"]],
                )
            else:
                res = blade.patch_smb_share_policies(
                    policy=SmbSharePolicy(enabled=module.params["enabled"]),
                    names=[module.params["name"]],
                )
            if res.status_code != 200:
                module.fail_json(
                    msg="Failed to change state of SMB share policy {0}.Error: {1}".format(
                        module.params["name"], get_error_message(res)
                    )
                )
    module.exit_json(changed=changed)


def delete_smb_client_policy(module, blade):
    """Delete SMB CLient Policy, or Rule

    If client is provided then delete the client rule if it exists.
    """

    changed = False
    versions = get_rest_api_version(blade)
    policy_delete = True
    if module.params["client"]:
        policy_delete = False
        if (
            LooseVersion(CONTEXT_API_VERSION) <= LooseVersion(versions)
            and module.params["context"]
        ):
            res = blade.get_smb_client_policies_rules(
                policy_names=[module.params["name"]],
                filter="client='" + module.params["client"] + "'",
                context_names=[module.params["context"]],
            )
        else:
            res = blade.get_smb_client_policies_rules(
                policy_names=[module.params["name"]],
                filter="client='" + module.params["client"] + "'",
            )
        if res.status_code == 200:
            if res.total_item_count == 0:
                pass
            elif res.total_item_count == 1:
                rule = list(res.items)[0]
                if module.params["client"] == rule.client:
                    changed = True
                    if not module.check_mode:
                        if (
                            LooseVersion(CONTEXT_API_VERSION) <= LooseVersion(versions)
                            and module.params["context"]
                        ):
                            res = blade.delete_smb_client_policies_rules(
                                names=[rule.name],
                                context_names=[module.params["context"]],
                            )
                        else:
                            res = blade.delete_smb_client_policies_rules(
                                names=[rule.name]
                            )
                        if res.status_code != 200:
                            module.fail_json(
                                msg="Failed to delete rule for client {0} in policy {1}. "
                                "Error: {2}".format(
                                    module.params["client"],
                                    module.params["name"],
                                    get_error_message(res),
                                )
                            )
            else:
                rules = list(res.items)
                for cli in rules:
                    if cli.client == "*":
                        changed = True
                        if not module.check_mode:
                            if (
                                LooseVersion(CONTEXT_API_VERSION)
                                <= LooseVersion(versions)
                                and module.params["context"]
                            ):
                                res = blade.delete_smb_client_policies_rules(
                                    names=[cli.name],
                                    context_names=[module.params["context"]],
                                )
                            else:
                                res = blade.delete_smb_client_policies_rules(
                                    names=[cli.name]
                                )
                            if res.status_code != 200:
                                module.fail_json(
                                    msg="Failed to delete rule for client {0} in policy {1}. "
                                    "Error: {2}".format(
                                        module.params["client"],
                                        module.params["name"],
                                        get_error_message(res),
                                    )
                                )
    if policy_delete:
        changed = True
        if not module.check_mode:
            if (
                LooseVersion(CONTEXT_API_VERSION) <= LooseVersion(versions)
                and module.params["context"]
            ):
                res = blade.delete_smb_client_policies(
                    names=[module.params["name"]],
                    context_names=[module.params["context"]],
                )
            else:
                res = blade.delete_smb_client_policies(names=[module.params["name"]])
            if res.status_code != 200:
                module.fail_json(
                    msg="Failed to delete SMB client policy {0}. Error: {1}".format(
                        module.params["name"], get_error_message(res)
                    )
                )
    module.exit_json(changed=changed)


def rename_smb_client_policy(module, blade):
    """Rename SMB Client Policy"""

    changed = True
    versions = get_rest_api_version(blade)
    if not module.check_mode:
        if (
            LooseVersion(CONTEXT_API_VERSION) <= LooseVersion(versions)
            and module.params["context"]
        ):
            res = blade.patch_smb_client_policies(
                names=[module.params["name"]],
                policy=SmbClientPolicy(name=module.params["rename"]),
                context_names=[module.params["context"]],
            )
        else:
            res = blade.patch_smb_client_policies(
                names=[module.params["name"]],
                policy=SmbClientPolicy(name=module.params["rename"]),
            )
        if res.status_code != 200:
            module.fail_json(
                msg="Failed to rename SMB client policy {0} to {1}. Error: {2}".format(
                    module.params["name"],
                    module.params["rename"],
                    get_error_message(res),
                )
            )
    module.exit_json(changed=changed)


def create_smb_client_policy(module, blade):
    """Create SMB Client Policy"""
    changed = True
    versions = get_rest_api_version(blade)
    if not module.check_mode:
        if (
            LooseVersion(CONTEXT_API_VERSION) <= LooseVersion(versions)
            and module.params["context"]
        ):
            res = blade.post_smb_client_policies(
                names=[module.params["name"]], context_names=[module.params["context"]]
            )
        else:
            res = blade.post_smb_client_policies(names=[module.params["name"]])
        if res.status_code != 200:
            module.fail_json(
                msg="Failed to create SMB client policy {0}.Error: {1}".format(
                    module.params["name"], get_error_message(res)
                )
            )
        if not module.params["enabled"]:
            if (
                LooseVersion(CONTEXT_API_VERSION) <= LooseVersion(versions)
                and module.params["context"]
            ):
                res = blade.patch_smb_client_policies(
                    policy=SmbClientPolicy(enabled=False),
                    names=[module.params["name"]],
                    context_names=[module.params["context"]],
                )
            else:
                res = blade.patch_smb_client_policies(
                    policy=SmbClientPolicy(enabled=False), names=[module.params["name"]]
                )
            if res.status_code != 200:
                if (
                    LooseVersion(CONTEXT_API_VERSION) <= LooseVersion(versions)
                    and module.params["context"]
                ):
                    blade.delete_smb_client_policies(
                        names=[module.params["name"]],
                        context_names=[module.params["context"]],
                    )
                else:
                    blade.delete_smb_client_policies(names=[module.params["name"]])
                module.fail_json(
                    msg="Failed to create SMB client policy {0}.Error: {1}".format(
                        module.params["name"], get_error_message(res)
                    )
                )
        if not module.params["client"]:
            module.fail_json(msg="client is required to create a new rule")
        else:
            rule = _smb_client_rule_from_params(module, versions)
            if (
                LooseVersion(CONTEXT_API_VERSION) <= LooseVersion(versions)
                and module.params["context"]
            ):
                res = blade.post_smb_client_policies_rules(
                    policy_names=[module.params["name"]],
                    rule=rule,
                    context_names=[module.params["context"]],
                )
            else:
                res = blade.post_smb_client_policies_rules(
                    policy_names=[module.params["name"]],
                    rule=rule,
                )
            if res.status_code != 200:
                module.fail_json(
                    msg="Failed to rule for policy {0}. Error: {1}".format(
                        module.params["name"],
                        get_error_message(res),
                    )
                )
    module.exit_json(changed=changed)


def create_network_access_policy(module, blade):
    """Create Network Access Policy

    The FlashBlade REST API has no POST for network-access-policies (only the
    rules endpoint can be posted to), so a named policy can never be created.
    Fail clearly instead of raising an AttributeError from the SDK.
    """
    module.fail_json(
        msg="Network access policy {0} does not exist and network access "
        "policies cannot be created. Only the rules of an existing policy "
        "(normally default-network-access-policy) can be managed.".format(
            module.params["name"]
        )
    )


def create_worm_data_policy(module, blade):
    """Create WORM Data Policy"""
    changed = True
    versions = get_rest_api_version(blade)
    if not module.check_mode:
        min_retention = time_to_milliseconds(module.params["min_retention"])
        max_retention = time_to_milliseconds(module.params["max_retention"])
        default_retention = time_to_milliseconds(module.params["default_retention"])
        if (
            LooseVersion(CONTEXT_API_VERSION) <= LooseVersion(versions)
            and module.params["context"]
        ):
            res = blade.post_worm_data_policies(
                policy=WormDataPolicy(
                    enabled=module.params["enabled"],
                    mode="compliance",
                    min_retention=min_retention,
                    max_retention=max_retention,
                    default_retention=default_retention,
                    retention_lock=module.params["retention_lock"],
                ),
                names=[module.params["name"]],
                context_names=[module.params["context"]],
            )
        else:
            res = blade.post_worm_data_policies(
                policy=WormDataPolicy(
                    enabled=module.params["enabled"],
                    mode="compliance",
                    min_retention=min_retention,
                    max_retention=max_retention,
                    default_retention=default_retention,
                    retention_lock=module.params["retention_lock"],
                ),
                names=[module.params["name"]],
            )
        if res.status_code != 200:
            if (
                LooseVersion(CONTEXT_API_VERSION) <= LooseVersion(versions)
                and module.params["context"]
            ):
                blade.delete_worm_data_policies(
                    names=[module.params["name"]],
                    context_names=[module.params["context"]],
                )
            else:
                blade.delete_worm_data_policies(names=[module.params["name"]])
            module.fail_json(
                msg="Failed to create WORM data policy {0}.Error: {1}".format(
                    module.params["name"], get_error_message(res)
                )
            )
    module.exit_json(changed=changed)


def update_smb_client_policy(module, blade):
    """Update SMB Client Policy Rule"""

    changed = False
    versions = get_rest_api_version(blade)
    if module.params["client"]:
        if (
            LooseVersion(CONTEXT_API_VERSION) <= LooseVersion(versions)
            and module.params["context"]
        ):
            current_policy_rule = blade.get_smb_client_policies_rules(
                policy_names=[module.params["name"]],
                filter="client='" + module.params["client"] + "'",
                context_names=[module.params["context"]],
            )
        else:
            current_policy_rule = blade.get_smb_client_policies_rules(
                policy_names=[module.params["name"]],
                filter="client='" + module.params["client"] + "'",
            )
        # If the rule lookup fails or returns nothing then no rule exists for
        # this client yet - some Purity//FB versions return an error rather
        # than an empty result set - so create it. Otherwise reconcile the
        # existing rule.
        if (
            current_policy_rule.status_code != 200
            or current_policy_rule.total_item_count == 0
        ):
            rule = _smb_client_rule_from_params(module, versions)
            changed = True
            if not module.check_mode:
                if module.params["before_rule"]:
                    before_name = (
                        module.params["name"] + "." + str(module.params["before_rule"])
                    )
                    if (
                        LooseVersion(CONTEXT_API_VERSION) <= LooseVersion(versions)
                        and module.params["context"]
                    ):
                        res = blade.post_smb_client_policies_rules(
                            policy_names=[module.params["name"]],
                            rule=rule,
                            before_rule_name=before_name,
                            context_names=[module.params["context"]],
                        )
                    else:
                        res = blade.post_smb_client_policies_rules(
                            policy_names=[module.params["name"]],
                            rule=rule,
                            before_rule_name=before_name,
                        )
                else:
                    if (
                        LooseVersion(CONTEXT_API_VERSION) <= LooseVersion(versions)
                        and module.params["context"]
                    ):
                        res = blade.post_smb_client_policies_rules(
                            policy_names=[module.params["name"]],
                            rule=rule,
                            context_names=[module.params["context"]],
                        )
                    else:
                        res = blade.post_smb_client_policies_rules(
                            policy_names=[module.params["name"]],
                            rule=rule,
                        )
                if res.status_code != 200:
                    module.fail_json(
                        msg="Failed to create rule for client {0} "
                        "in policy {1}. Error: {2}".format(
                            module.params["client"],
                            module.params["name"],
                            get_error_message(res),
                        )
                    )
        else:
            rules = list(current_policy_rule.items)
            cli_count = None
            done = False
            if module.params["client"] == "*":
                for cli in rules:
                    if cli.client == "*":
                        cli_count = cli
                if not cli_count:
                    rule = _smb_client_rule_from_params(module, versions)
                    done = True
                    changed = True
                    if not module.check_mode:
                        if module.params["before_rule"]:
                            if (
                                LooseVersion(CONTEXT_API_VERSION)
                                <= LooseVersion(versions)
                                and module.params["context"]
                            ):
                                res = blade.post_smb_client_policies_rules(
                                    policy_names=[module.params["name"]],
                                    rule=rule,
                                    before_rule_name=(
                                        module.params["name"]
                                        + "."
                                        + str(module.params["before_rule"]),
                                    ),
                                    context_names=[module.params["context"]],
                                )
                            else:
                                res = blade.post_smb_client_policies_rules(
                                    policy_names=[module.params["name"]],
                                    rule=rule,
                                    before_rule_name=(
                                        module.params["name"]
                                        + "."
                                        + str(module.params["before_rule"]),
                                    ),
                                )
                        else:
                            if (
                                LooseVersion(CONTEXT_API_VERSION)
                                <= LooseVersion(versions)
                                and module.params["context"]
                            ):
                                res = blade.post_smb_client_policies_rules(
                                    policy_names=[module.params["name"]],
                                    rule=rule,
                                    context_names=[module.params["context"]],
                                )
                            else:
                                res = blade.post_smb_client_policies_rules(
                                    policy_names=[module.params["name"]],
                                    rule=rule,
                                )
                        if res.status_code != 200:
                            module.fail_json(
                                msg="Failed to create rule for "
                                "client {0} in policy {1}. Error: {2}".format(
                                    module.params["client"],
                                    module.params["name"],
                                    get_error_message(res),
                                )
                            )
            if not done:
                old_policy_rule = rules[0]
                # Fields the task did not set keep their current value.
                current_rule = {
                    "client": old_policy_rule.client,
                    "permission": old_policy_rule.permission,
                }
                new_rule = {
                    "client": module.params["client"],
                    "permission": _default(
                        module.params["permission"], current_rule["permission"]
                    ),
                }
                if LooseVersion(SMB_ENCRYPT_API_VERSION) <= LooseVersion(versions):
                    current_rule["encryption"] = old_policy_rule.encryption
                    new_rule["encryption"] = _default(
                        module.params["smb_encryption"], current_rule["encryption"]
                    )
                if current_rule != new_rule:
                    changed = True
                    if not module.check_mode:
                        if LooseVersion(SMB_ENCRYPT_API_VERSION) <= LooseVersion(
                            versions
                        ):
                            rule = SmbClientPolicyRule(
                                client=new_rule["client"],
                                permission=new_rule["permission"],
                                encryption=new_rule["encryption"],
                            )
                        else:
                            rule = SmbClientPolicyRule(
                                client=new_rule["client"],
                                permission=new_rule["permission"],
                            )
                        if (
                            LooseVersion(CONTEXT_API_VERSION) <= LooseVersion(versions)
                            and module.params["context"]
                        ):
                            res = blade.patch_smb_client_policies_rules(
                                names=[
                                    module.params["name"]
                                    + "."
                                    + str(old_policy_rule.index)
                                ],
                                rule=rule,
                                context_names=[module.params["context"]],
                            )
                        else:
                            res = blade.patch_smb_client_policies_rules(
                                names=[
                                    module.params["name"]
                                    + "."
                                    + str(old_policy_rule.index)
                                ],
                                rule=rule,
                            )
                        if res.status_code != 200:
                            module.fail_json(
                                msg="Failed to update SMB client rule {0}. Error: {1}".format(
                                    module.params["name"]
                                    + "."
                                    + str(old_policy_rule.index),
                                    get_error_message(res),
                                )
                            )
                if (
                    module.params["before_rule"]
                    and module.params["before_rule"] != old_policy_rule.index
                ):
                    changed = True
                    if not module.check_mode:
                        before_name = (
                            module.params["name"]
                            + "."
                            + str(module.params["before_rule"])
                        )
                        if (
                            LooseVersion(CONTEXT_API_VERSION) <= LooseVersion(versions)
                            and module.params["context"]
                        ):
                            res = blade.patch_smb_client_policies_rules(
                                names=[
                                    module.params["name"]
                                    + "."
                                    + str(old_policy_rule.index)
                                ],
                                rule=SmbClientPolicyRule(),
                                before_rule_name=before_name,
                                context_names=[module.params["context"]],
                            )
                        else:
                            res = blade.patch_smb_client_policies_rules(
                                names=[
                                    module.params["name"]
                                    + "."
                                    + str(old_policy_rule.index)
                                ],
                                rule=SmbClientPolicyRule(),
                                before_rule_name=before_name,
                            )
                        if res.status_code != 200:
                            module.fail_json(
                                msg="Failed to move SMB client rule {0}. Error: {1}".format(
                                    module.params["name"]
                                    + "."
                                    + str(old_policy_rule.index),
                                    get_error_message(res),
                                )
                            )
    if (
        LooseVersion(CONTEXT_API_VERSION) <= LooseVersion(versions)
        and module.params["context"]
    ):
        current_policy = list(
            blade.get_smb_client_policies(
                names=[module.params["name"]], context_names=[module.params["context"]]
            ).items
        )[0]
    else:
        current_policy = list(
            blade.get_smb_client_policies(names=[module.params["name"]]).items
        )[0]
    if current_policy.enabled != module.params["enabled"]:
        changed = True
        if not module.check_mode:
            if (
                LooseVersion(CONTEXT_API_VERSION) <= LooseVersion(versions)
                and module.params["context"]
            ):
                res = blade.patch_smb_client_policies(
                    policy=SmbClientPolicy(enabled=module.params["enabled"]),
                    names=[module.params["name"]],
                    context_names=[module.params["context"]],
                )
            else:
                res = blade.patch_smb_client_policies(
                    policy=SmbClientPolicy(enabled=module.params["enabled"]),
                    names=[module.params["name"]],
                )
            if res.status_code != 200:
                module.fail_json(
                    msg="Failed to change state of SMB client policy {0}.Error: {1}".format(
                        module.params["name"], get_error_message(res)
                    )
                )
    module.exit_json(changed=changed)


def delete_nfs_policy(module, blade):
    """Delete NFS Export Policy, or Rule

    If client is provided then delete the client rule if it exists.
    """

    changed = False
    versions = get_rest_api_version(blade)
    policy_delete = True
    if module.params["client"]:
        policy_delete = False
        if (
            LooseVersion(CONTEXT_API_VERSION) <= LooseVersion(versions)
            and module.params["context"]
        ):
            res = blade.get_nfs_export_policies_rules(
                policy_names=[module.params["name"]],
                filter="client='" + module.params["client"] + "'",
                context_names=[module.params["context"]],
            )
        else:
            res = blade.get_nfs_export_policies_rules(
                policy_names=[module.params["name"]],
                filter="client='" + module.params["client"] + "'",
            )
        if res.status_code == 200:
            if res.total_item_count == 0:
                pass
            elif res.total_item_count == 1:
                rule = list(res.items)[0]
                if module.params["client"] == rule.client:
                    changed = True
                    if not module.check_mode:
                        if (
                            LooseVersion(CONTEXT_API_VERSION) <= LooseVersion(versions)
                            and module.params["context"]
                        ):
                            res = blade.delete_nfs_export_policies_rules(
                                names=[rule.name],
                                context_names=[module.params["context"]],
                            )
                        else:
                            res = blade.delete_nfs_export_policies_rules(
                                names=[rule.name]
                            )
                        if res.status_code != 200:
                            module.fail_json(
                                msg="Failed to delete rule for client {0} in policy {1}. "
                                "Error: {2}".format(
                                    module.params["client"],
                                    module.params["name"],
                                    get_error_message(res),
                                )
                            )
            else:
                rules = list(res.items)
                for cli in rules:
                    if cli.client == "*":
                        changed = True
                        if not module.check_mode:
                            if (
                                LooseVersion(CONTEXT_API_VERSION)
                                <= LooseVersion(versions)
                                and module.params["context"]
                            ):
                                res = blade.delete_nfs_export_policies_rules(
                                    names=[cli.name],
                                    context_names=[module.params["context"]],
                                )
                            else:
                                res = blade.delete_nfs_export_policies_rules(
                                    names=[cli.name]
                                )
                            if res.status_code != 200:
                                module.fail_json(
                                    msg="Failed to delete rule for client {0} in policy {1}. "
                                    "Error: {2}".format(
                                        module.params["client"],
                                        module.params["name"],
                                        get_error_message(res),
                                    )
                                )
    if policy_delete:
        changed = True
        if not module.check_mode:
            if (
                LooseVersion(CONTEXT_API_VERSION) <= LooseVersion(versions)
                and module.params["context"]
            ):
                res = blade.delete_nfs_export_policies(
                    names=[module.params["name"]],
                    context_names=[module.params["context"]],
                )
            else:
                res = blade.delete_nfs_export_policies(names=[module.params["name"]])
            if res.status_code != 200:
                module.fail_json(
                    msg="Failed to delete export policy {0}. Error: {1}".format(
                        module.params["name"], get_error_message(res)
                    )
                )
    module.exit_json(changed=changed)


def _network_rules_for_client(blade, module):
    """Return the rules of the network access policy that match I(client).

    The filter is applied client-side as well: a client of ``*`` is a
    wildcard to the REST filter and would otherwise match every rule.
    """
    res = blade.get_network_access_policies_rules(
        policy_names=[module.params["name"]],
        filter="client='" + module.params["client"] + "'",
    )
    if res.status_code != 200:
        return []
    return [rule for rule in list(res.items) if rule.client == module.params["client"]]


def update_network_access_policy(module, blade):
    """Update Network Access Policy Rule

    The network-access-policies endpoints take no fleet context, so no
    context_names is passed here.
    """

    changed = False
    if module.params["client"]:
        existing_rules = _network_rules_for_client(blade, module)
        if not existing_rules:
            rule = NetworkAccessPolicyRule(
                client=module.params["client"],
                effect=_default(module.params["effect"], "allow"),
                interfaces=module.params["interfaces"],
            )
            changed = True
            if not module.check_mode:
                post_kwargs = {}
                if module.params["before_rule"]:
                    post_kwargs["before_rule_name"] = (
                        module.params["name"] + "." + str(module.params["before_rule"])
                    )
                res = blade.post_network_access_policies_rules(
                    policy_names=[module.params["name"]], rule=rule, **post_kwargs
                )
                if res.status_code != 200:
                    module.fail_json(
                        msg="Failed to create rule for client {0} "
                        "in policy {1}. Error: {2}".format(
                            module.params["client"],
                            module.params["name"],
                            get_error_message(res),
                        )
                    )
        else:
            old_policy_rule = existing_rules[0]
            rule_name = module.params["name"] + "." + str(old_policy_rule.index)
            # Fields the task did not set keep their current value. Interface
            # lists are compared order-insensitively.
            current_rule = {
                "effect": old_policy_rule.effect,
                "interfaces": sorted(old_policy_rule.interfaces or []),
            }
            new_rule = {
                "effect": _default(module.params["effect"], current_rule["effect"]),
                "interfaces": (
                    sorted(module.params["interfaces"])
                    if module.params["interfaces"]
                    else current_rule["interfaces"]
                ),
            }
            if current_rule != new_rule:
                changed = True
                if not module.check_mode:
                    rule = NetworkAccessPolicyRule(
                        client=module.params["client"],
                        effect=new_rule["effect"],
                        interfaces=new_rule["interfaces"],
                    )
                    res = blade.patch_network_access_policies_rules(
                        names=[rule_name], rule=rule
                    )
                    if res.status_code != 200:
                        module.fail_json(
                            msg="Failed to update network access client rule {0}. "
                            "Error: {1}".format(rule_name, get_error_message(res))
                        )
            if (
                module.params["before_rule"]
                and module.params["before_rule"] != old_policy_rule.index
            ):
                changed = True
                if not module.check_mode:
                    before_name = (
                        module.params["name"] + "." + str(module.params["before_rule"])
                    )
                    res = blade.patch_network_access_policies_rules(
                        names=[rule_name],
                        rule=NetworkAccessPolicyRule(),
                        before_rule_name=before_name,
                    )
                    if res.status_code != 200:
                        module.fail_json(
                            msg="Failed to move network access client rule {0}. "
                            "Error: {1}".format(rule_name, get_error_message(res))
                        )
    current_policy = list(
        blade.get_network_access_policies(names=[module.params["name"]]).items
    )[0]
    if current_policy.enabled != module.params["enabled"]:
        changed = True
        if not module.check_mode:
            res = blade.patch_network_access_policies(
                policy=NetworkAccessPolicy(enabled=module.params["enabled"]),
                names=[module.params["name"]],
            )
            if res.status_code != 200:
                module.fail_json(
                    msg="Failed to change state of network access policy {0}."
                    "Error: {1}".format(module.params["name"], get_error_message(res))
                )
    module.exit_json(changed=changed)


def delete_network_access_policy(module, blade):
    """Delete Network Access Policy Rule

    If client is provided then delete the client rule if it exists. The REST
    API has no DELETE for network-access-policies themselves, so a whole
    policy delete is refused.
    """

    changed = False
    if not module.params["client"]:
        module.fail_json(
            msg="Network access policies cannot be deleted. Specify `client` "
            "to delete a rule from policy {0}.".format(module.params["name"])
        )
    for rule in _network_rules_for_client(blade, module):
        changed = True
        if not module.check_mode:
            res = blade.delete_network_access_policies_rules(names=[rule.name])
            if res.status_code != 200:
                module.fail_json(
                    msg="Failed to delete rule for client {0} in policy {1}. "
                    "Error: {2}".format(
                        module.params["client"],
                        module.params["name"],
                        get_error_message(res),
                    )
                )
    module.exit_json(changed=changed)


def delete_worm_data_policy(module, blade):
    """Delete WORM data Policy"""

    changed = True
    versions = get_rest_api_version(blade)
    if not module.check_mode:
        if (
            LooseVersion(CONTEXT_API_VERSION) <= LooseVersion(versions)
            and module.params["context"]
        ):
            res = blade.delete_worm_data_policies(
                names=[module.params["name"]], context_names=[module.params["context"]]
            )
        else:
            res = blade.delete_worm_data_policies(names=[module.params["name"]])
        if res.status_code != 200:
            module.fail_json(
                msg="Failed to delete WORM data policy {0}. Error: {1}".format(
                    module.params["name"], get_error_message(res)
                )
            )
    module.exit_json(changed=changed)


def rename_worm_data_policy(module):
    """Rename WORM Data Policy"""

    changed = False
    module.warn("Renaming of WORM Data policies is not supported")
    module.exit_json(changed=changed)


def rename_network_access_policy(module, blade):
    """Rename Network Access Policy"""

    changed = True
    if not module.check_mode:
        res = blade.patch_network_access_policies(
            names=[module.params["name"]],
            policy=NetworkAccessPolicy(name=module.params["rename"]),
        )
        if res.status_code != 200:
            module.fail_json(
                msg="Failed to rename network access policy {0} to {1}. Error: {2}".format(
                    module.params["name"],
                    module.params["rename"],
                    get_error_message(res),
                )
            )
    module.exit_json(changed=changed)


def rename_nfs_policy(module, blade):
    """Rename NFS Export Policy"""

    changed = True
    versions = get_rest_api_version(blade)
    if not module.check_mode:
        if (
            LooseVersion(CONTEXT_API_VERSION) <= LooseVersion(versions)
            and module.params["context"]
        ):
            res = blade.patch_nfs_export_policies(
                names=[module.params["name"]],
                policy=NfsExportPolicy(name=module.params["rename"]),
                context_names=[module.params["context"]],
            )
        else:
            res = blade.patch_nfs_export_policies(
                names=[module.params["name"]],
                policy=NfsExportPolicy(name=module.params["rename"]),
            )
        if res.status_code != 200:
            module.fail_json(
                msg="Failed to rename NFS export policy {0} to {1}. Error: {2}".format(
                    module.params["name"],
                    module.params["rename"],
                    get_error_message(res),
                )
            )
    module.exit_json(changed=changed)


def update_worm_data_policy(module, blade):
    """Update WORM data policy"""

    changed = False
    versions = get_rest_api_version(blade)
    if (
        LooseVersion(CONTEXT_API_VERSION) <= LooseVersion(versions)
        and module.params["context"]
    ):
        current_policy_config = list(
            blade.get_worm_data_policies(
                names=[module.params["name"]], context_names=[module.params["context"]]
            ).items
        )[0]
    else:
        current_policy_config = list(
            blade.get_worm_data_policies(names=[module.params["name"]]).items
        )[0]
    current_policy = {
        "default_retention": current_policy_config.default_retention,
        "enabled": current_policy_config.enabled,
        "max_retention": current_policy_config.max_retention,
        "min_retention": current_policy_config.min_retention,
        "retention_lock": current_policy_config.retention_lock,
    }
    new_policy = {
        "default_retention": current_policy_config.default_retention,
        "enabled": current_policy_config.enabled,
        "max_retention": current_policy_config.max_retention,
        "min_retention": current_policy_config.min_retention,
        "retention_lock": current_policy_config.retention_lock,
    }
    if module.params["enabled"] != current_policy["enabled"]:
        new_policy["enabled"] = module.params["enabled"]
    if (
        module.params["retention_lock"]
        and module.params["retention_lock"] != current_policy["retention_lock"]
    ):
        new_policy["retention_lock"] = module.params["retention_lock"]
    if (
        module.params["default_retention"]
        and time_to_milliseconds(module.params["default_retention"])
        != current_policy["default_retention"]
    ):
        new_policy["default_retention"] = time_to_milliseconds(
            module.params["default_retention"]
        )
    if (
        module.params["max_retention"]
        and time_to_milliseconds(module.params["max_retention"])
        != current_policy["max_retention"]
    ):
        new_policy["max_retention"] = time_to_milliseconds(
            module.params["max_retention"]
        )
    if (
        module.params["min_retention"]
        and time_to_milliseconds(module.params["min_retention"])
        != current_policy["min_retention"]
    ):
        new_policy["min_retention"] = time_to_milliseconds(
            module.params["min_retention"]
        )
    if new_policy != current_policy:
        changed = True
        if not module.check_mode:
            worm_policy = WormDataPolicy(
                enabled=new_policy["enabled"],
                retention_lock=new_policy["retention_lock"],
                min_retention=new_policy["min_retention"],
                max_retention=new_policy["max_retention"],
                default_retention=new_policy["default_retention"],
            )
            if (
                LooseVersion(CONTEXT_API_VERSION) <= LooseVersion(versions)
                and module.params["context"]
            ):
                res = blade.patch_worm_data_policies(
                    names=[module.params["name"]],
                    policy=worm_policy,
                    context_names=[module.params["context"]],
                )
            else:
                res = blade.patch_worm_data_policies(
                    names=[module.params["name"]], policy=worm_policy
                )
            if res.status_code != 200:
                module.fail_json(
                    msg="Failed to update WORM data policy {0}. Error: {1}".format(
                        module.params["name"],
                        get_error_message(res),
                    )
                )
    module.exit_json(changed=changed)


def update_nfs_policy(module, blade):
    """Update NFS Export Policy Rule"""

    changed = False
    versions = get_rest_api_version(blade)
    if module.params["client"]:
        if (
            LooseVersion(CONTEXT_API_VERSION) <= LooseVersion(versions)
            and module.params["context"]
        ):
            current_policy_rule = blade.get_nfs_export_policies_rules(
                policy_names=[module.params["name"]],
                filter="client='" + module.params["client"] + "'",
                context_names=[module.params["context"]],
            )
        else:
            current_policy_rule = blade.get_nfs_export_policies_rules(
                policy_names=[module.params["name"]],
                filter="client='" + module.params["client"] + "'",
            )
        # If the rule lookup fails or returns nothing then no rule exists for
        # this client yet - some Purity//FB versions return an error rather
        # than an empty result set - so create it. Otherwise reconcile the
        # existing rule.
        if (
            current_policy_rule.status_code != 200
            or current_policy_rule.total_item_count == 0
        ):
            rule = _nfs_rule_from_params(module, versions)
            changed = True
            if not module.check_mode:
                if module.params["before_rule"]:
                    before_name = (
                        module.params["name"] + "." + str(module.params["before_rule"])
                    )
                    if (
                        LooseVersion(CONTEXT_API_VERSION) <= LooseVersion(versions)
                        and module.params["context"]
                    ):
                        res = blade.post_nfs_export_policies_rules(
                            policy_names=[module.params["name"]],
                            rule=rule,
                            before_rule_name=before_name,
                            context_names=[module.params["context"]],
                        )
                    else:
                        res = blade.post_nfs_export_policies_rules(
                            policy_names=[module.params["name"]],
                            rule=rule,
                            before_rule_name=before_name,
                        )
                else:
                    if (
                        LooseVersion(CONTEXT_API_VERSION) <= LooseVersion(versions)
                        and module.params["context"]
                    ):
                        res = blade.post_nfs_export_policies_rules(
                            policy_names=[module.params["name"]],
                            rule=rule,
                            context_names=[module.params["context"]],
                        )
                    else:
                        res = blade.post_nfs_export_policies_rules(
                            policy_names=[module.params["name"]],
                            rule=rule,
                        )
                if res.status_code != 200:
                    module.fail_json(
                        msg="Failed to create rule for client {0} "
                        "in export policy {1}. Error: {2}".format(
                            module.params["client"],
                            module.params["name"],
                            get_error_message(res),
                        )
                    )
        else:
            rules = list(current_policy_rule.items)
            cli_count = None
            done = False
            if module.params["client"] == "*":
                for cli in rules:
                    if cli.client == "*":
                        cli_count = cli
                if not cli_count:
                    rule = _nfs_rule_from_params(module, versions)
                    done = True
                    changed = True
                    if not module.check_mode:
                        if module.params["before_rule"]:
                            if (
                                LooseVersion(CONTEXT_API_VERSION)
                                <= LooseVersion(versions)
                                and module.params["context"]
                            ):
                                res = blade.post_nfs_export_policies_rules(
                                    policy_names=[module.params["name"]],
                                    rule=rule,
                                    before_rule_name=(
                                        module.params["name"]
                                        + "."
                                        + str(module.params["before_rule"]),
                                    ),
                                    context_names=[module.params["context"]],
                                )
                            else:
                                res = blade.post_nfs_export_policies_rules(
                                    policy_names=[module.params["name"]],
                                    rule=rule,
                                    before_rule_name=(
                                        module.params["name"]
                                        + "."
                                        + str(module.params["before_rule"]),
                                    ),
                                )
                        else:
                            if (
                                LooseVersion(CONTEXT_API_VERSION)
                                <= LooseVersion(versions)
                                and module.params["context"]
                            ):
                                res = blade.post_nfs_export_policies_rules(
                                    policy_names=[module.params["name"]],
                                    rule=rule,
                                    context_names=[module.params["context"]],
                                )
                            else:
                                res = blade.post_nfs_export_policies_rules(
                                    policy_names=[module.params["name"]],
                                    rule=rule,
                                )
                        if res.status_code != 200:
                            module.fail_json(
                                msg="Failed to create rule for "
                                "client {0} in export policy {1}. Error: {2}".format(
                                    module.params["client"],
                                    module.params["name"],
                                    get_error_message(res),
                                )
                            )
            if not done:
                old_policy_rule = rules[0]
                # Fields the task did not set keep their current value. The
                # anon ids are compared as int (the PATCH body renders them
                # as strings, see _nfs_rule) and security is compared
                # order-insensitively but sent in the order it is stored,
                # since NFS clients can treat sec= order as a preference.
                current_rule = {
                    "access": getattr(old_policy_rule, "access", None),
                    "anongid": _anon_id(getattr(old_policy_rule, "anongid", None)),
                    "anonuid": _anon_id(getattr(old_policy_rule, "anonuid", None)),
                    "atime": old_policy_rule.atime,
                    "client": old_policy_rule.client,
                    "fileid_32bit": old_policy_rule.fileid_32bit,
                    "permission": old_policy_rule.permission,
                    "secure": old_policy_rule.secure,
                    "security": sorted(old_policy_rule.security or []),
                }
                new_rule = {
                    "access": _default(module.params["access"], current_rule["access"]),
                    "anongid": _default(
                        _anon_id(module.params["anongid"]), current_rule["anongid"]
                    ),
                    "anonuid": _default(
                        _anon_id(module.params["anonuid"]), current_rule["anonuid"]
                    ),
                    "atime": _default(module.params["atime"], current_rule["atime"]),
                    "client": module.params["client"],
                    "fileid_32bit": _default(
                        module.params["fileid_32bit"], current_rule["fileid_32bit"]
                    ),
                    "permission": _default(
                        module.params["permission"], current_rule["permission"]
                    ),
                    "secure": _default(module.params["secure"], current_rule["secure"]),
                    "security": (
                        sorted(module.params["security"])
                        if module.params["security"]
                        else current_rule["security"]
                    ),
                }
                if current_rule != new_rule:
                    changed = True
                    if not module.check_mode:
                        if module.params["security"]:
                            new_rule["security"] = module.params["security"]
                        else:
                            new_rule["security"] = old_policy_rule.security
                        rule = _nfs_rule(versions, patch=True, **new_rule)
                        if (
                            LooseVersion(CONTEXT_API_VERSION) <= LooseVersion(versions)
                            and module.params["context"]
                        ):
                            res = blade.patch_nfs_export_policies_rules(
                                names=[
                                    module.params["name"]
                                    + "."
                                    + str(old_policy_rule.index)
                                ],
                                rule=rule,
                                context_names=[module.params["context"]],
                            )
                        else:
                            res = blade.patch_nfs_export_policies_rules(
                                names=[
                                    module.params["name"]
                                    + "."
                                    + str(old_policy_rule.index)
                                ],
                                rule=rule,
                            )
                        if res.status_code != 200:
                            module.fail_json(
                                msg="Failed to update NFS export rule {0}. Error: {1}".format(
                                    module.params["name"]
                                    + "."
                                    + str(old_policy_rule.index),
                                    get_error_message(res),
                                )
                            )
                if (
                    module.params["before_rule"]
                    and module.params["before_rule"] != old_policy_rule.index
                ):
                    changed = True
                    if not module.check_mode:
                        before_name = (
                            module.params["name"]
                            + "."
                            + str(module.params["before_rule"])
                        )
                        if (
                            LooseVersion(CONTEXT_API_VERSION) <= LooseVersion(versions)
                            and module.params["context"]
                        ):
                            res = blade.patch_nfs_export_policies_rules(
                                names=[
                                    module.params["name"]
                                    + "."
                                    + str(old_policy_rule.index)
                                ],
                                rule=_nfs_rule(versions),
                                before_rule_name=before_name,
                                context_names=[module.params["context"]],
                            )
                        else:
                            res = blade.patch_nfs_export_policies_rules(
                                names=[
                                    module.params["name"]
                                    + "."
                                    + str(old_policy_rule.index)
                                ],
                                rule=_nfs_rule(versions),
                                before_rule_name=before_name,
                            )
                        if res.status_code != 200:
                            module.fail_json(
                                msg="Failed to move NFS export rule {0}. Error: {1}".format(
                                    module.params["name"]
                                    + "."
                                    + str(old_policy_rule.index),
                                    get_error_message(res),
                                )
                            )
    if (
        LooseVersion(CONTEXT_API_VERSION) <= LooseVersion(versions)
        and module.params["context"]
    ):
        current_policy = list(
            blade.get_nfs_export_policies(
                names=[module.params["name"]], context_names=[module.params["context"]]
            ).items
        )[0]
    else:
        current_policy = list(
            blade.get_nfs_export_policies(names=[module.params["name"]]).items
        )[0]
    if current_policy.enabled != module.params["enabled"]:
        changed = True
        if not module.check_mode:
            if (
                LooseVersion(CONTEXT_API_VERSION) <= LooseVersion(versions)
                and module.params["context"]
            ):
                res = blade.patch_nfs_export_policies(
                    policy=NfsExportPolicy(enabled=module.params["enabled"]),
                    names=[module.params["name"]],
                    context_names=[module.params["context"]],
                )
            else:
                res = blade.patch_nfs_export_policies(
                    policy=NfsExportPolicy(enabled=module.params["enabled"]),
                    names=[module.params["name"]],
                )
            if res.status_code != 200:
                module.fail_json(
                    msg="Failed to change state of nfs export policy {0}.Error: {1}".format(
                        module.params["name"], get_error_message(res)
                    )
                )
    module.exit_json(changed=changed)


def create_nfs_policy(module, blade):
    """Create NFS Export Policy"""
    changed = True
    versions = get_rest_api_version(blade)
    if not module.check_mode:
        if (
            LooseVersion(CONTEXT_API_VERSION) <= LooseVersion(versions)
            and module.params["context"]
        ):
            res = blade.post_nfs_export_policies(
                names=[module.params["name"]], context_names=[module.params["context"]]
            )
        else:
            res = blade.post_nfs_export_policies(names=[module.params["name"]])
        if res.status_code != 200:
            module.fail_json(
                msg="Failed to create nfs export policy {0}.Error: {1}".format(
                    module.params["name"], get_error_message(res)
                )
            )
        if not module.params["enabled"]:
            if (
                LooseVersion(CONTEXT_API_VERSION) <= LooseVersion(versions)
                and module.params["context"]
            ):
                res = blade.patch_nfs_export_policies(
                    policy=NfsExportPolicy(enabled=False),
                    names=[module.params["name"]],
                    context_names=[module.params["context"]],
                )
            else:
                res = blade.patch_nfs_export_policies(
                    policy=NfsExportPolicy(enabled=False), names=[module.params["name"]]
                )
            if res.status_code != 200:
                if (
                    LooseVersion(CONTEXT_API_VERSION) <= LooseVersion(versions)
                    and module.params["context"]
                ):
                    blade.delete_nfs_export_policies(
                        names=[module.params["name"]],
                        context_names=[module.params["context"]],
                    )
                else:
                    blade.delete_nfs_export_policies(names=[module.params["name"]])
                module.fail_json(
                    msg="Failed to create nfs export policy {0}.Error: {1}".format(
                        module.params["name"], get_error_message(res)
                    )
                )
        if module.params["client"]:
            rule = _nfs_rule_from_params(module, versions)
            if (
                LooseVersion(CONTEXT_API_VERSION) <= LooseVersion(versions)
                and module.params["context"]
            ):
                res = blade.post_nfs_export_policies_rules(
                    policy_names=[module.params["name"]],
                    rule=rule,
                    context_names=[module.params["context"]],
                )
            else:
                res = blade.post_nfs_export_policies_rules(
                    policy_names=[module.params["name"]],
                    rule=rule,
                )
            if res.status_code != 200:
                module.fail_json(
                    msg="Failed to rule for policy {0}. Error: {1}".format(
                        module.params["name"],
                        get_error_message(res),
                    )
                )
    module.exit_json(changed=changed)


def delete_os_policy(module, blade):
    """Delete Object Store Access Policy, Rule, or User

    If rule is provided then delete the rule if it exists.
    If user is provided then remove grant from user if granted.
    If no user or rule provided delete the whole policy.
    Cannot delete a policy with attached users, so delete all users
    if the force_delete option is selected.
    """

    changed = False
    versions = get_rest_api_version(blade)
    policy_name = module.params["account"] + "/" + module.params["name"]
    policy_delete = True
    if module.params["rule"]:
        policy_delete = False
        if (
            LooseVersion(CONTEXT_API_VERSION) <= LooseVersion(versions)
            and module.params["context"]
        ):
            res = blade.get_object_store_access_policies_rules(
                policy_names=[policy_name],
                names=[module.params["rule"]],
                context_names=[module.params["context"]],
            )
        else:
            res = blade.get_object_store_access_policies_rules(
                policy_names=[policy_name], names=[module.params["rule"]]
            )
        if res.status_code == 200 and res.total_item_count != 0:
            changed = True
            if not module.check_mode:
                if (
                    LooseVersion(CONTEXT_API_VERSION) <= LooseVersion(versions)
                    and module.params["context"]
                ):
                    res = blade.delete_object_store_access_policies_rules(
                        policy_names=[policy_name],
                        names=[module.params["rule"]],
                        context_names=[module.params["context"]],
                    )
                else:
                    res = blade.delete_object_store_access_policies_rules(
                        policy_names=[policy_name], names=[module.params["rule"]]
                    )
                if res.status_code != 200:
                    module.fail_json(
                        msg="Failed to delete users from policy {0}. Error: {1} - {2}".format(
                            policy_name, res.errors[0].context, get_error_message(res)
                        )
                    )

    if module.params["user"]:
        member_name = module.params["account"] + "/" + module.params["user"]
        policy_delete = False
        if (
            LooseVersion(CONTEXT_API_VERSION) <= LooseVersion(versions)
            and module.params["context"]
        ):
            res = blade.get_object_store_access_policies_object_store_users(
                policy_names=[policy_name],
                member_names=[member_name],
                context_names=[module.params["context"]],
            )
        else:
            res = blade.get_object_store_access_policies_object_store_users(
                policy_names=[policy_name], member_names=[member_name]
            )
        if res.status_code == 200 and res.total_item_count != 0:
            changed = True
            if not module.check_mode:
                member_name = module.params["account"] + "/" + module.params["user"]
                if (
                    LooseVersion(CONTEXT_API_VERSION) <= LooseVersion(versions)
                    and module.params["context"]
                ):
                    res = blade.delete_object_store_access_policies_object_store_users(
                        policy_names=[policy_name],
                        member_names=[member_name],
                        context_names=[module.params["context"]],
                    )
                else:
                    res = blade.delete_object_store_access_policies_object_store_users(
                        policy_names=[policy_name], member_names=[member_name]
                    )
                if res.status_code != 200:
                    module.fail_json(
                        msg="Failed to delete users from policy {0}. Error: {1} - {2}".format(
                            policy_name, res.errors[0].context, get_error_message(res)
                        )
                    )

    if policy_delete:
        if module.params["account"].lower() == "pure:policy":
            module.fail_json(msg="System-Wide policies cannot be deleted.")
        if (
            LooseVersion(CONTEXT_API_VERSION) <= LooseVersion(versions)
            and module.params["context"]
        ):
            policy_users = list(
                blade.get_object_store_access_policies_object_store_users(
                    policy_names=[policy_name], context_names=[module.params["context"]]
                ).items
            )
        else:
            policy_users = list(
                blade.get_object_store_access_policies_object_store_users(
                    policy_names=[policy_name]
                ).items
            )
        if len(policy_users) == 0:
            changed = True
            if not module.check_mode:
                if (
                    LooseVersion(CONTEXT_API_VERSION) <= LooseVersion(versions)
                    and module.params["context"]
                ):
                    res = blade.delete_object_store_access_policies(
                        names=[policy_name], context_names=[module.params["context"]]
                    )
                else:
                    res = blade.delete_object_store_access_policies(names=[policy_name])
                if res.status_code != 200:
                    module.fail_json(
                        msg="Failed to delete policy {0}. Error: {1}".format(
                            policy_name, get_error_message(res)
                        )
                    )
        else:
            if module.params["force_delete"]:
                changed = True
                if not module.check_mode:
                    for user in policy_users:
                        if (
                            LooseVersion(CONTEXT_API_VERSION) <= LooseVersion(versions)
                            and module.params["context"]
                        ):
                            res = blade.delete_object_store_access_policies_object_store_users(
                                member_names=[user.member.name],
                                policy_names=[policy_name],
                                context_names=[module.params["context"]],
                            )
                        else:
                            res = blade.delete_object_store_access_policies_object_store_users(
                                member_names=[user.member.name],
                                policy_names=[policy_name],
                            )
                        if res.status_code != 200:
                            module.fail_json(
                                msg="Failed to delete user {0} from policy {1}, "
                                "Error: {2}".format(
                                    user.member,
                                    policy_name,
                                    get_error_message(res),
                                )
                            )
                    if (
                        LooseVersion(CONTEXT_API_VERSION) <= LooseVersion(versions)
                        and module.params["context"]
                    ):
                        res = blade.delete_object_store_access_policies(
                            names=[policy_name],
                            context_names=[module.params["context"]],
                        )
                    else:
                        res = blade.delete_object_store_access_policies(
                            names=[policy_name]
                        )
                    if res.status_code != 200:
                        module.fail_json(
                            msg="Failed to delete policy {0}. Error: {1}".format(
                                policy_name, get_error_message(res)
                            )
                        )
            else:
                module.fail_json(
                    msg="Policy {0} cannot be deleted with connected users".format(
                        policy_name
                    )
                )
    module.exit_json(changed=changed)


def create_os_policy(module, blade):
    """Create Object Store Access Policy"""
    changed = True
    policy_name = module.params["account"] + "/" + module.params["name"]
    versions = get_rest_api_version(blade)
    if not module.check_mode:
        if (
            LooseVersion(CONTEXT_API_VERSION) <= LooseVersion(versions)
            and module.params["context"]
        ):
            res = blade.post_object_store_access_policies(
                names=[policy_name],
                policy=ObjectStoreAccessPolicyPost(description=module.params["desc"]),
                context_names=[module.params["context"]],
            )
        else:
            res = blade.post_object_store_access_policies(
                names=[policy_name],
                policy=ObjectStoreAccessPolicyPost(description=module.params["desc"]),
            )
        if res.status_code != 200:
            module.fail_json(
                msg="Failed to create access policy {0}.".format(policy_name)
            )
        if module.params["rule"]:
            if not module.params["actions"] or not module.params["object_resources"]:
                module.fail_json(
                    msg="Parameters `actions` and `object_resources` "
                    "are required to create a new rule"
                )
            conditions = PolicyRuleObjectAccessCondition(
                source_ips=module.params["source_ips"],
                s3_delimiters=module.params["s3_delimiters"],
                s3_prefixes=module.params["s3_prefixes"],
            )
            if LooseVersion(SMB_ENCRYPT_API_VERSION) <= LooseVersion(versions):
                rule = PolicyRuleObjectAccessPost(
                    actions=module.params["actions"],
                    resources=module.params["object_resources"],
                    conditions=conditions,
                    effect=_default(module.params["effect"], "allow"),
                )
            else:
                rule = PolicyRuleObjectAccessPost(
                    actions=module.params["actions"],
                    resources=module.params["object_resources"],
                    conditions=conditions,
                )
            if (
                LooseVersion(CONTEXT_API_VERSION) <= LooseVersion(versions)
                and module.params["context"]
            ):
                res = blade.post_object_store_access_policies_rules(
                    policy_names=policy_name,
                    names=[module.params["rule"]],
                    enforce_action_restrictions=module.params["ignore_enforcement"],
                    rule=rule,
                    context_names=[module.params["context"]],
                )
            else:
                res = blade.post_object_store_access_policies_rules(
                    policy_names=policy_name,
                    names=[module.params["rule"]],
                    enforce_action_restrictions=module.params["ignore_enforcement"],
                    rule=rule,
                )
            if res.status_code != 200:
                module.fail_json(
                    msg="Failed to create rule {0} to policy {1}. Error: {2}".format(
                        module.params["rule"], policy_name, get_error_message(res)
                    )
                )
        if module.params["user"]:
            member_name = module.params["account"] + "/" + module.params["user"]
            if (
                LooseVersion(CONTEXT_API_VERSION) <= LooseVersion(versions)
                and module.params["context"]
            ):
                res = blade.post_object_store_access_policies_object_store_users(
                    member_names=[member_name],
                    policy_names=[policy_name],
                    context_names=[module.params["context"]],
                )
            else:
                res = blade.post_object_store_access_policies_object_store_users(
                    member_names=[member_name], policy_names=[policy_name]
                )
            if res.status_code != 200:
                module.fail_json(
                    msg="Failed to add users to policy {0}. Error: {1} - {2}".format(
                        policy_name, res.errors[0].context, get_error_message(res)
                    )
                )
    module.exit_json(changed=changed)


def update_os_policy(module, blade):
    """Update Object Store Access Policy"""
    changed = False
    versions = get_rest_api_version(blade)
    policy_name = module.params["account"] + "/" + module.params["name"]
    if module.params["rule"]:
        if (
            LooseVersion(CONTEXT_API_VERSION) <= LooseVersion(versions)
            and module.params["context"]
        ):
            current_policy_rule = blade.get_object_store_access_policies_rules(
                policy_names=[policy_name],
                names=[module.params["rule"]],
                context_names=[module.params["context"]],
            )
        else:
            current_policy_rule = blade.get_object_store_access_policies_rules(
                policy_names=[policy_name], names=[module.params["rule"]]
            )
        if current_policy_rule.status_code != 200:
            changed = True
            if not module.check_mode:
                conditions = PolicyRuleObjectAccessCondition(
                    source_ips=module.params["source_ips"],
                    s3_delimiters=module.params["s3_delimiters"],
                    s3_prefixes=module.params["s3_prefixes"],
                )
                rule_kwargs = {
                    "actions": module.params["actions"],
                    "resources": module.params["object_resources"],
                    "conditions": conditions,
                }
                if LooseVersion(SMB_ENCRYPT_API_VERSION) <= LooseVersion(versions):
                    rule_kwargs["effect"] = _default(module.params["effect"], "allow")
                rule = PolicyRuleObjectAccessPost(**rule_kwargs)
                if (
                    LooseVersion(CONTEXT_API_VERSION) <= LooseVersion(versions)
                    and module.params["context"]
                ):
                    res = blade.post_object_store_access_policies_rules(
                        policy_names=policy_name,
                        names=[module.params["rule"]],
                        enforce_action_restrictions=module.params["ignore_enforcement"],
                        rule=rule,
                        context_names=[module.params["context"]],
                    )
                else:
                    res = blade.post_object_store_access_policies_rules(
                        policy_names=policy_name,
                        names=[module.params["rule"]],
                        enforce_action_restrictions=module.params["ignore_enforcement"],
                        rule=rule,
                    )
                if res.status_code != 200:
                    module.fail_json(
                        msg="Failed to create rule {0} in policy {1}. Error: {2}".format(
                            module.params["rule"], policy_name, get_error_message(res)
                        )
                    )
        else:
            old_policy_rule = list(current_policy_rule.items)[0]
            # Lists are compared order-insensitively (the array may return
            # them in any order) and fields the task did not set keep their
            # current value, including the rule effect.
            current_rule = {
                "actions": _sorted_or_none(old_policy_rule.actions),
                "resources": _sorted_or_none(old_policy_rule.resources),
                "ips": _sorted_or_none(
                    getattr(old_policy_rule.conditions, "source_ips", None)
                ),
                "prefixes": _sorted_or_none(
                    getattr(old_policy_rule.conditions, "s3_prefixes", None)
                ),
                "delimiters": _sorted_or_none(
                    getattr(old_policy_rule.conditions, "s3_delimiters", None)
                ),
                "effect": getattr(old_policy_rule, "effect", None),
            }
            new_rule = {
                "actions": _sorted_or_none(module.params["actions"])
                or current_rule["actions"],
                "resources": _sorted_or_none(module.params["object_resources"])
                or current_rule["resources"],
                "ips": _sorted_or_none(module.params["source_ips"])
                or current_rule["ips"],
                "prefixes": _sorted_or_none(module.params["s3_prefixes"])
                or current_rule["prefixes"],
                "delimiters": _sorted_or_none(module.params["s3_delimiters"])
                or current_rule["delimiters"],
                "effect": _default(module.params["effect"], current_rule["effect"]),
            }
            if current_rule != new_rule:
                changed = True
                if not module.check_mode:
                    conditions = PolicyRuleObjectAccessCondition(
                        source_ips=new_rule["ips"],
                        s3_prefixes=new_rule["prefixes"],
                        s3_delimiters=new_rule["delimiters"],
                    )
                    rule_kwargs = {
                        "actions": new_rule["actions"],
                        "resources": new_rule["resources"],
                        "conditions": conditions,
                    }
                    if new_rule["effect"] is not None:
                        rule_kwargs["effect"] = new_rule["effect"]
                    # PolicyRuleObjectAccess is the response model and drops
                    # `effect` from the request body; the PATCH body model
                    # (as the SDK itself types it) is PolicyRuleObjectAccessPost.
                    rule = PolicyRuleObjectAccessPost(**rule_kwargs)
                    if (
                        LooseVersion(CONTEXT_API_VERSION) <= LooseVersion(versions)
                        and module.params["context"]
                    ):
                        res = blade.patch_object_store_access_policies_rules(
                            policy_names=[policy_name],
                            names=[module.params["rule"]],
                            rule=rule,
                            enforce_action_restrictions=module.params[
                                "ignore_enforcement"
                            ],
                            context_names=[module.params["context"]],
                        )
                    else:
                        res = blade.patch_object_store_access_policies_rules(
                            policy_names=[policy_name],
                            names=[module.params["rule"]],
                            rule=rule,
                            enforce_action_restrictions=module.params[
                                "ignore_enforcement"
                            ],
                        )
                    if res.status_code != 200:
                        module.fail_json(
                            msg="Failed to update rule {0} in policy {1}. Error: {2}".format(
                                module.params["rule"],
                                policy_name,
                                get_error_message(res),
                            )
                        )
    if module.params["user"]:
        member_name = module.params["account"] + "/" + module.params["user"]
        if (
            LooseVersion(CONTEXT_API_VERSION) <= LooseVersion(versions)
            and module.params["context"]
        ):
            res = blade.get_object_store_access_policies_object_store_users(
                policy_names=[policy_name],
                member_names=[member_name],
                context_names=[module.params["context"]],
            )
        else:
            res = blade.get_object_store_access_policies_object_store_users(
                policy_names=[policy_name], member_names=[member_name]
            )
        if res.status_code != 200 or (
            res.status_code == 200 and res.total_item_count == 0
        ):
            changed = True
            if not module.check_mode:
                if (
                    LooseVersion(CONTEXT_API_VERSION) <= LooseVersion(versions)
                    and module.params["context"]
                ):
                    res = blade.post_object_store_access_policies_object_store_users(
                        member_names=[member_name],
                        policy_names=[policy_name],
                        context_names=[module.params["context"]],
                    )
                else:
                    res = blade.post_object_store_access_policies_object_store_users(
                        member_names=[member_name], policy_names=[policy_name]
                    )
                if res.status_code != 200:
                    module.fail_json(
                        msg="Failed to add user {0} to policy {1}. Error: {2}".format(
                            member_name, policy_name, get_error_message(res)
                        )
                    )
    module.exit_json(changed=changed)


def copy_os_policy_rule(module, blade):
    """Copy an existing policy rule to a new policy"""
    changed = True
    versions = get_rest_api_version(blade)
    policy_name = module.params["account"] + "/" + module.params["name"]
    if not module.params["target_rule"]:
        module.params["target_rule"] = module.params["rule"]
    if (
        LooseVersion(CONTEXT_API_VERSION) <= LooseVersion(versions)
        and module.params["context"]
    ):
        res = blade.get_object_store_access_policies_rules(
            policy_names=[module.params["target"]],
            names=[module.params["target_rule"]],
            context_names=[module.params["context"]],
        )
    else:
        res = blade.get_object_store_access_policies_rules(
            policy_names=[module.params["target"]], names=[module.params["target_rule"]]
        )
    if res.status_code == 200:
        module.fail_json(
            msg="Target rule {0} already exists in policy {1}".format(
                module.params["target_rule"], policy_name
            )
        )
    if (
        LooseVersion(CONTEXT_API_VERSION) <= LooseVersion(versions)
        and module.params["context"]
    ):
        current_rule = list(
            blade.get_object_store_access_policies_rules(
                policy_names=[policy_name],
                names=[module.params["rule"]],
                context_names=[module.params["context"]],
            ).items
        )[0]
    else:
        current_rule = list(
            blade.get_object_store_access_policies_rules(
                policy_names=[policy_name], names=[module.params["rule"]]
            ).items
        )[0]
    if not module.check_mode:
        conditions = PolicyRuleObjectAccessCondition(
            source_ips=current_rule.conditions.source_ips,
            s3_delimiters=current_rule.conditions.s3_delimiters,
            s3_prefixes=current_rule.conditions.s3_prefixes,
        )
        rule_kwargs = {
            "actions": current_rule.actions,
            "resources": current_rule.resources,
            "conditions": conditions,
        }
        if getattr(current_rule, "effect", None) is not None:
            # Keep a deny rule a deny rule when it is copied.
            rule_kwargs["effect"] = current_rule.effect
        rule = PolicyRuleObjectAccessPost(**rule_kwargs)
        if (
            LooseVersion(CONTEXT_API_VERSION) <= LooseVersion(versions)
            and module.params["context"]
        ):
            res = blade.post_object_store_access_policies_rules(
                policy_names=module.params["target"],
                names=[module.params["target_rule"]],
                enforce_action_restrictions=module.params["ignore_enforcement"],
                rule=rule,
                context_names=[module.params["context"]],
            )
        else:
            res = blade.post_object_store_access_policies_rules(
                policy_names=module.params["target"],
                names=[module.params["target_rule"]],
                enforce_action_restrictions=module.params["ignore_enforcement"],
                rule=rule,
            )
        if res.status_code != 200:
            module.fail_json(
                msg="Failed to copy rule {0} from policy {1} to policy {2}. "
                "Error: {3}".format(
                    module.params["rule"],
                    policy_name,
                    module.params["target"],
                    get_error_message(res),
                )
            )
    module.exit_json(changed=changed)


def delete_snap_policy(module, blade):
    """Delete snapshot policy

    If any rule parameters are provided then delete any rules that match
    all of the parameters provided.
    If no rule parameters are provided delete the entire policy
    """

    changed = False
    versions = get_rest_api_version(blade)
    rule_delete = False
    if (
        module.params["at"]
        or module.params["every"]
        or module.params["timezone"]
        or module.params["keep_for"]
    ):
        rule_delete = True
    if rule_delete:
        if (
            LooseVersion(CONTEXT_API_VERSION) <= LooseVersion(versions)
            and module.params["context"]
        ):
            current_rules = list(
                blade.get_policies(
                    names=[module.params["name"]],
                    context_names=[module.params["context"]],
                ).items
            )[0].rules
        else:
            current_rules = list(
                blade.get_policies(names=[module.params["name"]]).items
            )[0].rules
        for rule in current_rules:
            current_rule = {
                "at": rule.at,
                "every": rule.every,
                "keep_for": rule.keep_for,
                "time_zone": rule.time_zone,
            }
            if not module.params["at"]:
                delete_at = rule.at
            else:
                delete_at = time_to_milliseconds(module.params["at"])
            if module.params["keep_for"]:
                delete_keep_for = module.params["keep_for"]
            else:
                delete_keep_for = int(rule.keep_for / 1000)
            if module.params["every"]:
                delete_every = module.params["every"]
            else:
                delete_every = int(rule.every / 1000)
            if not module.params["timezone"]:
                delete_tz = rule.time_zone
            else:
                delete_tz = module.params["timezone"]
            delete_rule = {
                "at": delete_at,
                "every": delete_every * 1000,
                "keep_for": delete_keep_for * 1000,
                "time_zone": delete_tz,
            }
            if current_rule == delete_rule:
                changed = True
                attr = SnapshotPolicyPatch(remove_rules=[delete_rule])
                if not module.check_mode:
                    if (
                        LooseVersion(CONTEXT_API_VERSION) <= LooseVersion(versions)
                        and module.params["context"]
                    ):
                        res = blade.patch_policies(
                            destroy_snapshots=module.params["destroy_snapshots"],
                            names=[module.params["name"]],
                            policy=attr,
                            context_names=[module.params["context"]],
                        )
                    else:
                        res = blade.patch_policies(
                            destroy_snapshots=module.params["destroy_snapshots"],
                            names=[module.params["name"]],
                            policy=attr,
                        )
                    if res.status_code != 200:
                        module.fail_json(
                            msg="Failed to delete policy rule {0}. Error: {1}".format(
                                module.params["name"], get_error_message(res)
                            )
                        )
    else:
        changed = True
        if not module.check_mode:
            if (
                LooseVersion(CONTEXT_API_VERSION) <= LooseVersion(versions)
                and module.params["context"]
            ):
                res = blade.delete_policies(
                    names=[module.params["name"]],
                    context_names=[module.params["context"]],
                )
            else:
                res = blade.delete_policies(names=[module.params["name"]])
            if res.status_code != 200:
                module.fail_json(
                    msg="Failed to delete policy {0}. Error: {1}".format(
                        module.params["name"], get_error_message(res)
                    )
                )
    module.exit_json(changed=changed)


def create_snap_policy(module, blade):
    """Create snapshot policy"""
    changed = True
    versions = get_rest_api_version(blade)
    if (
        module.params["keep_for"]
        and not module.params["every"]
        or module.params["every"]
        and not module.params["keep_for"]
    ):
        module.fail_json(msg="`keep_for` and `every` are required.")
    if module.params["timezone"] and not module.params["at"]:
        module.fail_json(msg="`timezone` requires `at` to be provided.")
    if module.params["at"] and not module.params["every"]:
        module.fail_json(msg="`at` requires `every` to be provided.")

    if not module.check_mode:
        if module.params["at"] and module.params["every"]:
            if not module.params["every"] % 86400 == 0:
                module.fail_json(
                    msg="At time can only be set if every value is a multiple of 86400"
                )
            if not module.params["timezone"]:
                module.params["timezone"] = _get_local_tz(module)
                if module.params["timezone"] not in pytz.all_timezones_set:
                    module.fail_json(
                        msg="Timezone {0} is not valid".format(
                            module.params["timezone"]
                        )
                    )
        if not module.params["keep_for"]:
            module.params["keep_for"] = 0
        if not module.params["every"]:
            module.params["every"] = 0
        if module.params["keep_for"] < module.params["every"]:
            module.fail_json(
                msg="Retention period cannot be less than snapshot interval."
            )
        if module.params["at"] and not module.params["timezone"]:
            module.params["timezone"] = _get_local_tz(module)
            if module.params["timezone"] not in set(pytz.all_timezones_set):
                module.fail_json(
                    msg="Timezone {0} is not valid".format(module.params["timezone"])
                )

        if module.params["keep_for"]:
            if not 300 <= module.params["keep_for"] <= 34560000:
                module.fail_json(
                    msg="keep_for parameter is out of range (300 to 34560000)"
                )
            if not 300 <= module.params["every"] <= 34560000:
                module.fail_json(
                    msg="every parameter is out of range (300 to 34560000)"
                )
            if module.params["at"]:
                attr = Policy(
                    enabled=module.params["enabled"],
                    rules=[
                        PolicyRule(
                            keep_for=module.params["keep_for"] * 1000,
                            every=module.params["every"] * 1000,
                            at=time_to_milliseconds(module.params["at"]),
                            time_zone=module.params["timezone"],
                        )
                    ],
                )
            else:
                attr = Policy(
                    enabled=module.params["enabled"],
                    rules=[
                        PolicyRule(
                            keep_for=module.params["keep_for"] * 1000,
                            every=module.params["every"] * 1000,
                        )
                    ],
                )
        else:
            attr = Policy(enabled=module.params["enabled"])
        if (
            LooseVersion(CONTEXT_API_VERSION) <= LooseVersion(versions)
            and module.params["context"]
        ):
            res = blade.post_policies(
                names=[module.params["name"]],
                policy=attr,
                context_names=[module.params["context"]],
            )
        else:
            res = blade.post_policies(names=[module.params["name"]], policy=attr)
        if res.status_code != 200:
            module.fail_json(
                msg="Failed to create snapshot policy {0}. Error: {1}".format(
                    module.params["name"], get_error_message(res)
                )
            )
        if module.params["filesystem"]:
            for filesystem in module.params["filesystem"]:
                if (
                    LooseVersion(CONTEXT_API_VERSION) <= LooseVersion(versions)
                    and module.params["context"]
                ):
                    res = blade.get_file_systems(
                        names=[filesystem],
                        destroyed=False,
                        context_names=[module.params["context"]],
                    )
                else:
                    res = blade.get_file_systems(names=[filesystem], destroyed=False)
                if res.status_code != 200:
                    module.fail_json(
                        msg="Filesystems to assign to {0} does not "
                        "exist, or is deleted.".format(module.params["name"])
                    )
                if (
                    LooseVersion(CONTEXT_API_VERSION) <= LooseVersion(versions)
                    and module.params["context"]
                ):
                    res = blade.post_policies_file_systems(
                        policy_names=[module.params["name"]],
                        member_names=[filesystem],
                        context_names=[module.params["context"]],
                    )
                else:
                    res = blade.post_policies_file_systems(
                        policy_names=[module.params["name"]],
                        member_names=[filesystem],
                    )
                if res.status_code != 200:
                    module.fail_json(
                        msg="Failed to add filesystem {0} to "
                        "policy {1}. Error: {2}.".format(
                            filesystem, module.params["name"], get_error_message(res)
                        )
                    )
        if module.params["replica_link"]:
            repl_link = []
            for link in module.params["replica_link"]:
                if (
                    LooseVersion(CONTEXT_API_VERSION) <= LooseVersion(versions)
                    and module.params["context"]
                ):
                    res = blade.get_file_system_replica_links(
                        local_file_system_names=[link],
                        context_names=[module.params["context"]],
                    )
                else:
                    res = blade.get_file_system_replica_links(
                        local_file_system_names=[link]
                    )
                if res.status_code != 200:
                    module.fail_json(
                        msg="Replica Link {0} does not exist.".format(link)
                    )
                else:
                    repl_link = list(res.items)[0]
                if (
                    LooseVersion(CONTEXT_API_VERSION) <= LooseVersion(versions)
                    and module.params["context"]
                ):
                    res = blade.post_policies_file_system_replica_links(
                        policy_names=[module.params["name"]],
                        local_file_system_names=[link],
                        remote_names=[repl_link.remote.name],
                        context_names=[module.params["context"]],
                    )
                else:
                    res = blade.post_policies_file_system_replica_links(
                        policy_names=[module.params["name"]],
                        local_file_system_names=[link],
                        remote_names=[repl_link.remote.name],
                    )
                if res.status_code != 200:
                    module.fail_json(
                        msg="Failed to connect filesystem replicsa link {0} to policy {1}. "
                        "Error: {2}".format(
                            link, module.params["name"], get_error_message(res)
                        )
                    )
    module.exit_json(changed=changed)


def update_snap_policy(module, blade):
    """Update snapshot policy

    Add new rules to the policy using this function.
    Should it be necessary to modify an existing rule these are the rules:

    Due to the 'best fit' nature of Purity we only add new rulkes in this function.
    If you trying to update an existing rule, then this should be done by deleting
    the current rule and then adding the new rule.

    Purity may recover some snapshots as long as the add happens before the eradication delay
    (typically 24h) causes the snapshots to be eradicated.
    """

    changed = False
    versions = get_rest_api_version(blade)
    if (
        module.params["keep_for"]
        and not module.params["every"]
        or module.params["every"]
        and not module.params["keep_for"]
    ):
        module.fail_json(msg="`keep_for` and `every` are required.")
    if module.params["timezone"] and not module.params["at"]:
        module.fail_json(msg="`timezone` requires `at` to be provided.")
    if module.params["at"] and not module.params["every"]:
        module.fail_json(msg="`at` requires `every` to be provided.")
    ctx = _context_kwargs(module, versions)
    current_policy = list(
        blade.get_policies(names=[module.params["name"]], **ctx).items
    )[0]
    current_rules = current_policy.rules or []
    # Rules and the enabled state are only reconciled for state=present; a
    # state=absent call reaches here to detach members and must not re-enable
    # the policy through the default of `enabled`.
    if module.params["state"] == "present":
        # Snapshot policies carry a single schedule rule. Fall back to the
        # existing rule's value for any field the task did not specify, so a
        # partial update (e.g. changing only keep_for) does not reset the
        # other fields to zero.
        existing_rule = current_rules[0] if current_rules else None
        if module.params["at"]:
            new_at = time_to_milliseconds(module.params["at"])
        elif existing_rule is not None:
            new_at = existing_rule.at
        else:
            new_at = None
        if module.params["keep_for"]:
            new_keep_for = module.params["keep_for"]
        elif existing_rule is not None:
            new_keep_for = int(existing_rule.keep_for / 1000)
        else:
            new_keep_for = 0
        if module.params["every"]:
            new_every = module.params["every"]
        elif existing_rule is not None:
            new_every = int(existing_rule.every / 1000)
        else:
            new_every = 0
        if module.params["timezone"]:
            new_tz = module.params["timezone"]
        elif existing_rule is not None:
            new_tz = existing_rule.time_zone
        else:
            new_tz = None
        new_rule = {
            "at": new_at,
            "every": new_every * 1000,
            "keep_for": new_keep_for * 1000,
            "time_zone": new_tz,
        }
        # Only add the rule when no existing rule already matches it, so a
        # second rule is not re-added on every run.
        rule_exists = any(
            {
                "at": rule.at,
                "every": rule.every,
                "keep_for": rule.keep_for,
                "time_zone": rule.time_zone,
            }
            == new_rule
            for rule in current_rules
        )
        add_rule = bool(new_every or new_keep_for) and not rule_exists
        # The enabled state is reconciled on its own, not only when a rule is
        # added.
        enable_change = current_policy.enabled != module.params["enabled"]
        if add_rule or enable_change:
            changed = True
            if not module.check_mode:
                if add_rule:
                    if new_at is not None and new_every % 86400 != 0:
                        module.fail_json(
                            msg="At time can only be set if every value is a "
                            "multiple of 86400"
                        )
                    if new_at is not None and not new_tz:
                        new_tz = _get_local_tz(module)
                        if new_tz not in pytz.all_timezones_set:
                            module.fail_json(
                                msg="Timezone {0} is not valid".format(new_tz)
                            )
                    if new_keep_for < new_every:
                        module.fail_json(
                            msg="Retention period cannot be less than snapshot interval."
                        )
                    if not 300 <= new_keep_for <= 34560000:
                        module.fail_json(
                            msg="keep_for parameter is out of range (300 to 34560000)"
                        )
                    if not 300 <= new_every <= 34560000:
                        module.fail_json(
                            msg="every parameter is out of range (300 to 34560000)"
                        )
                    rule_kwargs = {
                        "keep_for": new_keep_for * 1000,
                        "every": new_every * 1000,
                    }
                    if new_at is not None:
                        rule_kwargs["at"] = new_at
                        rule_kwargs["time_zone"] = new_tz
                    attr = SnapshotPolicyPatch(
                        enabled=module.params["enabled"],
                        add_rules=[PolicyRule(**rule_kwargs)],
                    )
                else:
                    attr = SnapshotPolicyPatch(enabled=module.params["enabled"])
                res = blade.patch_policies(
                    names=[module.params["name"]], policy=attr, **ctx
                )
                if res.status_code != 200:
                    module.fail_json(
                        msg="Failed to update snapshot policy {0}. Error: {1}".format(
                            module.params["name"], get_error_message(res)
                        )
                    )

    if module.params["filesystem"]:
        current_filesystems = []
        if (
            LooseVersion(CONTEXT_API_VERSION) <= LooseVersion(versions)
            and module.params["context"]
        ):
            policy_fs_details = list(
                blade.get_policies_file_systems(
                    policy_names=[module.params["name"]],
                    context_names=[module.params["context"]],
                ).items
            )
        else:
            policy_fs_details = list(
                blade.get_policies_file_systems(
                    policy_names=[module.params["name"]]
                ).items
            )
        for member in policy_fs_details:
            current_filesystems.append(member.member.name)
        if module.params["state"] == "present":
            difference_set = [
                item
                for item in module.params["filesystem"]
                if item not in current_filesystems
            ]
            for new_fs in difference_set:
                changed = True
                if (
                    LooseVersion(CONTEXT_API_VERSION) <= LooseVersion(versions)
                    and module.params["context"]
                ):
                    res = blade.get_file_systems(
                        names=[new_fs],
                        destroyed=False,
                        context_names=[module.params["context"]],
                    )
                else:
                    res = blade.get_file_systems(names=[new_fs], destroyed=False)
                if res.status_code != 200:
                    module.fail_json(
                        msg="Filesystem {0} to assign to {1} does not "
                        "exist, or is deleted.".format(new_fs, module.params["name"])
                    )
                if (
                    LooseVersion(CONTEXT_API_VERSION) <= LooseVersion(versions)
                    and module.params["context"]
                ):
                    res = blade.post_policies_file_systems(
                        policy_names=[module.params["name"]],
                        member_names=[new_fs],
                        context_names=[module.params["context"]],
                    )
                else:
                    res = blade.post_policies_file_systems(
                        policy_names=[module.params["name"]],
                        member_names=[new_fs],
                    )
                if res.status_code != 200:
                    module.fail_json(
                        msg="Failed to add filesystem {0} to "
                        "policy {1}. Error: {2}.".format(
                            new_fs, module.params["name"], get_error_message(res)
                        )
                    )
        else:
            for old_fs in module.params["filesystem"]:
                if old_fs in current_filesystems:
                    changed = True
                    if (
                        LooseVersion(CONTEXT_API_VERSION) <= LooseVersion(versions)
                        and module.params["context"]
                    ):
                        res = blade.delete_policies_file_systems(
                            policy_names=[module.params["name"]],
                            member_names=[old_fs],
                            context_names=[module.params["context"]],
                        )
                    else:
                        res = blade.delete_policies_file_systems(
                            policy_names=[module.params["name"]],
                            member_names=[old_fs],
                        )
                    if res.status_code != 200:
                        module.fail_json(
                            msg="Failed to remove filesystem {0} from "
                            "policy {1}. Error: {2}.".format(
                                old_fs, module.params["name"], get_error_message(res)
                            )
                        )
    if module.params["replica_link"]:
        current_rls = []
        if (
            LooseVersion(CONTEXT_API_VERSION) <= LooseVersion(versions)
            and module.params["context"]
        ):
            policy_rl_details = list(
                blade.get_policies_file_system_replica_links(
                    policy_names=[module.params["name"]],
                    context_names=[module.params["context"]],
                ).items
            )
        else:
            policy_rl_details = list(
                blade.get_policies_file_system_replica_links(
                    policy_names=[module.params["name"]]
                ).items
            )
        for member in policy_rl_details:
            current_rls.append(member.member.name)
        if module.params["state"] == "present":
            difference_set = [
                item
                for item in module.params["replica_link"]
                if item not in current_rls
            ]
            for new_rl in difference_set:
                changed = True
                if (
                    LooseVersion(CONTEXT_API_VERSION) <= LooseVersion(versions)
                    and module.params["context"]
                ):
                    res = blade.get_file_systems_replica_links(
                        names=[new_rl], context_names=[module.params["context"]]
                    )
                else:
                    res = blade.get_file_systems_replica_links(names=[new_rl])
                if res.status_code != 200:
                    module.fail_json(
                        msg="Replica link {0} to assign to {1} does not "
                        "exist, or is deleted.".format(new_rl, module.params["name"])
                    )
                if (
                    LooseVersion(CONTEXT_API_VERSION) <= LooseVersion(versions)
                    and module.params["context"]
                ):
                    res = blade.post_policies_file_system_replica_links(
                        policy_names=[module.params["name"]],
                        member_names=[new_rl],
                        context_names=[module.params["context"]],
                    )
                else:
                    res = blade.post_policies_file_system_replica_links(
                        policy_names=[module.params["name"]],
                        member_names=[new_rl],
                    )
                if res.status_code != 200:
                    module.fail_json(
                        msg="Failed to add replica link {0} to "
                        "policy {1}. Error: {2}.".format(
                            new_rl, module.params["name"], get_error_message(res)
                        )
                    )
        else:
            for old_rl in module.params["replica_link"]:
                if old_rl in current_rls:
                    changed = True
                    if (
                        LooseVersion(CONTEXT_API_VERSION) <= LooseVersion(versions)
                        and module.params["context"]
                    ):
                        res = blade.delete_policies_file_system_replica_links(
                            policy_names=[module.params["name"]],
                            member_names=[old_rl],
                            context_names=[module.params["context"]],
                        )
                    else:
                        res = blade.delete_policies_file_system_replica_links(
                            policy_names=[module.params["name"]],
                            member_names=[old_rl],
                        )
                    if res.status_code != 200:
                        module.fail_json(
                            msg="Failed to remove replica link {0} from "
                            "policy {1}. Error: {2}.".format(
                                old_rl, module.params["name"], get_error_message(res)
                            )
                        )

    module.exit_json(changed=changed)


def rename_snap_policy(module, blade):
    """Rename snapshot policy"""

    changed = True
    versions = get_rest_api_version(blade)
    if not module.check_mode:
        # Policy keeps `name` in the request body (SnapshotPolicyPatch strips
        # it as read-only), so this is the only way to ask for a rename.
        res = blade.patch_policies(
            names=[module.params["name"]],
            policy=Policy(name=module.params["rename"]),
            **_context_kwargs(module, versions),
        )
        if res.status_code != 200:
            module.fail_json(
                msg="Failed to rename snapshot policy {0} to {1}. Error: {2}".format(
                    module.params["name"],
                    module.params["rename"],
                    get_error_message(res),
                )
            )
        # Purity//FB (verified on 4.6.4, REST 2.21) answers 200 to a body of
        # {"name": ...} on PATCH /policies but silently keeps the old name,
        # whether addressed by names or ids: snapshot policies have no rename
        # on the array. Check the result rather than report a rename that
        # never happened.
        res = blade.get_policies(
            names=[module.params["rename"]], **_context_kwargs(module, versions)
        )
        if res.status_code != 200:
            module.fail_json(
                msg="Failed to rename snapshot policy {0} to {1}: the array "
                "accepted the request but kept the old name. Purity//FB REST "
                "{2} does not support renaming snapshot policies.".format(
                    module.params["name"], module.params["rename"], versions
                )
            )
    module.exit_json(changed=changed)


def _copy_rule_policy(module, blade, source, versions, ctx):
    """Create the target of a copy for NFS and SMB policies.

    The target is created empty, disabled if the source is disabled, and then
    every source rule is re-created on it in evaluation order. Returns the
    last SDK response so the caller can report a failure.
    """
    policy_type = module.params["policy_type"]
    target = module.params["target"]
    if policy_type == "nfs":
        poster = blade.post_nfs_export_policies
        patcher = blade.patch_nfs_export_policies
        rule_getter = blade.get_nfs_export_policies_rules
        rule_poster = blade.post_nfs_export_policies_rules
        policy_model = NfsExportPolicy
    elif policy_type == "smb_client":
        poster = blade.post_smb_client_policies
        patcher = blade.patch_smb_client_policies
        rule_getter = blade.get_smb_client_policies_rules
        rule_poster = blade.post_smb_client_policies_rules
        policy_model = SmbClientPolicy
    else:
        poster = blade.post_smb_share_policies
        patcher = blade.patch_smb_share_policies
        rule_getter = blade.get_smb_share_policies_rules
        rule_poster = blade.post_smb_share_policies_rules
        policy_model = SmbSharePolicy
    res = poster(names=[target], **ctx)
    if res.status_code != 200:
        return res
    if not source.enabled:
        res = patcher(names=[target], policy=policy_model(enabled=False), **ctx)
        if res.status_code != 200:
            return res
    rules_res = rule_getter(policy_names=[source.name], **ctx)
    rules = list(rules_res.items) if rules_res.status_code == 200 else []
    if policy_type != "smb_share":
        # Client rules are evaluated in index order; share rules have no index.
        rules.sort(key=lambda rule: rule.index)
    for rule in rules:
        if policy_type == "nfs":
            new_rule = _nfs_rule(
                versions,
                client=rule.client,
                permission=rule.permission,
                access=getattr(rule, "access", None),
                anonuid=_anon_id(getattr(rule, "anonuid", None)),
                anongid=_anon_id(getattr(rule, "anongid", None)),
                fileid_32bit=rule.fileid_32bit,
                atime=rule.atime,
                secure=rule.secure,
                security=rule.security,
            )
        elif policy_type == "smb_client":
            if LooseVersion(SMB_ENCRYPT_API_VERSION) <= LooseVersion(versions):
                new_rule = SmbClientPolicyRule(
                    client=rule.client,
                    permission=rule.permission,
                    encryption=rule.encryption,
                )
            else:
                new_rule = SmbClientPolicyRule(
                    client=rule.client, permission=rule.permission
                )
        else:
            new_rule = SmbSharePolicyRule(
                principal=rule.principal,
                read=rule.read,
                change=rule.change,
                full_control=rule.full_control,
            )
        res = rule_poster(policy_names=[target], rule=new_rule, **ctx)
        if res.status_code != 200:
            return res
    return res


def copy_policy(module, blade, source):
    """Copy a snapshot, NFS, SMB or WORM policy to a new policy I(target).

    The REST API has no copy operation for these policy types, so the target
    is created with the source settings and the source rules are re-created
    on it. Network access policies cannot be created, so they cannot be
    copied either.
    """
    changed = True
    versions = get_rest_api_version(blade)
    ctx = _context_kwargs(module, versions)
    policy_type = module.params["policy_type"]
    target = module.params["target"]
    if not target:
        module.fail_json(msg="`target` is required when state is copy")
    if not source:
        module.fail_json(msg="Policy {0} does not exist".format(module.params["name"]))
    getters = {
        "snapshot": blade.get_policies,
        "nfs": blade.get_nfs_export_policies,
        "smb_client": blade.get_smb_client_policies,
        "smb_share": blade.get_smb_share_policies,
        "worm": blade.get_worm_data_policies,
    }
    if policy_type not in getters:
        module.fail_json(
            msg="Copy is not supported for {0} policies".format(policy_type)
        )
    if getters[policy_type](names=[target], **ctx).status_code == 200:
        module.fail_json(msg="Target policy {0} already exists".format(target))
    if not module.check_mode:
        if policy_type == "snapshot":
            policy_kwargs = {"enabled": source.enabled}
            rules = []
            for rule in source.rules or []:
                rule_kwargs = {"every": rule.every, "keep_for": rule.keep_for}
                if rule.at is not None:
                    rule_kwargs["at"] = rule.at
                    rule_kwargs["time_zone"] = rule.time_zone
                rules.append(PolicyRule(**rule_kwargs))
            if rules:
                policy_kwargs["rules"] = rules
            res = blade.post_policies(
                names=[target], policy=Policy(**policy_kwargs), **ctx
            )
        elif policy_type == "worm":
            res = blade.post_worm_data_policies(
                names=[target],
                policy=WormDataPolicy(
                    enabled=source.enabled,
                    mode=source.mode,
                    min_retention=source.min_retention,
                    max_retention=source.max_retention,
                    default_retention=source.default_retention,
                    retention_lock=source.retention_lock,
                ),
                **ctx,
            )
        else:
            res = _copy_rule_policy(module, blade, source, versions, ctx)
        if res.status_code != 200:
            module.fail_json(
                msg="Failed to copy policy {0} to {1}. Error: {2}".format(
                    module.params["name"], target, get_error_message(res)
                )
            )
    module.exit_json(changed=changed)


def main():
    argument_spec = purefb_argument_spec()
    argument_spec.update(
        dict(
            state=dict(
                type="str", default="present", choices=["absent", "present", "copy"]
            ),
            policy_type=dict(
                type="str",
                default="snapshot",
                choices=[
                    "snapshot",
                    "access",
                    "nfs",
                    "smb_share",
                    "smb_client",
                    "network",
                    "worm",
                ],
            ),
            enabled=dict(type="bool", default=True),
            timezone=dict(type="str"),
            name=dict(type="str"),
            at=dict(type="str"),
            every=dict(type="int"),
            keep_for=dict(type="int"),
            filesystem=dict(type="list", elements="str"),
            replica_link=dict(type="list", elements="str"),
            account=dict(type="str"),
            target=dict(type="str"),
            target_rule=dict(type="str"),
            rename=dict(type="str"),
            rule=dict(type="str"),
            user=dict(type="str"),
            effect=dict(type="str", choices=["allow", "deny"]),
            actions=dict(
                type="list",
                elements="str",
                choices=[
                    "s3:*",
                    "s3:AbortMultipartUpload",
                    "s3:BypassGovernanceRetention",
                    "s3:CreateBucket",
                    "s3:DeleteBucket",
                    "s3:DeleteObject",
                    "s3:DeleteObjectVersion",
                    "s3:ExtendSafemodeRetentionPeriod",
                    "s3:GetBucketAcl",
                    "s3:GetBucketLocation",
                    "s3:GetBucketVersioning",
                    "s3:GetLifecycleConfiguration",
                    "s3:GetObject",
                    "s3:GetObjectAcl",
                    "s3:GetObjectLegalHold",
                    "s3:GetObjectLockConfiguration",
                    "s3:GetObjectRetention",
                    "s3:GetObjectTagging",
                    "s3:GetObjectVersion",
                    "s3:GetObjectVersionTagging",
                    "s3:ListAllMyBuckets",
                    "s3:ListBucket",
                    "s3:ListBucketMultipartUploads",
                    "s3:ListBucketVersions",
                    "s3:ListMultipartUploadParts",
                    "s3:PutBucketVersioning",
                    "s3:PutLifecycleConfiguration",
                    "s3:PutObject",
                    "s3:PutObjectLegalHold",
                    "s3:PutObjectLockConfiguration",
                    "s3:PutObjectRetention",
                    "s3:ResolveSafemodeConflicts",
                ],
            ),
            object_resources=dict(type="list", elements="str"),
            source_ips=dict(type="list", elements="str"),
            s3_prefixes=dict(type="list", elements="str"),
            s3_delimiters=dict(type="list", elements="str"),
            ignore_enforcement=dict(type="bool", default=True),
            force_delete=dict(type="bool", default=False),
            # Rule fields carry no argspec default so that an update only
            # touches what the task set; the defaults are applied on create.
            access=dict(
                type="str",
                choices=["root-squash", "all-squash", "no-squash"],
            ),
            anonuid=dict(type="int"),
            anongid=dict(type="int"),
            atime=dict(type="bool"),
            client=dict(type="str"),
            fileid_32bit=dict(type="bool"),
            permission=dict(type="str", choices=["rw", "ro"]),
            secure=dict(type="bool"),
            destroy_snapshots=dict(type="bool", default=False),
            security=dict(
                type="list",
                elements="str",
                choices=["sys", "krb5", "krb5i", "krb5p"],
            ),
            before_rule=dict(type="int"),
            principal=dict(type="str"),
            change=dict(type="str", choices=["deny", "allow", ""]),
            read=dict(type="str", choices=["deny", "allow", ""]),
            full_control=dict(type="str", choices=["deny", "allow", ""]),
            smb_encryption=dict(
                type="str",
                choices=["disabled", "optional", "required"],
            ),
            desc=dict(type="str", default=""),
            interfaces=dict(
                type="list",
                elements="str",
                choices=[
                    "management-ssh",
                    "management-rest-api",
                    "management-web-ui",
                    "snmp",
                    "local-network-superuser-password-access",
                ],
            ),
            default_retention=dict(type="str"),
            min_retention=dict(type="str"),
            max_retention=dict(type="str"),
            retention_lock=dict(type="str", choices=["locked", "unlocked"]),
            context=dict(type="str", default=""),
        )
    )

    required_together = [["keep_for", "every"]]
    required_if = [
        ["policy_type", "access", ["account", "name"]],
        ["policy_type", "nfs", ["name"]],
        ["policy_type", "smb_client", ["name"]],
        ["policy_type", "smb_share", ["name"]],
        ["policy_type", "network", ["interfaces"]],
    ]

    module = AnsibleModule(
        argument_spec,
        required_together=required_together,
        required_if=required_if,
        supports_check_mode=True,
    )

    if not HAS_PYTZ:
        module.fail_json(msg="pytz is required for this module")

    state = module.params["state"]
    blade = get_system(module)
    versions = get_rest_api_version(blade)
    if (
        LooseVersion(CONTEXT_API_VERSION) <= LooseVersion(versions)
        and not module.params["context"]
    ):
        # Only default the context to the local array name when this array is a
        # member of a fleet. A standalone array is "not in a fleet" and rejects
        # fleet context, so leave context unset - no context_names is then sent.
        fleet_res = blade.get_fleets()
        if fleet_res.status_code == 200 and list(fleet_res.items):
            module.params["context"] = list(blade.get_arrays().items)[0].name
    if module.params["policy_type"] == "access":
        if not HAS_PYPURECLIENT:
            module.fail_json(msg="py-pure-client sdk is required for this module")
        try:
            if (
                LooseVersion(CONTEXT_API_VERSION) <= LooseVersion(versions)
                and module.params["context"]
            ):
                policy = list(
                    blade.get_object_store_access_policies(
                        names=[module.params["account"] + "/" + module.params["name"]],
                        context_names=[module.params["context"]],
                    ).items
                )[0]
            else:
                policy = list(
                    blade.get_object_store_access_policies(
                        names=[module.params["account"] + "/" + module.params["name"]]
                    ).items
                )[0]
        except AttributeError:
            policy = None
        if module.params["user"]:
            member_name = module.params["account"] + "/" + module.params["user"]
            if (
                LooseVersion(CONTEXT_API_VERSION) <= LooseVersion(versions)
                and module.params["context"]
            ):
                res = blade.get_object_store_users(
                    names=[member_name], context_names=[module.params["context"]]
                )
            else:
                res = blade.get_object_store_users(names=[member_name])
            if res.status_code != 200:
                module.fail_json(
                    msg="User {0} does not exist in account {1}. Error: {2}".format(
                        module.params["user"],
                        module.params["account"],
                        get_error_message(res),
                    )
                )
        if policy and state == "present":
            update_os_policy(module, blade)
        elif state == "present" and not policy:
            create_os_policy(module, blade)
        elif state == "absent" and policy:
            delete_os_policy(module, blade)
        elif state == "copy":
            if not module.params["target"] or not module.params["rule"]:
                module.fail_json(
                    msg="`target` and `rule` are required to copy an access policy rule"
                )
            if not policy:
                module.fail_json(
                    msg="Policy {0}/{1} does not exist".format(
                        module.params["account"], module.params["name"]
                    )
                )
            if "/" not in module.params["target"]:
                module.fail_json(
                    msg='Incorrect format for target policy. Must be "<account>/<name>"'
                )
            if (
                LooseVersion(CONTEXT_API_VERSION) <= LooseVersion(versions)
                and module.params["context"]
            ):
                res = blade.get_object_store_access_policies(
                    names=[module.params["target"]],
                    context_names=[module.params["context"]],
                )
            else:
                res = blade.get_object_store_access_policies(
                    names=[module.params["target"]]
                )
            if res.status_code != 200:
                module.fail_json(
                    msg="Target policy {0} does not exist".format(
                        module.params["target"]
                    )
                )
            copy_os_policy_rule(module, blade)
    elif module.params["policy_type"] == "nfs":
        new_policy = None
        if LooseVersion(NFS_POLICY_API_VERSION) > LooseVersion(versions):
            module.fail_json(
                msg=(
                    "Minimum FlashBlade REST version required: {0}".format(
                        NFS_POLICY_API_VERSION
                    )
                )
            )
        if not HAS_PYPURECLIENT:
            module.fail_json(msg="py-pure-client sdk is required for this module")
        try:
            if (
                LooseVersion(CONTEXT_API_VERSION) <= LooseVersion(versions)
                and module.params["context"]
            ):
                policy = list(
                    blade.get_nfs_export_policies(
                        names=[module.params["name"]],
                        context_names=[module.params["context"]],
                    ).items
                )[0]
            else:
                policy = list(
                    blade.get_nfs_export_policies(names=[module.params["name"]]).items
                )[0]
        except AttributeError:
            policy = None
        if module.params["rename"]:
            try:
                if (
                    LooseVersion(CONTEXT_API_VERSION) <= LooseVersion(versions)
                    and module.params["context"]
                ):
                    new_policy = list(
                        blade.get_nfs_export_policies(
                            names=[module.params["rename"]],
                            context_names=[module.params["context"]],
                        ).items
                    )[0]
                else:
                    new_policy = list(
                        blade.get_nfs_export_policies(
                            names=[module.params["rename"]]
                        ).items
                    )[0]
            except AttributeError:
                new_policy = None
        if policy and state == "present" and not module.params["rename"]:
            if module.params["before_rule"]:
                if (
                    LooseVersion(CONTEXT_API_VERSION) <= LooseVersion(versions)
                    and module.params["context"]
                ):
                    res = blade.get_nfs_export_policies_rules(
                        policy_names=[module.params["name"]],
                        names=[
                            module.params["name"]
                            + "."
                            + str(module.params["before_rule"])
                        ],
                        context_names=[module.params["context"]],
                    )
                else:
                    res = blade.get_nfs_export_policies_rules(
                        policy_names=[module.params["name"]],
                        names=[
                            module.params["name"]
                            + "."
                            + str(module.params["before_rule"])
                        ],
                    )
                if res.status_code != 200:
                    module.fail_json(
                        msg="Rule index {0} does not exist.".format(
                            module.params["before_rule"]
                        )
                    )
            update_nfs_policy(module, blade)
        elif (
            state == "present" and module.params["rename"] and policy and not new_policy
        ):
            rename_nfs_policy(module, blade)
        elif state == "present" and not policy and not module.params["rename"]:
            create_nfs_policy(module, blade)
        elif state == "absent" and policy:
            delete_nfs_policy(module, blade)
        elif state == "copy":
            copy_policy(module, blade, policy)
    elif module.params["policy_type"] == "smb_client":
        if LooseVersion(SMB_POLICY_API_VERSION) > LooseVersion(versions):
            module.fail_json(
                msg=(
                    "Minimum FlashBlade REST version required: {0}".format(
                        SMB_POLICY_API_VERSION
                    )
                )
            )
        if not HAS_PYPURECLIENT:
            module.fail_json(msg="py-pure-client sdk is required for this module")
        try:
            if (
                LooseVersion(CONTEXT_API_VERSION) <= LooseVersion(versions)
                and module.params["context"]
            ):
                policy = list(
                    blade.get_smb_client_policies(
                        names=[module.params["name"]],
                        context_names=[module.params["context"]],
                    ).items
                )[0]
            else:
                policy = list(
                    blade.get_smb_client_policies(names=[module.params["name"]]).items
                )[0]
        except AttributeError:
            policy = None
        if module.params["rename"]:
            try:
                if (
                    LooseVersion(CONTEXT_API_VERSION) <= LooseVersion(versions)
                    and module.params["context"]
                ):
                    new_policy = list(
                        blade.get_smb_client_policies(
                            names=[module.params["rename"]],
                            context_names=[module.params["context"]],
                        ).items
                    )[0]
                else:
                    new_policy = list(
                        blade.get_smb_client_policies(
                            names=[module.params["rename"]]
                        ).items
                    )[0]
            except AttributeError:
                new_policy = None
        if policy and state == "present" and not module.params["rename"]:
            if module.params["before_rule"]:
                if (
                    LooseVersion(CONTEXT_API_VERSION) <= LooseVersion(versions)
                    and module.params["context"]
                ):
                    res = blade.get_smb_client_policies_rules(
                        policy_names=[module.params["name"]],
                        names=[
                            module.params["name"]
                            + "."
                            + str(module.params["before_rule"])
                        ],
                        context_names=[module.params["context"]],
                    )
                else:
                    res = blade.get_smb_client_policies_rules(
                        policy_names=[module.params["name"]],
                        names=[
                            module.params["name"]
                            + "."
                            + str(module.params["before_rule"])
                        ],
                    )
                if res.status_code != 200:
                    module.fail_json(
                        msg="Rule index {0} does not exist.".format(
                            module.params["before_rule"]
                        )
                    )
            update_smb_client_policy(module, blade)
        elif (
            state == "present" and module.params["rename"] and policy and not new_policy
        ):
            rename_smb_client_policy(module, blade)
        elif state == "present" and not policy and not module.params["rename"]:
            create_smb_client_policy(module, blade)
        elif state == "absent" and policy:
            delete_smb_client_policy(module, blade)
        elif state == "copy":
            copy_policy(module, blade, policy)
    elif module.params["policy_type"] == "smb_share":
        if LooseVersion(SMB_POLICY_API_VERSION) > LooseVersion(versions):
            module.fail_json(
                msg=(
                    "Minimum FlashBlade REST version required: {0}".format(
                        SMB_POLICY_API_VERSION
                    )
                )
            )
        if not HAS_PYPURECLIENT:
            module.fail_json(msg="py-pure-client sdk is required for this module")
        try:
            if (
                LooseVersion(CONTEXT_API_VERSION) <= LooseVersion(versions)
                and module.params["context"]
            ):
                policy = list(
                    blade.get_smb_share_policies(
                        names=[module.params["name"]],
                        context_names=[module.params["context"]],
                    ).items
                )[0]
            else:
                policy = list(
                    blade.get_smb_share_policies(names=[module.params["name"]]).items
                )[0]
        except AttributeError:
            policy = None
        if module.params["rename"]:
            try:
                if (
                    LooseVersion(CONTEXT_API_VERSION) <= LooseVersion(versions)
                    and module.params["context"]
                ):
                    new_policy = list(
                        blade.get_smb_share_policies(
                            names=[module.params["rename"]],
                            context_names=[module.params["context"]],
                        ).items
                    )[0]
                else:
                    new_policy = list(
                        blade.get_smb_share_policies(
                            names=[module.params["rename"]]
                        ).items
                    )[0]
            except AttributeError:
                new_policy = None
        if policy and state == "present" and not module.params["rename"]:
            if module.params["before_rule"]:
                # Share rules are keyed by principal and have no index or
                # evaluation order, so there is nothing to move them before.
                module.fail_json(
                    msg="`before_rule` is not supported for SMB share policies"
                )
            update_smb_share_policy(module, blade)
        elif (
            state == "present" and module.params["rename"] and policy and not new_policy
        ):
            rename_smb_share_policy(module, blade)
        elif state == "present" and not policy and not module.params["rename"]:
            create_smb_share_policy(module, blade)
        elif state == "absent" and policy:
            delete_smb_share_policy(module, blade)
        elif state == "copy":
            copy_policy(module, blade, policy)
    elif module.params["policy_type"] == "network":
        if LooseVersion(NET_POLICY_API_VERSION) > LooseVersion(versions):
            module.fail_json(
                msg=(
                    "Minimum FlashBlade REST version required: {0}".format(
                        NET_POLICY_API_VERSION
                    )
                )
            )
        if not HAS_PYPURECLIENT:
            module.fail_json(msg="py-pure-client sdk is required for this module")
        # The network-access-policies endpoints take no fleet context in any
        # REST version (the SDK methods have no context_names argument), so
        # the context defaulted for fleet members above must not be sent.
        try:
            policy = list(
                blade.get_network_access_policies(names=[module.params["name"]]).items
            )[0]
        except AttributeError:
            policy = None
        if module.params["rename"]:
            try:
                new_policy = list(
                    blade.get_network_access_policies(
                        names=[module.params["rename"]]
                    ).items
                )[0]
            except AttributeError:
                new_policy = None
        if policy and state == "present" and not module.params["rename"]:
            if module.params["before_rule"]:
                res = blade.get_network_access_policies_rules(
                    policy_names=[module.params["name"]],
                    names=[
                        module.params["name"] + "." + str(module.params["before_rule"])
                    ],
                )
                if res.status_code != 200:
                    module.fail_json(
                        msg="Rule index {0} does not exist.".format(
                            module.params["before_rule"]
                        )
                    )
            update_network_access_policy(module, blade)
        elif (
            state == "present" and module.params["rename"] and policy and not new_policy
        ):
            rename_network_access_policy(module, blade)
        elif state == "present" and not policy and not module.params["rename"]:
            create_network_access_policy(module, blade)
        elif state == "absent" and policy:
            delete_network_access_policy(module, blade)
        elif state == "copy":
            module.fail_json(msg="Network access policies cannot be copied")
    elif module.params["policy_type"] == "worm":
        if LooseVersion(WORM_POLICY_API_VERSION) > LooseVersion(versions):
            module.fail_json(
                msg=(
                    "Minimum FlashBlade REST version required: {0}".format(
                        WORM_POLICY_API_VERSION
                    )
                )
            )
        if not HAS_PYPURECLIENT:
            module.fail_json(msg="py-pure-client sdk is required for this module")
        try:
            if (
                LooseVersion(CONTEXT_API_VERSION) <= LooseVersion(versions)
                and module.params["context"]
            ):
                policy = list(
                    blade.get_worm_data_policies(
                        names=[module.params["name"]],
                        context_names=[module.params["context"]],
                    ).items
                )[0]
            else:
                policy = list(
                    blade.get_worm_data_policies(names=[module.params["name"]]).items
                )[0]
        except AttributeError:
            policy = None
        if module.params["rename"]:
            try:
                if (
                    LooseVersion(CONTEXT_API_VERSION) <= LooseVersion(versions)
                    and module.params["context"]
                ):
                    new_policy = list(
                        blade.get_worm_data_policies(
                            names=[module.params["rename"]],
                            context_names=[module.params["context"]],
                        ).items
                    )[0]
                else:
                    new_policy = list(
                        blade.get_worm_data_policies(
                            names=[module.params["rename"]]
                        ).items
                    )[0]
            except AttributeError:
                new_policy = None
        if policy and state == "present" and not module.params["rename"]:
            if module.params["before_rule"]:
                if (
                    LooseVersion(CONTEXT_API_VERSION) <= LooseVersion(versions)
                    and module.params["context"]
                ):
                    res = blade.get_worm_data_policies_rules(
                        policy_names=[module.params["name"]],
                        names=[
                            module.params["name"]
                            + "."
                            + str(module.params["before_rule"])
                        ],
                        context_names=[module.params["context"]],
                    )
                else:
                    res = blade.get_worm_data_policies_rules(
                        policy_names=[module.params["name"]],
                        names=[
                            module.params["name"]
                            + "."
                            + str(module.params["before_rule"])
                        ],
                    )
                if res.status_code != 200:
                    module.fail_json(
                        msg="Rule index {0} does not exist.".format(
                            module.params["before_rule"]
                        )
                    )
            update_worm_data_policy(module, blade)
        elif (
            state == "present" and module.params["rename"] and policy and not new_policy
        ):
            rename_worm_data_policy(module)
        elif state == "present" and not policy and not module.params["rename"]:
            create_worm_data_policy(module, blade)
        elif state == "absent" and policy:
            delete_worm_data_policy(module, blade)
        elif state == "copy":
            copy_policy(module, blade, policy)
    else:
        if not HAS_PYPURECLIENT:
            module.fail_json(msg="py-pure-client sdk is required for this module")
        try:
            if (
                LooseVersion(CONTEXT_API_VERSION) <= LooseVersion(versions)
                and module.params["context"]
            ):
                policy = list(
                    blade.get_policies(
                        names=[module.params["name"]],
                        context_names=[module.params["context"]],
                    ).items
                )[0]
            else:
                policy = list(blade.get_policies(names=[module.params["name"]]).items)[
                    0
                ]
        except AttributeError:
            policy = None
        if module.params["rename"]:
            try:
                new_policy = list(
                    blade.get_policies(
                        names=[module.params["rename"]],
                        **_context_kwargs(module, versions),
                    ).items
                )[0]
            except AttributeError:
                new_policy = None
        if state == "present" and module.params["rename"] and policy and not new_policy:
            rename_snap_policy(module, blade)
        elif not policy and state == "present" and not module.params["rename"]:
            create_snap_policy(module, blade)
        elif policy and state == "present" and not module.params["rename"]:
            update_snap_policy(module, blade)
        elif policy and state == "absent":
            if module.params["filesystem"] or module.params["replica_link"]:
                update_snap_policy(module, blade)
            else:
                delete_snap_policy(module, blade)
        elif state == "copy":
            copy_policy(module, blade, policy)

    module.exit_json(changed=False)


if __name__ == "__main__":
    main()
