.. Created with antsibull-docs 2.14.0

ansible.hub.ah_collection module -- Update, or destroy Automation Hub Collections
+++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++

This module is part of the `ansible.hub collection <https://galaxy.ansible.com/ui/repo/published/ansible/hub/>`_ (version 1.0.0).

It is not included in ``ansible-core``.
To check whether it is installed, run ``ansible-galaxy collection list``.

To install it, use: :code:`ansible-galaxy collection install ansible.hub`.

To use it in a playbook, specify: ``ansible.hub.ah_collection``.


.. contents::
   :local:
   :depth: 1


Synopsis
--------

- Upload, or destroy Automation Hub Collections.








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
      <p>For Automation Hub this is <code class="ansible-value literal notranslate">galaxy</code></p>
      <p style="margin-top: 8px;"><b style="color: blue;">Default:</b> <code style="color: blue;">&#34;galaxy&#34;</code></p>
    </td>
  </tr>
  <tr>
    <td valign="top">
      <div class="ansibleOptionAnchor" id="parameter-ah_token"></div>
      <p style="display: inline;"><strong>ah_token</strong></p>
      <a class="ansibleOptionLink" href="#parameter-ah_token" title="Permalink to this option"></a>
      <p style="font-size: small; margin-bottom: 0;">
        <span style="color: purple;">any</span>
      </p>
    </td>
    <td valign="top">
      <p>The Ansible Automation Hub API token to use.</p>
      <p>This value can be in one of two formats.</p>
      <p>A string which is the token itself. (for example, bqV5txm97wqJqtkxlMkhQz0pKhRMMX)</p>
      <p>A dictionary structure as returned by the ah_token module.</p>
      <p>If value not set, will try environment variable <code class="xref std std-envvar literal notranslate">AH_API_TOKEN</code>.</p>
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
      <div class="ansibleOptionAnchor" id="parameter-auto_approve"></div>
      <p style="display: inline;"><strong>auto_approve</strong></p>
      <a class="ansibleOptionLink" href="#parameter-auto_approve" title="Permalink to this option"></a>
      <p style="font-size: small; margin-bottom: 0;">
        <span style="color: purple;">boolean</span>
      </p>
    </td>
    <td valign="top">
      <p>Approves a collection.</p>
      <p>Requires version to be set.</p>
      <p style="margin-top: 8px;"><b">Choices:</b></p>
      <ul>
        <li><p><code>false</code></p></li>
        <li><p><code style="color: blue;"><b>true</b></code> <span style="color: blue;">← (default)</span></p></li>
      </ul>

    </td>
  </tr>
  <tr>
    <td valign="top">
      <div class="ansibleOptionAnchor" id="parameter-interval"></div>
      <p style="display: inline;"><strong>interval</strong></p>
      <a class="ansibleOptionLink" href="#parameter-interval" title="Permalink to this option"></a>
      <p style="font-size: small; margin-bottom: 0;">
        <span style="color: purple;">float</span>
      </p>
    </td>
    <td valign="top">
      <p>The interval to request an update from Automation Hub when waiting for the approval.</p>
      <p style="margin-top: 8px;"><b style="color: blue;">Default:</b> <code style="color: blue;">10.0</code></p>
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
      <p>Collection name.</p>
      <p>Must be lower case containing only alphanumeric characters and underscores.</p>
    </td>
  </tr>
  <tr>
    <td valign="top">
      <div class="ansibleOptionAnchor" id="parameter-namespace"></div>
      <p style="display: inline;"><strong>namespace</strong></p>
      <a class="ansibleOptionLink" href="#parameter-namespace" title="Permalink to this option"></a>
      <p style="font-size: small; margin-bottom: 0;">
        <span style="color: purple;">string</span>
        / <span style="color: red;">required</span>
      </p>
    </td>
    <td valign="top">
      <p>Namespace name.</p>
      <p>Must be lower case containing only alphanumeric characters and underscores.</p>
    </td>
  </tr>
  <tr>
    <td valign="top">
      <div class="ansibleOptionAnchor" id="parameter-overwrite_existing"></div>
      <p style="display: inline;"><strong>overwrite_existing</strong></p>
      <a class="ansibleOptionLink" href="#parameter-overwrite_existing" title="Permalink to this option"></a>
      <p style="font-size: small; margin-bottom: 0;">
        <span style="color: purple;">boolean</span>
      </p>
    </td>
    <td valign="top">
      <p>Overwrites an existing collection.</p>
      <p>Requires version to be set.</p>
      <p style="margin-top: 8px;"><b">Choices:</b></p>
      <ul>
        <li><p><code style="color: blue;"><b>false</b></code> <span style="color: blue;">← (default)</span></p></li>
        <li><p><code>true</code></p></li>
      </ul>

    </td>
  </tr>
  <tr>
    <td valign="top">
      <div class="ansibleOptionAnchor" id="parameter-path"></div>
      <p style="display: inline;"><strong>path</strong></p>
      <a class="ansibleOptionLink" href="#parameter-path" title="Permalink to this option"></a>
      <p style="font-size: small; margin-bottom: 0;">
        <span style="color: purple;">string</span>
      </p>
    </td>
    <td valign="top">
      <p>Collection artifact file path.</p>
      <p>If version is not specified, version will be derived from the file name.</p>
    </td>
  </tr>
  <tr>
    <td valign="top">
      <div class="ansibleOptionAnchor" id="parameter-repository"></div>
      <p style="display: inline;"><strong>repository</strong></p>
      <a class="ansibleOptionLink" href="#parameter-repository" title="Permalink to this option"></a>
      <p style="font-size: small; margin-bottom: 0;">
        <span style="color: purple;">string</span>
      </p>
    </td>
    <td valign="top">
      <p>Name of the destination collection for the repository (staging for uploads).</p>
      <p style="margin-top: 8px;"><b style="color: blue;">Default:</b> <code style="color: blue;">&#34;staging&#34;</code></p>
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
      <p>Desired state of the resource.</p>
      <p>If present with a path, will upload a collection artifact to Automation hub.</p>
      <p>If present will return data on a collection.</p>
      <p>If present with version, will return data on a collection version.</p>
      <p>If absent without version, will delete the collection and all versions.</p>
      <p>If absent with version, will delete only specified version.</p>
      <p style="margin-top: 8px;"><b">Choices:</b></p>
      <ul>
        <li><p><code style="color: blue;"><b>&#34;present&#34;</b></code> <span style="color: blue;">← (default)</span></p></li>
        <li><p><code>&#34;absent&#34;</code></p></li>
      </ul>

    </td>
  </tr>
  <tr>
    <td valign="top">
      <div class="ansibleOptionAnchor" id="parameter-timeout"></div>
      <p style="display: inline;"><strong>timeout</strong></p>
      <a class="ansibleOptionLink" href="#parameter-timeout" title="Permalink to this option"></a>
      <p style="font-size: small; margin-bottom: 0;">
        <span style="color: purple;">integer</span>
      </p>
    </td>
    <td valign="top">
      <p>Waiting for the approval will abort after this amount of seconds.</p>
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
  <tr>
    <td valign="top">
      <div class="ansibleOptionAnchor" id="parameter-version"></div>
      <p style="display: inline;"><strong>version</strong></p>
      <a class="ansibleOptionLink" href="#parameter-version" title="Permalink to this option"></a>
      <p style="font-size: small; margin-bottom: 0;">
        <span style="color: purple;">string</span>
      </p>
    </td>
    <td valign="top">
      <p>Collection Version.</p>
      <p>Must be lower case containing only alphanumeric characters and underscores.</p>
    </td>
  </tr>
  <tr>
    <td valign="top">
      <div class="ansibleOptionAnchor" id="parameter-wait"></div>
      <p style="display: inline;"><strong>wait</strong></p>
      <a class="ansibleOptionLink" href="#parameter-wait" title="Permalink to this option"></a>
      <p style="font-size: small; margin-bottom: 0;">
        <span style="color: purple;">boolean</span>
      </p>
    </td>
    <td valign="top">
      <p>Waits for the collection to be uploaded.</p>
      <p style="margin-top: 8px;"><b">Choices:</b></p>
      <ul>
        <li><p><code>false</code></p></li>
        <li><p><code style="color: blue;"><b>true</b></code> <span style="color: blue;">← (default)</span></p></li>
      </ul>

    </td>
  </tr>
  </tbody>
  </table>






Examples
--------

.. code-block:: yaml

    - name: Upload collection to Automation Hub
      ansible.hub.ah_collection:
        namespace: awx
        name: awx
        path: /var/tmp/collections/awx-awx-15.0.0.tar.gz

    - name: Remove collection from Automation Hub
      ansible.hub.ah_collection:
        namespace: test_collection
        name: test
        version: 4.1.2
        state: absent






Authors
~~~~~~~

- Sean Sullivan (@sean-m-sullivan)
- Tom Page (@Tompage1994)



Collection links
~~~~~~~~~~~~~~~~

* `Issue Tracker <https://github.com/ansible-collections/ansible\_hub/issues>`__
* `Repository (Sources) <https://github.com/ansible-collections/ansible\_hub>`__
* `Report an issue <https://github.com/ansible-collections/ansible\_hub/issues/new/choose>`__
