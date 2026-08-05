.. Created with antsibull-docs 2.14.0

ansible.hub.ah_ee_registry module -- Manage private automation hub execution environment remote registries
++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++

This module is part of the `ansible.hub collection <https://galaxy.ansible.com/ui/repo/published/ansible/hub/>`_ (version 1.0.0).

It is not included in ``ansible-core``.
To check whether it is installed, run ``ansible-galaxy collection list``.

To install it, use: :code:`ansible-galaxy collection install ansible.hub`.

To use it in a playbook, specify: ``ansible.hub.ah_ee_registry``.


.. contents::
   :local:
   :depth: 1


Synopsis
--------

- Update and delete execution environment remote registries.
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
      <div class="ansibleOptionAnchor" id="parameter-ca_cert"></div>
      <p style="display: inline;"><strong>ca_cert</strong></p>
      <a class="ansibleOptionLink" href="#parameter-ca_cert" title="Permalink to this option"></a>
      <p style="font-size: small; margin-bottom: 0;">
        <span style="color: purple;">string</span>
      </p>
    </td>
    <td valign="top">
      <p>A PEM encoded CA certificate used for authentication.</p>
      <p>Mutually exclusive with <code class="ansible-option literal notranslate"><strong><a class="reference internal" href="#parameter-ca_cert_path"><span class="std std-ref"><span class="pre">ca_cert_path</span></span></a></strong></code>.</p>
    </td>
  </tr>
  <tr>
    <td valign="top">
      <div class="ansibleOptionAnchor" id="parameter-ca_cert_path"></div>
      <p style="display: inline;"><strong>ca_cert_path</strong></p>
      <a class="ansibleOptionLink" href="#parameter-ca_cert_path" title="Permalink to this option"></a>
      <p style="font-size: small; margin-bottom: 0;">
        <span style="color: purple;">string</span>
      </p>
    </td>
    <td valign="top">
      <p>Path to a PEM encoded CA certificate used for authentication.</p>
      <p>Mutually exclusive with <code class="ansible-option literal notranslate"><strong><a class="reference internal" href="#parameter-ca_cert"><span class="std std-ref"><span class="pre">ca_cert</span></span></a></strong></code>.</p>
    </td>
  </tr>
  <tr>
    <td valign="top">
      <div class="ansibleOptionAnchor" id="parameter-client_cert"></div>
      <p style="display: inline;"><strong>client_cert</strong></p>
      <a class="ansibleOptionLink" href="#parameter-client_cert" title="Permalink to this option"></a>
      <p style="font-size: small; margin-bottom: 0;">
        <span style="color: purple;">string</span>
      </p>
    </td>
    <td valign="top">
      <p>A PEM encoded client certificate used for authentication.</p>
      <p>Mutually exclusive with <code class="ansible-option literal notranslate"><strong><a class="reference internal" href="#parameter-client_cert_path"><span class="std std-ref"><span class="pre">client_cert_path</span></span></a></strong></code>.</p>
    </td>
  </tr>
  <tr>
    <td valign="top">
      <div class="ansibleOptionAnchor" id="parameter-client_cert_path"></div>
      <p style="display: inline;"><strong>client_cert_path</strong></p>
      <a class="ansibleOptionLink" href="#parameter-client_cert_path" title="Permalink to this option"></a>
      <p style="font-size: small; margin-bottom: 0;">
        <span style="color: purple;">string</span>
      </p>
    </td>
    <td valign="top">
      <p>Path to a PEM encoded client certificate used for authentication.</p>
      <p>Mutually exclusive with <code class="ansible-option literal notranslate"><strong><a class="reference internal" href="#parameter-client_cert"><span class="std std-ref"><span class="pre">client_cert</span></span></a></strong></code>.</p>
    </td>
  </tr>
  <tr>
    <td valign="top">
      <div class="ansibleOptionAnchor" id="parameter-client_key"></div>
      <p style="display: inline;"><strong>client_key</strong></p>
      <a class="ansibleOptionLink" href="#parameter-client_key" title="Permalink to this option"></a>
      <p style="font-size: small; margin-bottom: 0;">
        <span style="color: purple;">string</span>
      </p>
    </td>
    <td valign="top">
      <p>A PEM encoded private key file used for authentication.</p>
      <p>Mutually exclusive with <code class="ansible-option literal notranslate"><strong><a class="reference internal" href="#parameter-client_key_path"><span class="std std-ref"><span class="pre">client_key_path</span></span></a></strong></code>.</p>
    </td>
  </tr>
  <tr>
    <td valign="top">
      <div class="ansibleOptionAnchor" id="parameter-client_key_path"></div>
      <p style="display: inline;"><strong>client_key_path</strong></p>
      <a class="ansibleOptionLink" href="#parameter-client_key_path" title="Permalink to this option"></a>
      <p style="font-size: small; margin-bottom: 0;">
        <span style="color: purple;">string</span>
      </p>
    </td>
    <td valign="top">
      <p>Path to a PEM encoded private key file used for authentication.</p>
      <p>Mutually exclusive with <code class="ansible-option literal notranslate"><strong><a class="reference internal" href="#parameter-client_key"><span class="std std-ref"><span class="pre">client_key</span></span></a></strong></code>.</p>
    </td>
  </tr>
  <tr>
    <td valign="top">
      <div class="ansibleOptionAnchor" id="parameter-download_concurrency"></div>
      <p style="display: inline;"><strong>download_concurrency</strong></p>
      <a class="ansibleOptionLink" href="#parameter-download_concurrency" title="Permalink to this option"></a>
      <p style="font-size: small; margin-bottom: 0;">
        <span style="color: purple;">string</span>
      </p>
    </td>
    <td valign="top">
      <p>Number of concurrent collections to download.</p>
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
      <p>Name of the registry to remove or modify.</p>
    </td>
  </tr>
  <tr>
    <td valign="top">
      <div class="ansibleOptionAnchor" id="parameter-password"></div>
      <p style="display: inline;"><strong>password</strong></p>
      <a class="ansibleOptionLink" href="#parameter-password" title="Permalink to this option"></a>
      <p style="font-size: small; margin-bottom: 0;">
        <span style="color: purple;">string</span>
      </p>
    </td>
    <td valign="top">
      <p>The password to authenticate to the registry with.</p>
    </td>
  </tr>
  <tr>
    <td valign="top">
      <div class="ansibleOptionAnchor" id="parameter-proxy_password"></div>
      <p style="display: inline;"><strong>proxy_password</strong></p>
      <a class="ansibleOptionLink" href="#parameter-proxy_password" title="Permalink to this option"></a>
      <p style="font-size: small; margin-bottom: 0;">
        <span style="color: purple;">string</span>
      </p>
    </td>
    <td valign="top">
      <p>Password for the username to use for the proxy connection.</p>
    </td>
  </tr>
  <tr>
    <td valign="top">
      <div class="ansibleOptionAnchor" id="parameter-proxy_url"></div>
      <p style="display: inline;"><strong>proxy_url</strong></p>
      <a class="ansibleOptionLink" href="#parameter-proxy_url" title="Permalink to this option"></a>
      <p style="font-size: small; margin-bottom: 0;">
        <span style="color: purple;">string</span>
      </p>
    </td>
    <td valign="top">
      <p>Proxy URL to use for the connection.</p>
    </td>
  </tr>
  <tr>
    <td valign="top">
      <div class="ansibleOptionAnchor" id="parameter-proxy_username"></div>
      <p style="display: inline;"><strong>proxy_username</strong></p>
      <a class="ansibleOptionLink" href="#parameter-proxy_username" title="Permalink to this option"></a>
      <p style="font-size: small; margin-bottom: 0;">
        <span style="color: purple;">string</span>
      </p>
    </td>
    <td valign="top">
      <p>Username to use for the proxy connection.</p>
    </td>
  </tr>
  <tr>
    <td valign="top">
      <div class="ansibleOptionAnchor" id="parameter-rate_limit"></div>
      <p style="display: inline;"><strong>rate_limit</strong></p>
      <a class="ansibleOptionLink" href="#parameter-rate_limit" title="Permalink to this option"></a>
      <p style="font-size: small; margin-bottom: 0;">
        <span style="color: purple;">string</span>
      </p>
    </td>
    <td valign="top">
      <p>Limits total download rate in requests per second.</p>
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
      <p>If <code class="ansible-value literal notranslate">absent</code>, then the module deletes the registry.</p>
      <p>The module does not fail if the registry does not exist because the state is already as expected.</p>
      <p>If <code class="ansible-value literal notranslate">present</code>, then the module updates the description and README file for the registry.</p>
      <p style="margin-top: 8px;"><b">Choices:</b></p>
      <ul>
        <li><p><code>&#34;absent&#34;</code></p></li>
        <li><p><code style="color: blue;"><b>&#34;present&#34;</b></code> <span style="color: blue;">← (default)</span></p></li>
      </ul>

    </td>
  </tr>
  <tr>
    <td valign="top">
      <div class="ansibleOptionAnchor" id="parameter-tls_validation"></div>
      <p style="display: inline;"><strong>tls_validation</strong></p>
      <a class="ansibleOptionLink" href="#parameter-tls_validation" title="Permalink to this option"></a>
      <p style="font-size: small; margin-bottom: 0;">
        <span style="color: purple;">boolean</span>
      </p>
    </td>
    <td valign="top">
      <p>Whether to validate TLS when connecting to the remote registry.</p>
      <p style="margin-top: 8px;"><b">Choices:</b></p>
      <ul>
        <li><p><code>false</code></p></li>
        <li><p><code style="color: blue;"><b>true</b></code> <span style="color: blue;">← (default)</span></p></li>
      </ul>

    </td>
  </tr>
  <tr>
    <td valign="top">
      <div class="ansibleOptionAnchor" id="parameter-url"></div>
      <p style="display: inline;"><strong>url</strong></p>
      <a class="ansibleOptionLink" href="#parameter-url" title="Permalink to this option"></a>
      <p style="font-size: small; margin-bottom: 0;">
        <span style="color: purple;">string</span>
      </p>
    </td>
    <td valign="top">
      <p>The URL of the remote registry.</p>
      <p>Required if <code class="ansible-option-value literal notranslate"><a class="reference internal" href="#parameter-state"><span class="std std-ref"><span class="pre">state=present</span></span></a></code>.</p>
    </td>
  </tr>
  <tr>
    <td valign="top">
      <div class="ansibleOptionAnchor" id="parameter-username"></div>
      <p style="display: inline;"><strong>username</strong></p>
      <a class="ansibleOptionLink" href="#parameter-username" title="Permalink to this option"></a>
      <p style="font-size: small; margin-bottom: 0;">
        <span style="color: purple;">string</span>
      </p>
    </td>
    <td valign="top">
      <p>The username to authenticate to the registry with.</p>
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






