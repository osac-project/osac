.. Created with antsibull-docs 2.24.0

ansible.hub.team_roles module -- Assign roles to teams in private automation hub
++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++

This module is part of the `ansible.hub collection <https://galaxy.ansible.com/ui/repo/published/ansible/hub/>`_ (version 1.0.6).

It is not included in ``ansible-core``.
To check whether it is installed, run ``ansible-galaxy collection list``.

To install it, use: :code:`ansible\-galaxy collection install ansible.hub`.

To use it in a playbook, specify: ``ansible.hub.team_roles``.


.. contents::
   :local:
   :depth: 1


Synopsis
--------

- Assign roles to existing teams in private automation hub.
- Teams and roles must already exist.
- Uses the Hub UI v2 API for role assignments.
- Requires private automation hub version 4.11 or later.








Parameters
----------

.. raw:: html

  <table style="width: 100%;">
  <thead>
    <tr>
    <th colspan="2"><p>Parameter</p></th>
    <th><p>Comments</p></th>
  </tr>
  </thead>
  <tbody>
  <tr>
    <td colspan="2" valign="top">
      <div class="ansibleOptionAnchor" id="parameter-ah_host"></div>
      <div class="ansibleOptionAnchor" id="parameter-ah_hostname"></div>
      <div class="ansibleOptionAnchor" id="parameter-aap_hostname"></div>
      <p style="display: inline;"><strong>ah_host</strong></p>
      <a class="ansibleOptionLink" href="#parameter-ah_host" title="Permalink to this option"></a>
      <p style="font-size: small; margin-bottom: 0;"><span style="color: darkgreen; white-space: normal;">aliases: ah_hostname, aap_hostname</span></p>
      <p style="font-size: small; margin-bottom: 0;">
        <span style="color: purple;">string</span>
      </p>
    </td>
    <td valign="top">
      <p>URL to Ansible Automation Hub instance.</p>
      <p>If value not set, will try environment variable <code class="xref std std-envvar literal notranslate">AH_HOST</code>, or <code class="xref std std-envvar literal notranslate">AAP_HOSTNAME</code>.</p>
      <p>If value not specified by any means, the value of <code class='docutils literal notranslate'>127.0.0.1</code> will be used.</p>
    </td>
  </tr>
  <tr>
    <td colspan="2" valign="top">
      <div class="ansibleOptionAnchor" id="parameter-ah_password"></div>
      <div class="ansibleOptionAnchor" id="parameter-aap_password"></div>
      <p style="display: inline;"><strong>ah_password</strong></p>
      <a class="ansibleOptionLink" href="#parameter-ah_password" title="Permalink to this option"></a>
      <p style="font-size: small; margin-bottom: 0;"><span style="color: darkgreen; white-space: normal;">aliases: aap_password</span></p>
      <p style="font-size: small; margin-bottom: 0;">
        <span style="color: purple;">string</span>
      </p>
    </td>
    <td valign="top">
      <p>Password for your Ansible Automation Hub instance.</p>
      <p>If value not set, will try environment variable <code class="xref std std-envvar literal notranslate">AH_PASSWORD</code>, or <code class="xref std std-envvar literal notranslate">AAP_PASSWORD</code>.</p>
    </td>
  </tr>
  <tr>
    <td colspan="2" valign="top">
      <div class="ansibleOptionAnchor" id="parameter-ah_path_prefix"></div>
      <p style="display: inline;"><strong>ah_path_prefix</strong></p>
      <a class="ansibleOptionLink" href="#parameter-ah_path_prefix" title="Permalink to this option"></a>
      <p style="font-size: small; margin-bottom: 0;">
        <span style="color: purple;">string</span>
      </p>
    </td>
    <td valign="top">
      <p>API path used to access the api.</p>
      <p>For galaxy_ng this is either <code class="ansible-value literal notranslate">automation-hub</code> or the custom prefix used on install with <code class="xref std std-envvar literal notranslate">GALAXY_API_PATH_PREFIX</code>.</p>
      <p>For Automation Hub this is <code class="ansible-value literal notranslate">galaxy</code>.</p>
      <p style="margin-top: 8px;"><b style="color: blue;">Default:</b> <code style="color: blue;">&#34;galaxy&#34;</code></p>
    </td>
  </tr>
  <tr>
    <td colspan="2" valign="top">
      <div class="ansibleOptionAnchor" id="parameter-ah_token"></div>
      <div class="ansibleOptionAnchor" id="parameter-aap_token"></div>
      <p style="display: inline;"><strong>ah_token</strong></p>
      <a class="ansibleOptionLink" href="#parameter-ah_token" title="Permalink to this option"></a>
      <p style="font-size: small; margin-bottom: 0;"><span style="color: darkgreen; white-space: normal;">aliases: aap_token</span></p>
      <p style="font-size: small; margin-bottom: 0;">
        <span style="color: purple;">any</span>
      </p>
    </td>
    <td valign="top">
      <p>The Ansible Automation Hub API token to use.</p>
      <p>This value can be in one of two formats.</p>
      <p>A string which is the token itself. (for example, bqV5txm97wqJqtkxlMkhQz0pKhRMMX)</p>
      <p>A dictionary structure as returned by the ah_token module.</p>
      <p>If value not set, will try environment variable <code class="xref std std-envvar literal notranslate">AH_API_TOKEN</code>, or <code class="xref std std-envvar literal notranslate">AAP_TOKEN</code>.</p>
    </td>
  </tr>
  <tr>
    <td colspan="2" valign="top">
      <div class="ansibleOptionAnchor" id="parameter-ah_username"></div>
      <div class="ansibleOptionAnchor" id="parameter-aap_username"></div>
      <p style="display: inline;"><strong>ah_username</strong></p>
      <a class="ansibleOptionLink" href="#parameter-ah_username" title="Permalink to this option"></a>
      <p style="font-size: small; margin-bottom: 0;"><span style="color: darkgreen; white-space: normal;">aliases: aap_username</span></p>
      <p style="font-size: small; margin-bottom: 0;">
        <span style="color: purple;">string</span>
      </p>
    </td>
    <td valign="top">
      <p>Username for your Ansible Automation Hub instance.</p>
      <p>If value not set, will try environment variable <code class="xref std std-envvar literal notranslate">AH_USERNAME</code>, or <code class="xref std std-envvar literal notranslate">AAP_USERNAME</code>.</p>
    </td>
  </tr>
  <tr>
    <td colspan="2" valign="top">
      <div class="ansibleOptionAnchor" id="parameter-request_timeout"></div>
      <div class="ansibleOptionAnchor" id="parameter-aap_request_timeout"></div>
      <p style="display: inline;"><strong>request_timeout</strong></p>
      <a class="ansibleOptionLink" href="#parameter-request_timeout" title="Permalink to this option"></a>
      <p style="font-size: small; margin-bottom: 0;"><span style="color: darkgreen; white-space: normal;">aliases: aap_request_timeout</span></p>
      <p style="font-size: small; margin-bottom: 0;">
        <span style="color: purple;">float</span>
      </p>
    </td>
    <td valign="top">
      <p>Specify the timeout Ansible should use in requests to the Automation Hub host.</p>
      <p>Defaults to 10 seconds, but this is handled by the shared module_utils code.</p>
      <p>If value not set, will try environment variable <code class="xref std std-envvar literal notranslate">AAP_REQUEST_TIMEOUT</code></p>
    </td>
  </tr>
  <tr>
    <td colspan="2" valign="top">
      <div class="ansibleOptionAnchor" id="parameter-role"></div>
      <p style="display: inline;"><strong>role</strong></p>
      <a class="ansibleOptionLink" href="#parameter-role" title="Permalink to this option"></a>
      <p style="font-size: small; margin-bottom: 0;">
        <span style="color: purple;">string</span>
        / <span style="color: red;">required</span>
      </p>
    </td>
    <td valign="top">
      <p>Role name to assign to the team (e.g., galaxy.collection_namespace_owner).</p>
      <p>The role must already exist.</p>
    </td>
  </tr>
  <tr>
    <td colspan="2" valign="top">
      <div class="ansibleOptionAnchor" id="parameter-state"></div>
      <p style="display: inline;"><strong>state</strong></p>
      <a class="ansibleOptionLink" href="#parameter-state" title="Permalink to this option"></a>
      <p style="font-size: small; margin-bottom: 0;">
        <span style="color: purple;">string</span>
      </p>
    </td>
    <td valign="top">
      <p>If <code class="ansible-value literal notranslate">present</code>, assigns the role to the team.</p>
      <p>If <code class="ansible-value literal notranslate">absent</code>, removes the role from the team.</p>
      <p style="margin-top: 8px;"><b">Choices:</b></p>
      <ul>
        <li><p><code style="color: blue;"><b>&#34;present&#34;</b></code> <span style="color: blue;">← (default)</span></p></li>
        <li><p><code>&#34;absent&#34;</code></p></li>
      </ul>

    </td>
  </tr>
  <tr>
    <td colspan="2" valign="top">
      <div class="ansibleOptionAnchor" id="parameter-targets"></div>
      <p style="display: inline;"><strong>targets</strong></p>
      <a class="ansibleOptionLink" href="#parameter-targets" title="Permalink to this option"></a>
      <p style="font-size: small; margin-bottom: 0;">
        <span style="color: purple;">dictionary</span>
      </p>
    </td>
    <td valign="top">
      <p>Resource targets to scope the role to.</p>
      <p>Required for object-level roles.</p>
      <p>When multiple namespaces are specified, a separate role assignment is created for each.</p>
    </td>
  </tr>
  <tr>
    <td></td>
    <td valign="top">
      <div class="ansibleOptionAnchor" id="parameter-targets/collection_namespaces"></div>
      <p style="display: inline;"><strong>collection_namespaces</strong></p>
      <a class="ansibleOptionLink" href="#parameter-targets/collection_namespaces" title="Permalink to this option"></a>
      <p style="font-size: small; margin-bottom: 0;">
        <span style="color: purple;">list</span>
        / <span style="color: purple;">elements=string</span>
      </p>
    </td>
    <td valign="top">
      <p>List of collection namespaces to limit permissions to.</p>
      <p>Each namespace will receive its own role assignment.</p>
    </td>
  </tr>
  <tr>
    <td colspan="2" valign="top">
      <div class="ansibleOptionAnchor" id="parameter-organization"></div>
      <p style="display: inline;"><strong>organization</strong></p>
      <a class="ansibleOptionLink" href="#parameter-organization" title="Permalink to this option"></a>
      <p style="font-size: small; margin-bottom: 0;">
        <span style="color: purple;">string</span>
      </p>
    </td>
    <td valign="top">
      <p>Name or ID of the organization containing the team.</p>
      <p>Optional when the team name is unique.</p>
      <p>Required when teams with the same name exist in multiple organizations.</p>
      <p>The module fails if multiple matching teams are found without this parameter.</p>
    </td>
  </tr>

  <tr>
    <td colspan="2" valign="top">
      <div class="ansibleOptionAnchor" id="parameter-team"></div>
      <p style="display: inline;"><strong>team</strong></p>
      <a class="ansibleOptionLink" href="#parameter-team" title="Permalink to this option"></a>
      <p style="font-size: small; margin-bottom: 0;">
        <span style="color: purple;">string</span>
        / <span style="color: red;">required</span>
      </p>
    </td>
    <td valign="top">
      <p>Name of the team to assign roles to.</p>
      <p>The team must already exist.</p>
    </td>
  </tr>
  <tr>
    <td colspan="2" valign="top">
      <div class="ansibleOptionAnchor" id="parameter-validate_certs"></div>
      <div class="ansibleOptionAnchor" id="parameter-ah_verify_ssl"></div>
      <div class="ansibleOptionAnchor" id="parameter-aap_validate_certs"></div>
      <p style="display: inline;"><strong>validate_certs</strong></p>
      <a class="ansibleOptionLink" href="#parameter-validate_certs" title="Permalink to this option"></a>
      <p style="font-size: small; margin-bottom: 0;"><span style="color: darkgreen; white-space: normal;">aliases: ah_verify_ssl, aap_validate_certs</span></p>
      <p style="font-size: small; margin-bottom: 0;">
        <span style="color: purple;">boolean</span>
      </p>
    </td>
    <td valign="top">
      <p>Whether to allow insecure connections to Automation Hub Server.</p>
      <p>If <code class="ansible-value literal notranslate">no</code>, SSL certificates will not be validated.</p>
      <p>This should only be used on personally controlled sites using self-signed certificates.</p>
      <p>If value not set, will try environment variable <code class="xref std std-envvar literal notranslate">AH_VERIFY_SSL</code>, or <code class="xref std std-envvar literal notranslate">AAP_VALIDATE_CERTS</code>.</p>
      <p style="margin-top: 8px;"><b">Choices:</b></p>
      <ul>
        <li><p><code>false</code></p></li>
        <li><p><code>true</code></p></li>
      </ul>

    </td>
  </tr>
  </tbody>
  </table>






