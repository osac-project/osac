.. Created with antsibull-docs 2.14.0

ansible.hub.ah_role module -- Manage a role of group permissions
++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++

This module is part of the `ansible.hub collection <https://galaxy.ansible.com/ui/repo/published/ansible/hub/>`_ (version 1.0.0).

It is not included in ``ansible-core``.
To check whether it is installed, run ``ansible-galaxy collection list``.

To install it, use: :code:`ansible-galaxy collection install ansible.hub`.

To use it in a playbook, specify: ``ansible.hub.ah_role``.


.. contents::
   :local:
   :depth: 1


Synopsis
--------

- Manage a role of group permissions.








Parameters
----------

.. raw:: html

  <table style="width: 100%;">
  <thead>
    <tr>
    <th><p>Parameter</p></th>
    <th><p>Comments</p></th>
  </tr>
  </thead>
  <tbody>
  <tr>
    <td valign="top">
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
    <td valign="top">
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
    <td valign="top">
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
    <td valign="top">
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
    <td valign="top">
      <div class="ansibleOptionAnchor" id="parameter-description"></div>
      <p style="display: inline;"><strong>description</strong></p>
      <a class="ansibleOptionLink" href="#parameter-description" title="Permalink to this option"></a>
      <p style="font-size: small; margin-bottom: 0;">
        <span style="color: purple;">string</span>
      </p>
    </td>
    <td valign="top">
      <p>Description to use for the role.</p>
    </td>
  </tr>
  <tr>
    <td valign="top">
      <div class="ansibleOptionAnchor" id="parameter-name"></div>
      <p style="display: inline;"><strong>name</strong></p>
      <a class="ansibleOptionLink" href="#parameter-name" title="Permalink to this option"></a>
      <p style="font-size: small; margin-bottom: 0;">
        <span style="color: purple;">string</span>
        / <span style="color: red;">required</span>
      </p>
    </td>
    <td valign="top">
      <p>Name of the role.</p>
      <p>Must start with &#x27;galaxy.&#x27;.</p>
    </td>
  </tr>
  <tr>
    <td valign="top">
      <div class="ansibleOptionAnchor" id="parameter-perms"></div>
      <p style="display: inline;"><strong>perms</strong></p>
      <a class="ansibleOptionLink" href="#parameter-perms" title="Permalink to this option"></a>
      <p style="font-size: small; margin-bottom: 0;">
        <span style="color: purple;">list</span>
        / <span style="color: purple;">elements=string</span>
      </p>
    </td>
    <td valign="top">
      <p>The list of permissions to add to or remove from the given group.</p>
      <p>The module accepts the following roles.</p>
      <p>For user management, <code class="ansible-value literal notranslate">add_user</code>, <code class="ansible-value literal notranslate">change_user</code>, <code class="ansible-value literal notranslate">delete_user</code>, and <code class="ansible-value literal notranslate">view_user</code>.</p>
      <p>For group management, <code class="ansible-value literal notranslate">add_group</code>, <code class="ansible-value literal notranslate">change_group</code>, <code class="ansible-value literal notranslate">delete_group</code>, and <code class="ansible-value literal notranslate">view_group</code>.</p>
      <p>For collection namespace management, <code class="ansible-value literal notranslate">add_namespace</code>, <code class="ansible-value literal notranslate">change_namespace</code>, <code class="ansible-value literal notranslate">upload_to_namespace</code>, and <code class="ansible-value literal notranslate">delete_namespace</code>.</p>
      <p>For collection content management, <code class="ansible-value literal notranslate">modify_ansible_repo_content</code>, <code class="ansible-value literal notranslate">delete_collection</code>, and <code class="ansible-value literal notranslate">sign_ansiblerepository</code>.</p>
      <p>For remote repository configuration, <code class="ansible-value literal notranslate">change_collectionremote</code>, <code class="ansible-value literal notranslate">view_collectionremote</code>, <code class="ansible-value literal notranslate">add_collectionremote</code>, <code class="ansible-value literal notranslate">delete_collectionremote</code>, and <code class="ansible-value literal notranslate">manage_roles_collectionremote</code>.</p>
      <p>For Ansible Repository management, only with private automation hub v4.7.0 <code class="ansible-value literal notranslate">add_ansiblerepository</code>, <code class="ansible-value literal notranslate">change_ansiblerepository</code>, <code class="ansible-value literal notranslate">delete_ansiblerepository</code>, <code class="ansible-value literal notranslate">manage_roles_ansiblerepository</code>, <code class="ansible-value literal notranslate">repair_ansiblerepository</code>, <code class="ansible-value literal notranslate">view_ansiblerepository</code>,</p>
      <p>For container image management, only with private automation hub v4.3.2 or later, <code class="ansible-value literal notranslate">change_containernamespace_perms</code>, <code class="ansible-value literal notranslate">change_container</code>, <code class="ansible-value literal notranslate">change_image_tag</code>, <code class="ansible-value literal notranslate">create_container</code>, Push existing container <code class="ansible-value literal notranslate">push_container</code>, <code class="ansible-value literal notranslate">namespace_add_containerdistribution</code>, <code class="ansible-value literal notranslate">manage_roles_containernamespace</code>, and <code class="ansible-value literal notranslate">delete_containerrepository</code>.</p>
      <p>For remote registry management, <code class="ansible-value literal notranslate">add_containerregistryremote</code>, <code class="ansible-value literal notranslate">change_containerregistryremote</code>, and <code class="ansible-value literal notranslate">delete_containerregistryremote</code>.</p>
      <p>For task management, <code class="ansible-value literal notranslate">change_task</code>, <code class="ansible-value literal notranslate">view_task</code>, and <code class="ansible-value literal notranslate">delete_task</code>.</p>
      <p>You can also grant or revoke all permissions with <code class="ansible-value literal notranslate">*</code> or <code class="ansible-value literal notranslate">all</code>.</p>
    </td>
  </tr>
  <tr>
    <td valign="top">
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
    <td valign="top">
      <div class="ansibleOptionAnchor" id="parameter-state"></div>
      <p style="display: inline;"><strong>state</strong></p>
      <a class="ansibleOptionLink" href="#parameter-state" title="Permalink to this option"></a>
      <p style="font-size: small; margin-bottom: 0;">
        <span style="color: purple;">string</span>
      </p>
    </td>
    <td valign="top">
      <p>If <code class="ansible-value literal notranslate">absent</code>, then the module removes the listed permissions from the group. If the group has permissions that are not listed in <code class="ansible-option literal notranslate"><strong><a class="reference internal" href="#parameter-perms"><span class="std std-ref"><span class="pre">perms</span></span></a></strong></code>, then the module does not remove those pre-existing permissions.</p>
      <p>If <code class="ansible-value literal notranslate">present</code>, then the module adds the listed permissions to the group. The module does not remove the permissions that the group already has.</p>
      <p style="margin-top: 8px;"><b">Choices:</b></p>
      <ul>
        <li><p><code>&#34;absent&#34;</code></p></li>
        <li><p><code style="color: blue;"><b>&#34;present&#34;</b></code> <span style="color: blue;">← (default)</span></p></li>
      </ul>

    </td>
  </tr>
  <tr>
    <td valign="top">
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




