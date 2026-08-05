.. Created with antsibull-docs 2.14.0

ansible.hub.group_roles module -- Add roles to private automation hub user groups
+++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++

This module is part of the `ansible.hub collection <https://galaxy.ansible.com/ui/repo/published/ansible/hub/>`_ (version 1.0.0).

It is not included in ``ansible-core``.
To check whether it is installed, run ``ansible-galaxy collection list``.

To install it, use: :code:`ansible-galaxy collection install ansible.hub`.

To use it in a playbook, specify: ``ansible.hub.group_roles``.


.. contents::
   :local:
   :depth: 1


Synopsis
--------

- Add roles to private automation hub user groups.
- Requires AAP 2.3 or Galaxy 4.6 or Later for global roles.
- Requires AAP 2.4 or Galaxy 4.7 or Later for most targeted roles.








Parameters
----------

.. raw:: html

  <table style="width: 100%;">
  <thead>
    <tr>
    <th colspan="3"><p>Parameter</p></th>
    <th><p>Comments</p></th>
  </tr>
  </thead>
  <tbody>
  <tr>
    <td colspan="3" valign="top">
      <div class="ansibleOptionAnchor" id="parameter-ah_host"></div>
      <div class="ansibleOptionAnchor" id="parameter-ah_hostname"></div>
      <p style="display: inline;"><strong>ah_host</strong></p>
      <a class="ansibleOptionLink" href="#parameter-ah_host" title="Permalink to this option"></a>
      <p style="font-size: small; margin-bottom: 0;"><span style="color: darkgreen; white-space: normal;">aliases: ah_hostname</span></p>
      <p style="font-size: small; margin-bottom: 0;">
        <span style="color: purple;">string</span>
      </p>
    </td>
    <td valign="top">
      <p>URL to Ansible Automation Hub instance.</p>
      <p>If value not set, will try environment variable <code class="xref std std-envvar literal notranslate">AH_HOST</code>.</p>
      <p>If value not specified by any means, the value of <code class='docutils literal notranslate'>127.0.0.1</code> will be used.</p>
    </td>
  </tr>
  <tr>
    <td colspan="3" valign="top">
      <div class="ansibleOptionAnchor" id="parameter-ah_password"></div>
      <p style="display: inline;"><strong>ah_password</strong></p>
      <a class="ansibleOptionLink" href="#parameter-ah_password" title="Permalink to this option"></a>
      <p style="font-size: small; margin-bottom: 0;">
        <span style="color: purple;">string</span>
      </p>
    </td>
    <td valign="top">
      <p>Password for your Ansible Automation Hub instance.</p>
      <p>If value not set, will try environment variable <code class="xref std std-envvar literal notranslate">AH_PASSWORD</code>.</p>
    </td>
  </tr>
  <tr>
    <td colspan="3" valign="top">
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
    <td colspan="3" valign="top">
      <div class="ansibleOptionAnchor" id="parameter-ah_username"></div>
      <p style="display: inline;"><strong>ah_username</strong></p>
      <a class="ansibleOptionLink" href="#parameter-ah_username" title="Permalink to this option"></a>
      <p style="font-size: small; margin-bottom: 0;">
        <span style="color: purple;">string</span>
      </p>
    </td>
    <td valign="top">
      <p>Username for your Ansible Automation Hub instance.</p>
      <p>If value not set, will try environment variable <code class="xref std std-envvar literal notranslate">AH_USERNAME</code>.</p>
    </td>
  </tr>
  <tr>
    <td colspan="3" valign="top">
      <div class="ansibleOptionAnchor" id="parameter-groups"></div>
      <p style="display: inline;"><strong>groups</strong></p>
      <a class="ansibleOptionLink" href="#parameter-groups" title="Permalink to this option"></a>
      <p style="font-size: small; margin-bottom: 0;">
        <span style="color: purple;">list</span>
        / <span style="color: purple;">elements=string</span>
        / <span style="color: red;">required</span>
      </p>
    </td>
    <td valign="top">
      <p>List of Group names that receive the permissions specified by the roles.</p>
      <p>If the group is not found, it will be created.</p>
    </td>
  </tr>
  <tr>
    <td colspan="3" valign="top">
      <div class="ansibleOptionAnchor" id="parameter-request_timeout"></div>
      <p style="display: inline;"><strong>request_timeout</strong></p>
      <a class="ansibleOptionLink" href="#parameter-request_timeout" title="Permalink to this option"></a>
      <p style="font-size: small; margin-bottom: 0;">
        <span style="color: purple;">float</span>
      </p>
    </td>
    <td valign="top">
      <p>Specify the timeout Ansible should use in requests to the Automation Hub host.</p>
      <p>Defaults to 10 seconds, but this is handled by the shared module_utils code.</p>
    </td>
  </tr>
  <tr>
    <td colspan="3" valign="top">
      <div class="ansibleOptionAnchor" id="parameter-role_list"></div>
      <p style="display: inline;"><strong>role_list</strong></p>
      <a class="ansibleOptionLink" href="#parameter-role_list" title="Permalink to this option"></a>
      <p style="font-size: small; margin-bottom: 0;">
        <span style="color: purple;">list</span>
        / <span style="color: purple;">elements=dictionary</span>
        / <span style="color: red;">required</span>
      </p>
    </td>
    <td valign="top">
      <p>List of sets of roles and targets to apply to the groups.</p>
    </td>
  </tr>
  <tr>
    <td></td>
    <td colspan="2" valign="top">
      <div class="ansibleOptionAnchor" id="parameter-role_list/roles"></div>
      <p style="display: inline;"><strong>roles</strong></p>
      <a class="ansibleOptionLink" href="#parameter-role_list/roles" title="Permalink to this option"></a>
      <p style="font-size: small; margin-bottom: 0;">
        <span style="color: purple;">list</span>
        / <span style="color: purple;">elements=string</span>
      </p>
    </td>
    <td valign="top">
      <p>List of roles to apply to the groups.</p>
    </td>
  </tr>
  <tr>
    <td></td>
    <td colspan="2" valign="top">
      <div class="ansibleOptionAnchor" id="parameter-role_list/targets"></div>
      <p style="display: inline;"><strong>targets</strong></p>
      <a class="ansibleOptionLink" href="#parameter-role_list/targets" title="Permalink to this option"></a>
      <p style="font-size: small; margin-bottom: 0;">
        <span style="color: purple;">dictionary</span>
      </p>
    </td>
    <td valign="top">
      <p>List of targets to apply the roles to.</p>
      <p>If left empty, it will give global permissions to the group.</p>
      <p>An example of using this would be to give a specific group rights over a list of collection namespaces.</p>
      <p style="margin-top: 8px;"><b style="color: blue;">Default:</b> <code style="color: blue;">{}</code></p>
    </td>
  </tr>
  <tr>
    <td></td>
    <td></td>
    <td valign="top">
      <div class="ansibleOptionAnchor" id="parameter-role_list/targets/collection_namespaces"></div>
      <p style="display: inline;"><strong>collection_namespaces</strong></p>
      <a class="ansibleOptionLink" href="#parameter-role_list/targets/collection_namespaces" title="Permalink to this option"></a>
      <p style="font-size: small; margin-bottom: 0;">
        <span style="color: purple;">list</span>
        / <span style="color: purple;">elements=string</span>
      </p>
    </td>
    <td valign="top">
      <p>List of collection namespaces to limit the role permissions to.</p>
      <p style="margin-top: 8px;"><b style="color: blue;">Default:</b> <code style="color: blue;">[]</code></p>
    </td>
  </tr>
  <tr>
    <td></td>
    <td></td>
    <td valign="top">
      <div class="ansibleOptionAnchor" id="parameter-role_list/targets/collection_remotes"></div>
      <p style="display: inline;"><strong>collection_remotes</strong></p>
      <a class="ansibleOptionLink" href="#parameter-role_list/targets/collection_remotes" title="Permalink to this option"></a>
      <p style="font-size: small; margin-bottom: 0;">
        <span style="color: purple;">list</span>
        / <span style="color: purple;">elements=string</span>
      </p>
    </td>
    <td valign="top">
      <p>List of collection remotes to limit the role permissions to.</p>
      <p style="margin-top: 8px;"><b style="color: blue;">Default:</b> <code style="color: blue;">[]</code></p>
    </td>
  </tr>
  <tr>
    <td></td>
    <td></td>
    <td valign="top">
      <div class="ansibleOptionAnchor" id="parameter-role_list/targets/collection_repositories"></div>
      <p style="display: inline;"><strong>collection_repositories</strong></p>
      <a class="ansibleOptionLink" href="#parameter-role_list/targets/collection_repositories" title="Permalink to this option"></a>
      <p style="font-size: small; margin-bottom: 0;">
        <span style="color: purple;">list</span>
        / <span style="color: purple;">elements=string</span>
      </p>
    </td>
    <td valign="top">
      <p>List of collection repositories to limit the role permissions to.</p>
      <p style="margin-top: 8px;"><b style="color: blue;">Default:</b> <code style="color: blue;">[]</code></p>
    </td>
  </tr>
  <tr>
    <td></td>
    <td></td>
    <td valign="top">
      <div class="ansibleOptionAnchor" id="parameter-role_list/targets/container_registery_remotes"></div>
      <p style="display: inline;"><strong>container_registery_remotes</strong></p>
      <a class="ansibleOptionLink" href="#parameter-role_list/targets/container_registery_remotes" title="Permalink to this option"></a>
      <p style="font-size: small; margin-bottom: 0;">
        <span style="color: purple;">list</span>
        / <span style="color: purple;">elements=string</span>
      </p>
    </td>
    <td valign="top">
      <p>List of container remote registries to limit the role permissions to.</p>
      <p style="margin-top: 8px;"><b style="color: blue;">Default:</b> <code style="color: blue;">[]</code></p>
    </td>
  </tr>
  <tr>
    <td></td>
    <td></td>
    <td valign="top">
      <div class="ansibleOptionAnchor" id="parameter-role_list/targets/execution_environments"></div>
      <p style="display: inline;"><strong>execution_environments</strong></p>
      <a class="ansibleOptionLink" href="#parameter-role_list/targets/execution_environments" title="Permalink to this option"></a>
      <p style="font-size: small; margin-bottom: 0;">
        <span style="color: purple;">list</span>
        / <span style="color: purple;">elements=string</span>
      </p>
    </td>
    <td valign="top">
      <p>List of execution environments to limit the role permissions to.</p>
      <p style="margin-top: 8px;"><b style="color: blue;">Default:</b> <code style="color: blue;">[]</code></p>
    </td>
  </tr>


  <tr>
    <td colspan="3" valign="top">
      <div class="ansibleOptionAnchor" id="parameter-state"></div>
      <p style="display: inline;"><strong>state</strong></p>
      <a class="ansibleOptionLink" href="#parameter-state" title="Permalink to this option"></a>
      <p style="font-size: small; margin-bottom: 0;">
        <span style="color: purple;">string</span>
      </p>
    </td>
    <td valign="top">
      <p>If <code class="ansible-value literal notranslate">absent</code>, then the module deletes the given combination of roles for given groups.</p>
      <p>The module does not fail if the combination does not exist because the state is already as expected.</p>
      <p>If <code class="ansible-value literal notranslate">present</code>, then the module creates the group roles if it does not already exist.</p>
      <p>If already existing, no change is made.</p>
      <p>If <code class="ansible-value literal notranslate">enforced</code>, then the module will remove any group role combinations not provided.</p>
      <p style="margin-top: 8px;"><b">Choices:</b></p>
      <ul>
        <li><p><code style="color: blue;"><b>&#34;present&#34;</b></code> <span style="color: blue;">← (default)</span></p></li>
        <li><p><code>&#34;enforced&#34;</code></p></li>
        <li><p><code>&#34;absent&#34;</code></p></li>
      </ul>

    </td>
  </tr>
  <tr>
    <td colspan="3" valign="top">
      <div class="ansibleOptionAnchor" id="parameter-validate_certs"></div>
      <div class="ansibleOptionAnchor" id="parameter-ah_verify_ssl"></div>
      <p style="display: inline;"><strong>validate_certs</strong></p>
      <a class="ansibleOptionLink" href="#parameter-validate_certs" title="Permalink to this option"></a>
      <p style="font-size: small; margin-bottom: 0;"><span style="color: darkgreen; white-space: normal;">aliases: ah_verify_ssl</span></p>
      <p style="font-size: small; margin-bottom: 0;">
        <span style="color: purple;">boolean</span>
      </p>
    </td>
    <td valign="top">
      <p>Whether to allow insecure connections to Automation Hub Server.</p>
      <p>If <code class="ansible-value literal notranslate">no</code>, SSL certificates will not be validated.</p>
      <p>This should only be used on personally controlled sites using self-signed certificates.</p>
      <p>If value not set, will try environment variable <code class="xref std std-envvar literal notranslate">AH_VERIFY_SSL</code>.</p>
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

    - name: Ensure the group exists
      ansible.hub.group_roles:
        groups:
          - santa
          - group1
        role_list:
          - roles:
              - galaxy.group_admin
          - roles:
              - galaxy.collection_remote_owner
            targets:
              collection_remotes:
                - community
          - roles:
              - galaxy.execution_environment_admin
          - roles:
              - galaxy.collection_namespace_owner
            targets:
              collection_namespaces:
                - autohubtest2
        state: present
        ah_host: hub.example.com
        ah_username: admin
        ah_password: Sup3r53cr3t






Authors
~~~~~~~

- Sean Sullivan (@sean-m-sullivan)



Collection links
~~~~~~~~~~~~~~~~

* `Issue Tracker <https://github.com/ansible-collections/ansible\_hub/issues>`__
* `Repository (Sources) <https://github.com/ansible-collections/ansible\_hub>`__
* `Report an issue <https://github.com/ansible-collections/ansible\_hub/issues/new/choose>`__