Examples
--------

.. code-block:: yaml

    - name: Add a remote registry to AH
      ansible.hub.ah_ee_registry:
        name: my_quayio
        state: present
        url: https://quay.io/my/registry
        ah_host: hub.example.com
        ah_username: admin
        ah_password: Sup3r53cr3t
      no_log: True

    - name: Add a remote registry which requires auth to AH
      ansible.hub.ah_ee_registry:
        name: my_quayio_auth
        state: present
        url: https://quay.io/my/registry
        username: myuser
        password: mypassword
        ah_host: hub.example.com
        ah_username: admin
        ah_password: Sup3r53cr3t
      no_log: True

    - name: Remove a remote registry from AH
      ansible.hub.ah_ee_registry:
        name: examplehub
        state: absent
        ah_host: hub.example.com
        ah_username: admin
        ah_password: Sup3r53cr3t
      no_log: True






Authors
~~~~~~~

- Tom Page (@Tompage1994)



Collection links
~~~~~~~~~~~~~~~~

* `Issue Tracker <https://github.com/ansible-collections/ansible\_hub/issues>`__
* `Repository (Sources) <https://github.com/ansible-collections/ansible\_hub>`__
* `Report an issue <https://github.com/ansible-collections/ansible\_hub/issues/new/choose>`__