Examples
--------

.. code-block:: yaml

    - name: Assign namespace-scoped role to a team
      ansible.hub.team_roles:
        organization: my_org
        team: my_team
        role: galaxy.collection_namespace_owner
        targets:
          collection_namespaces:
            - my_namespace
        state: present
        ah_host: hub.example.com
        ah_username: admin
        ah_password: Sup3r53cr3t

    - name: Assign role to a team for multiple namespaces
      ansible.hub.team_roles:
        team: my_team
        role: galaxy.collection_namespace_owner
        targets:
          collection_namespaces:
            - namespace_one
            - namespace_two
        state: present
        ah_host: hub.example.com
        ah_username: admin
        ah_password: Sup3r53cr3t

    - name: Remove a role from a team
      ansible.hub.team_roles:
        team: my_team
        role: galaxy.collection_namespace_owner
        targets:
          collection_namespaces:
            - my_namespace
        state: absent
        ah_host: hub.example.com
        ah_username: admin
        ah_password: Sup3r53cr3t




Return Values
-------------
The following are the fields unique to this module:

.. raw:: html

  <table style="width: 100%;">
  <thead>
    <tr>
    <th><p>Key</p></th>
    <th><p>Description</p></th>
  </tr>
  </thead>
  <tbody>
  <tr>
    <td valign="top">
      <div class="ansibleOptionAnchor" id="return-object_id"></div>
      <p style="display: inline;"><strong>object_id</strong></p>
      <a class="ansibleOptionLink" href="#return-object_id" title="Permalink to this return value"></a>
      <p style="font-size: small; margin-bottom: 0;">
        <span style="color: purple;">string</span>
      </p>
    </td>
    <td valign="top">
      <p>The target object ID if a scoped role was used.</p>
      <p>When multiple namespaces are specified, only the last processed object ID is returned.</p>
      <p style="margin-top: 8px;"><b>Returned:</b> when targets are specified and a change was made</p>
    </td>
  </tr>
  <tr>
    <td valign="top">
      <div class="ansibleOptionAnchor" id="return-role"></div>
      <p style="display: inline;"><strong>role</strong></p>
      <a class="ansibleOptionLink" href="#return-role" title="Permalink to this return value"></a>
      <p style="font-size: small; margin-bottom: 0;">
        <span style="color: purple;">string</span>
      </p>
    </td>
    <td valign="top">
      <p>The role that was assigned or removed.</p>
      <p style="margin-top: 8px;"><b>Returned:</b> always</p>
    </td>
  </tr>
  <tr>
    <td valign="top">
      <div class="ansibleOptionAnchor" id="return-team"></div>
      <p style="display: inline;"><strong>team</strong></p>
      <a class="ansibleOptionLink" href="#return-team" title="Permalink to this return value"></a>
      <p style="font-size: small; margin-bottom: 0;">
        <span style="color: purple;">string</span>
      </p>
    </td>
    <td valign="top">
      <p>The name of the team.</p>
      <p style="margin-top: 8px;"><b>Returned:</b> always</p>
    </td>
  </tr>
  </tbody>
  </table>




Authors
~~~~~~~

- Brian McLaughlin (@bmclaughlin)


Collection links
~~~~~~~~~~~~~~~~

* `Issue Tracker <https://github.com/ansible\-collections/ansible\_hub/issues>`__
* `Repository (Sources) <https://github.com/ansible\-collections/ansible\_hub>`__
* `Report an issue <https://github.com/ansible\-collections/ansible\_hub/issues/new/choose>`__