Notes
-----

- Supports :literal:`check\_mode`.
- This module only works up to Automation Hub version 4.6 (AAP 2.3).


Examples
--------

.. code-block:: yaml

    - name: Ensure the operators have the correct permissions to manage users
      ansible.hub.ah_role:
        name: galaxy.operators
        perms:
          - add_user
          - change_user
          - delete_user
          - view_user
        state: present
        ah_host: hub.example.com
        ah_username: admin
        ah_password: Sup3r53cr3t
      no_log: True

    - name: Ensure the administrators have all the permissions
      ansible.hub.ah_role:
        name: galaxy.administrators
        perms: "*"
        state: present
        ah_host: hub.example.com
        ah_username: admin
        ah_password: Sup3r53cr3t
      no_log: True

    - name: Ensure the developers cannot manage groups nor users
      ansible.hub.ah_role:
        name: galaxy.developers
        perms:
          - add_user
          - change_user
          - delete_user
          - add_group
          - change_group
          - delete_group
        state: absent
        ah_host: hub.example.com
        ah_username: admin
        ah_password: Sup3r53cr3t
      no_log: True






Authors
~~~~~~~

- Sean Sullivan (@sean-m-sullivan)



Collection links
~~~~~~~~~~~~~~~~

* `Issue Tracker <https://github.com/ansible-collections/ansible\_hub/issues>`__
* `Repository (Sources) <https://github.com/ansible-collections/ansible\_hub>`__
* `Report an issue <https://github.com/ansible-collections/ansible\_hub/issues/new/choose>`__
