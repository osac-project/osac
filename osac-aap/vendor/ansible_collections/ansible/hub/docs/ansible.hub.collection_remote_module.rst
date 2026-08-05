.. Created with antsibull-docs 2.14.0

ansible.hub.collection_remote module -- Configure a collection remote repository
++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++

This module is part of the `ansible.hub collection <https://galaxy.ansible.com/ui/repo/published/ansible/hub/>`_ (version 1.0.0).

It is not included in ``ansible-core``.
To check whether it is installed, run ``ansible-galaxy collection list``.

To install it, use: :code:`ansible-galaxy collection install ansible.hub`.

To use it in a playbook, specify: ``ansible.hub.collection_remote``.


.. contents::
   :local:
   :depth: 1


Synopsis
--------

- Configure an Automation Hub collection remote repository.
- Requires AAP 2.4 or Later








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
      <div class="ansibleOptionAnchor" id="parameter-auth_url"></div>
      <p style="display: inline;"><strong>auth_url</strong></p>
      <a class="ansibleOptionLink" href="#parameter-auth_url" title="Permalink to this option"></a>
      <p style="font-size: small; margin-bottom: 0;">
        <span style="color: purple;">string</span>
      </p>
    </td>
    <td valign="top">
      <p>Remote URL for the repository authentication if separate.</p>
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
      <p>Mutually exclusive with <code class="ansible-option literal notranslate"><strong><a class="reference internal" href="#parameter-ca_cert_path"><span class="std std-ref"><span class="pre">ca_cert_path</span></span></a></strong></code></p>
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
      <p>Mutually exclusive with <code class="ansible-option literal notranslate"><strong><a class="reference internal" href="#parameter-ca_cert"><span class="std std-ref"><span class="pre">ca_cert</span></span></a></strong></code></p>
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
      <p>Mutually exclusive with <code class="ansible-option literal notranslate"><strong><a class="reference internal" href="#parameter-client_cert_path"><span class="std std-ref"><span class="pre">client_cert_path</span></span></a></strong></code></p>
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
      <p>Mutually exclusive with <code class="ansible-option literal notranslate"><strong><a class="reference internal" href="#parameter-client_cert"><span class="std std-ref"><span class="pre">client_cert</span></span></a></strong></code></p>
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
      <p>Mutually exclusive with <code class="ansible-option literal notranslate"><strong><a class="reference internal" href="#parameter-client_key_path"><span class="std std-ref"><span class="pre">client_key_path</span></span></a></strong></code></p>
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
      <p>Mutually exclusive with <code class="ansible-option literal notranslate"><strong><a class="reference internal" href="#parameter-client_key"><span class="std std-ref"><span class="pre">client_key</span></span></a></strong></code></p>
    </td>
  </tr>
  <tr>
    <td valign="top">
      <div class="ansibleOptionAnchor" id="parameter-download_concurrency"></div>
      <p style="display: inline;"><strong>download_concurrency</strong></p>
      <a class="ansibleOptionLink" href="#parameter-download_concurrency" title="Permalink to this option"></a>
      <p style="font-size: small; margin-bottom: 0;">
        <span style="color: purple;">integer</span>
      </p>
    </td>
    <td valign="top">
      <p>Number of concurrent collections to download.</p>
      <p style="margin-top: 8px;"><b style="color: blue;">Default:</b> <code style="color: blue;">10</code></p>
    </td>
  </tr>
  <tr>
    <td valign="top">
      <div class="ansibleOptionAnchor" id="parameter-max_retries"></div>
      <p style="display: inline;"><strong>max_retries</strong></p>
      <a class="ansibleOptionLink" href="#parameter-max_retries" title="Permalink to this option"></a>
      <p style="font-size: small; margin-bottom: 0;">
        <span style="color: purple;">integer</span>
      </p>
    </td>
    <td valign="top">
      <p>Retries to use when running sync. Default is 0 which does not limit.</p>
      <p style="margin-top: 8px;"><b style="color: blue;">Default:</b> <code style="color: blue;">0</code></p>
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
      <p>Remote Repository name. Probably one of community validated, or rh-certified.</p>
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
      <p>Password to authenticate to the remote repository.</p>
    </td>
  </tr>
  <tr>
    <td valign="top">
      <div class="ansibleOptionAnchor" id="parameter-policy"></div>
      <p style="display: inline;"><strong>policy</strong></p>
      <a class="ansibleOptionLink" href="#parameter-policy" title="Permalink to this option"></a>
      <p style="font-size: small; margin-bottom: 0;">
        <span style="color: purple;">string</span>
      </p>
    </td>
    <td valign="top">
      <p>The policy to use when downloading content.</p>
      <p style="margin-top: 8px;"><b">Choices:</b></p>
      <ul>
        <li><p><code style="color: blue;"><b>&#34;immediate&#34;</b></code> <span style="color: blue;">← (default)</span></p></li>
        <li><p><code>&#34;When syncing, download all metadata and content now.&#34;</code></p></li>
      </ul>

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
      <p>Password to use for the username for the proxy connection.</p>
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
        <span style="color: purple;">integer</span>
      </p>
    </td>
    <td valign="top">
      <p>Limits total download rate in requests per second.</p>
      <p style="margin-top: 8px;"><b style="color: blue;">Default:</b> <code style="color: blue;">8</code></p>
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
      <div class="ansibleOptionAnchor" id="parameter-requirements"></div>
      <p style="display: inline;"><strong>requirements</strong></p>
      <a class="ansibleOptionLink" href="#parameter-requirements" title="Permalink to this option"></a>
      <p style="font-size: small; margin-bottom: 0;">
        <span style="color: purple;">list</span>
        / <span style="color: purple;">elements=string</span>
      </p>
    </td>
    <td valign="top">
      <p>Requirements to download from remote.</p>
    </td>
  </tr>
  <tr>
    <td valign="top">
      <div class="ansibleOptionAnchor" id="parameter-requirements_file"></div>
      <p style="display: inline;"><strong>requirements_file</strong></p>
      <a class="ansibleOptionLink" href="#parameter-requirements_file" title="Permalink to this option"></a>
      <p style="font-size: small; margin-bottom: 0;">
        <span style="color: purple;">string</span>
      </p>
    </td>
    <td valign="top">
      <p>A yaml requirements file to download from remote.</p>
    </td>
  </tr>
  <tr>
    <td valign="top">
      <div class="ansibleOptionAnchor" id="parameter-signed_only"></div>
      <p style="display: inline;"><strong>signed_only</strong></p>
      <a class="ansibleOptionLink" href="#parameter-signed_only" title="Permalink to this option"></a>
      <p style="font-size: small; margin-bottom: 0;">
        <span style="color: purple;">boolean</span>
      </p>
    </td>
    <td valign="top">
      <p>Whether to only download signed collections.</p>
      <p style="margin-top: 8px;"><b">Choices:</b></p>
      <ul>
        <li><p><code style="color: blue;"><b>false</b></code> <span style="color: blue;">← (default)</span></p></li>
        <li><p><code>true</code></p></li>
      </ul>

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
      <p>If <code class="ansible-value literal notranslate">absent</code>, then the module deletes the remote repository.</p>
      <p>The module does not fail if the remote repository does not exist because the state is already as expected.</p>
      <p>If <code class="ansible-value literal notranslate">present</code>, then the module updates the description and README file for the remote repository.</p>
      <p style="margin-top: 8px;"><b">Choices:</b></p>
      <ul>
        <li><p><code style="color: blue;"><b>&#34;present&#34;</b></code> <span style="color: blue;">← (default)</span></p></li>
        <li><p><code>&#34;absent&#34;</code></p></li>
      </ul>

    </td>
  </tr>
  <tr>
    <td valign="top">
      <div class="ansibleOptionAnchor" id="parameter-sync_dependencies"></div>
      <p style="display: inline;"><strong>sync_dependencies</strong></p>
      <a class="ansibleOptionLink" href="#parameter-sync_dependencies" title="Permalink to this option"></a>
      <p style="font-size: small; margin-bottom: 0;">
        <span style="color: purple;">boolean</span>
      </p>
    </td>
    <td valign="top">
      <p>Whether to download dependencies when syncing collections.</p>
      <p style="margin-top: 8px;"><b">Choices:</b></p>
      <ul>
        <li><p><code>false</code></p></li>
        <li><p><code style="color: blue;"><b>true</b></code> <span style="color: blue;">← (default)</span></p></li>
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
      <p>Whether to use TLS validation against the remote repository.</p>
      <p style="margin-top: 8px;"><b">Choices:</b></p>
      <ul>
        <li><p><code>false</code></p></li>
        <li><p><code style="color: blue;"><b>true</b></code> <span style="color: blue;">← (default)</span></p></li>
      </ul>

    </td>
  </tr>
  <tr>
    <td valign="top">
      <div class="ansibleOptionAnchor" id="parameter-token"></div>
      <p style="display: inline;"><strong>token</strong></p>
      <a class="ansibleOptionLink" href="#parameter-token" title="Permalink to this option"></a>
      <p style="font-size: small; margin-bottom: 0;">
        <span style="color: purple;">string</span>
      </p>
    </td>
    <td valign="top">
      <p>Token to authenticate to the remote repository.</p>
    </td>
  </tr>
  <tr>
    <td valign="top">
      <div class="ansibleOptionAnchor" id="parameter-url"></div>
      <p style="display: inline;"><strong>url</strong></p>
      <a class="ansibleOptionLink" href="#parameter-url" title="Permalink to this option"></a>
      <p style="font-size: small; margin-bottom: 0;">
        <span style="color: purple;">string</span>
        / <span style="color: red;">required</span>
      </p>
    </td>
    <td valign="top">
      <p>Remote URL for the repository.</p>
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
      <p>Username to authenticate to the remote repository.</p>
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

    - name: Configure rh-certified collection_remote
      collection_remote:
        name: rh-certified
        url: https://cloud.redhat.com/api/automation-hub/
        token: aabbcc
        auth_url: https://sso.redhat.com/auth/realms/redhat-external/protocol/openid-connect/token

    - name: Configure community collection_remote
      collection_remote:
        name: community
        url: https://galaxy.ansible.com/api/
        requirements:
          - galaxy.galaxy
          - infra.controller_configuration

    - name: Configure community collection_remote from a file
      collection_remote:
        name: community
        url: https://galaxy.ansible.com/api/
        requirements_file: "/tmp/requirements.yml"






Authors
~~~~~~~

- Tom Page (@Tompage1994)



Collection links
~~~~~~~~~~~~~~~~

* `Issue Tracker <https://github.com/ansible-collections/ansible\_hub/issues>`__
* `Repository (Sources) <https://github.com/ansible-collections/ansible\_hub>`__
* `Report an issue <https://github.com/ansible-collections/ansible\_hub/issues/new/choose>`__
