.. Created with antsibull-docs 2.14.0

ansible.hub.ah_ee_image module -- Manage private automation hub execution environment images
++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++

This module is part of the `ansible.hub collection <https://galaxy.ansible.com/ui/repo/published/ansible/hub/>`_ (version 1.0.0).

It is not included in ``ansible-core``.
To check whether it is installed, run ``ansible-galaxy collection list``.

To install it, use: :code:`ansible-galaxy collection install ansible.hub`.

To use it in a playbook, specify: ``ansible.hub.ah_ee_image``.


.. contents::
   :local:
   :depth: 1


Synopsis
--------

- Delete execution environment images.
- Update container image tags.








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
      <div class="ansibleOptionAnchor" id="parameter-append"></div>
      <p style="display: inline;"><strong>append</strong></p>
      <a class="ansibleOptionLink" href="#parameter-append" title="Permalink to this option"></a>
      <p style="font-size: small; margin-bottom: 0;">
        <span style="color: purple;">boolean</span>
      </p>
    </td>
    <td valign="top">
      <p>If <code class="ansible-value literal notranslate">yes</code>, then add the tags specified in <code class="ansible-option literal notranslate"><strong><a class="reference internal" href="#parameter-tags"><span class="std std-ref"><span class="pre">tags</span></span></a></strong></code> to the image.</p>
      <p>If <code class="ansible-value literal notranslate">no</code>, then sets the image tags to the given list and removes all the other existing tags.</p>
      <p style="margin-top: 8px;"><b">Choices:</b></p>
      <ul>
        <li><p><code>false</code></p></li>
        <li><p><code style="color: blue;"><b>true</b></code> <span style="color: blue;">← (default)</span></p></li>
      </ul>

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
      <p>Name and tag of the image to remove or modify.</p>
      <p>Use a colon character between the image name and the tag.</p>
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
      <p>If <code class="ansible-value literal notranslate">absent</code>, then deletes the image and all its tags.</p>
      <p>If <code class="ansible-value literal notranslate">present</code>, then updates the image tags.</p>
      <p style="margin-top: 8px;"><b">Choices:</b></p>
      <ul>
        <li><p><code>&#34;absent&#34;</code></p></li>
        <li><p><code style="color: blue;"><b>&#34;present&#34;</b></code> <span style="color: blue;">← (default)</span></p></li>
      </ul>

    </td>
  </tr>
  <tr>
    <td valign="top">
      <div class="ansibleOptionAnchor" id="parameter-tags"></div>
      <p style="display: inline;"><strong>tags</strong></p>
      <a class="ansibleOptionLink" href="#parameter-tags" title="Permalink to this option"></a>
      <p style="font-size: small; margin-bottom: 0;">
        <span style="color: purple;">list</span>
        / <span style="color: purple;">elements=string</span>
      </p>
    </td>
    <td valign="top">
      <p>List of the image tags to update.</p>
      <p>Only used when <code class="ansible-option-value literal notranslate"><a class="reference internal" href="#parameter-state"><span class="std std-ref"><span class="pre">state=present</span></span></a></code>, otherwise ignored.</p>
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
- Only works with private automation hub v4.3.2 or later.


Examples
--------

.. code-block:: yaml

    - name: Ensure the image has the additional tags
      ansible.hub.ah_ee_image:
        name: ansible-automation-platform-20-early-access/ee-supported-rhel8:2.0.0-15
        state: present
        tags:
          - v2
          - "2.0"
          - prod1
        ah_host: hub.example.com
        ah_username: admin
        ah_password: Sup3r53cr3t
      no_log: yes

    - name: Replace all the image tags
      ansible.hub.ah_ee_image:
        name: ansible-automation-platform-20-early-access/ee-supported-rhel8:2.0.0-15
        state: present
        append: false
        tags:
          - prod2
          - "2.0"
        ah_host: hub.example.com
        ah_username: admin
        ah_password: Sup3r53cr3t
      no_log: yes

    - name: Ensure the image does not exist
      ansible.hub.ah_ee_image:
        name: ansible-automation-platform-20-early-access/ee-supported-rhel8:2.0
        state: absent
        ah_host: hub.example.com
        ah_username: admin
        ah_password: Sup3r53cr3t
      no_log: yes






Authors
~~~~~~~

- Herve Quatremain (@herve4m)



Collection links
~~~~~~~~~~~~~~~~

* `Issue Tracker <https://github.com/ansible-collections/ansible\_hub/issues>`__
* `Repository (Sources) <https://github.com/ansible-collections/ansible\_hub>`__
* `Report an issue <https://github.com/ansible-collections/ansible\_hub/issues/new/choose>`__
