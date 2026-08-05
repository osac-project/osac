.. Created with antsibull-docs 2.14.0

ansible.hub.ah_ee_repository module -- Manage private automation hub execution environment repositories/containers
++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++

This module is part of the `ansible.hub collection <https://galaxy.ansible.com/ui/repo/published/ansible/hub/>`_ (version 1.0.0).

It is not included in ``ansible-core``.
To check whether it is installed, run ``ansible-galaxy collection list``.

To install it, use: :code:`ansible-galaxy collection install ansible.hub`.

To use it in a playbook, specify: ``ansible.hub.ah_ee_repository``.


.. contents::
   :local:
   :depth: 1


Synopsis
--------

- Update and delete execution environment repositories (also known as containers).
- Grant group access to repositories.








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
      <p>Text that describes the repository.</p>
    </td>
  </tr>
  <tr>
    <td valign="top">
      <div class="ansibleOptionAnchor" id="parameter-exclude_tags"></div>
      <p style="display: inline;"><strong>exclude_tags</strong></p>
      <a class="ansibleOptionLink" href="#parameter-exclude_tags" title="Permalink to this option"></a>
      <p style="font-size: small; margin-bottom: 0;">
        <span style="color: purple;">list</span>
        / <span style="color: purple;">elements=string</span>
      </p>
    </td>
    <td valign="top">
      <p>The tags to avoid pulling in.</p>
      <p style="margin-top: 8px;"><b style="color: blue;">Default:</b> <code style="color: blue;">[]</code></p>
    </td>
  </tr>
  <tr>
    <td valign="top">
      <div class="ansibleOptionAnchor" id="parameter-include_tags"></div>
      <p style="display: inline;"><strong>include_tags</strong></p>
      <a class="ansibleOptionLink" href="#parameter-include_tags" title="Permalink to this option"></a>
      <p style="font-size: small; margin-bottom: 0;">
        <span style="color: purple;">list</span>
        / <span style="color: purple;">elements=string</span>
      </p>
    </td>
    <td valign="top">
      <p>The tags to pull in.</p>
      <p style="margin-top: 8px;"><b style="color: blue;">Default:</b> <code style="color: blue;">[]</code></p>
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
      <p>Name of the repository to remove or modify.</p>
    </td>
  </tr>
  <tr>
    <td valign="top">
      <div class="ansibleOptionAnchor" id="parameter-readme"></div>
      <p style="display: inline;"><strong>readme</strong></p>
      <a class="ansibleOptionLink" href="#parameter-readme" title="Permalink to this option"></a>
      <p style="font-size: small; margin-bottom: 0;">
        <span style="color: purple;">string</span>
      </p>
    </td>
    <td valign="top">
      <p>README text in Markdown format for the repository.</p>
      <p>Mutually exclusive with the <code class="ansible-option literal notranslate"><strong><a class="reference internal" href="#parameter-readme_file"><span class="std std-ref"><span class="pre">readme_file</span></span></a></strong></code> option.</p>
    </td>
  </tr>
  <tr>
    <td valign="top">
      <div class="ansibleOptionAnchor" id="parameter-readme_file"></div>
      <p style="display: inline;"><strong>readme_file</strong></p>
      <a class="ansibleOptionLink" href="#parameter-readme_file" title="Permalink to this option"></a>
      <p style="font-size: small; margin-bottom: 0;">
        <span style="color: purple;">path</span>
      </p>
    </td>
    <td valign="top">
      <p>Path to a README file in Markdown format to associate with the repository.</p>
      <p>Mutually exclusive with the <code class="ansible-option literal notranslate"><strong><a class="reference internal" href="#parameter-readme"><span class="std std-ref"><span class="pre">readme</span></span></a></strong></code> option.</p>
    </td>
  </tr>
  <tr>
    <td valign="top">
      <div class="ansibleOptionAnchor" id="parameter-registry"></div>
      <p style="display: inline;"><strong>registry</strong></p>
      <a class="ansibleOptionLink" href="#parameter-registry" title="Permalink to this option"></a>
      <p style="font-size: small; margin-bottom: 0;">
        <span style="color: purple;">string</span>
      </p>
    </td>
    <td valign="top">
      <p>The remote registry that the repository belongs in.</p>
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
      <p>If <code class="ansible-value literal notranslate">absent</code>, then the module deletes the repository.</p>
      <p>The module does not fail if the repository does not exist because the state is already as expected.</p>
      <p>If <code class="ansible-value literal notranslate">present</code>, then the module sets the description and README file for the repository.</p>
      <p style="margin-top: 8px;"><b">Choices:</b></p>
      <ul>
        <li><p><code>&#34;absent&#34;</code></p></li>
        <li><p><code style="color: blue;"><b>&#34;present&#34;</b></code> <span style="color: blue;">← (default)</span></p></li>
      </ul>

    </td>
  </tr>
  <tr>
    <td valign="top">
      <div class="ansibleOptionAnchor" id="parameter-upstream_name"></div>
      <p style="display: inline;"><strong>upstream_name</strong></p>
      <a class="ansibleOptionLink" href="#parameter-upstream_name" title="Permalink to this option"></a>
      <p style="font-size: small; margin-bottom: 0;">
        <span style="color: purple;">string</span>
      </p>
    </td>
    <td valign="top">
      <p>The name of the image upstream.</p>
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
- Only works with private automation hub v4.3.2 or later for local repositories and v4.4.0 for remote repositories.
- The module cannot be use to create repositories. Use :literal:`podman push` for example to create repositories.


Examples
--------

.. code-block:: yaml

    - name: Ensure the repository description and README are set
      ansible.hub.ah_ee_repository:
        name: ansible-automation-platform-20-early-access/ee-supported-rhel8
        state: present
        description: Supported execution environment
        readme: |
          # My execution environment

          * bullet 1
          * bullet 2
        ah_host: hub.example.com
        ah_username: admin
        ah_password: Sup3r53cr3t
      no_log: True

    - name: Ensure the repository README is set
      ansible.hub.ah_ee_repository:
        name: ansible-automation-platform-20-early-access/ee-supported-rhel8
        state: present
        readme_file: README.md
        ah_host: hub.example.com
        ah_username: admin
        ah_password: Sup3r53cr3t
      no_log: True

    - name: Ensure the repository is removed
      ansible.hub.ah_ee_repository:
        name: ansible-automation-platform-20-early-access/ee-supported-rhel8
        state: absent
        ah_host: hub.example.com
        ah_username: admin
        ah_password: Sup3r53cr3t
      no_log: True

    - name: Add a remote repository from quayio registry
      ansible.hub.ah_ee_repository:
        name: myrepo
        upstream_name: repo
        registry: quayio
        include_tags:
          - latest
          - 0.0.1
        state: present
        ah_host: hub.example.com
        ah_username: admin
        ah_password: Sup3r53cr3t
      no_log: True






Authors
~~~~~~~

- Herve Quatremain (@herve4m)



Collection links
~~~~~~~~~~~~~~~~

* `Issue Tracker <https://github.com/ansible-collections/ansible\_hub/issues>`__
* `Repository (Sources) <https://github.com/ansible-collections/ansible\_hub>`__
* `Report an issue <https://github.com/ansible-collections/ansible\_hub/issues/new/choose>`__
