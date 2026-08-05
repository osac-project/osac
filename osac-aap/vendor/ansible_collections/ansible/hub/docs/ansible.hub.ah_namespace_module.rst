.. Created with antsibull-docs 2.14.0

ansible.hub.ah_namespace module -- create, update, or destroy Automation Hub Namespace
++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++

This module is part of the `ansible.hub collection <https://galaxy.ansible.com/ui/repo/published/ansible/hub/>`_ (version 1.0.0).

It is not included in ``ansible-core``.
To check whether it is installed, run ``ansible-galaxy collection list``.

To install it, use: :code:`ansible-galaxy collection install ansible.hub`.

To use it in a playbook, specify: ``ansible.hub.ah_namespace``.


.. contents::
   :local:
   :depth: 1


Synopsis
--------

- Create, update, or destroy Automation Hub Namespace.








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
    <td colspan="2" valign="top">
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
      <p>For Automation Hub this is <code class="ansible-value literal notranslate">galaxy</code></p>
      <p style="margin-top: 8px;"><b style="color: blue;">Default:</b> <code style="color: blue;">&#34;galaxy&#34;</code></p>
    </td>
  </tr>
  <tr>
    <td colspan="2" valign="top">
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
    <td colspan="2" valign="top">
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
    <td colspan="2" valign="top">
      <div class="ansibleOptionAnchor" id="parameter-avatar_url"></div>
      <p style="display: inline;"><strong>avatar_url</strong></p>
      <a class="ansibleOptionLink" href="#parameter-avatar_url" title="Permalink to this option"></a>
      <p style="font-size: small; margin-bottom: 0;">
        <span style="color: purple;">string</span>
      </p>
    </td>
    <td valign="top">
      <p>Namespace logo URL.</p>
    </td>
  </tr>
  <tr>
    <td colspan="2" valign="top">
      <div class="ansibleOptionAnchor" id="parameter-company"></div>
      <p style="display: inline;"><strong>company</strong></p>
      <a class="ansibleOptionLink" href="#parameter-company" title="Permalink to this option"></a>
      <p style="font-size: small; margin-bottom: 0;">
        <span style="color: purple;">string</span>
      </p>
    </td>
    <td valign="top">
      <p>Namespace owner company name.</p>
    </td>
  </tr>
  <tr>
    <td colspan="2" valign="top">
      <div class="ansibleOptionAnchor" id="parameter-description"></div>
      <p style="display: inline;"><strong>description</strong></p>
      <a class="ansibleOptionLink" href="#parameter-description" title="Permalink to this option"></a>
      <p style="font-size: small; margin-bottom: 0;">
        <span style="color: purple;">string</span>
      </p>
    </td>
    <td valign="top">
      <p>Description to use for the namespace.</p>
    </td>
  </tr>
  <tr>
    <td colspan="2" valign="top">
      <div class="ansibleOptionAnchor" id="parameter-email"></div>
      <p style="display: inline;"><strong>email</strong></p>
      <a class="ansibleOptionLink" href="#parameter-email" title="Permalink to this option"></a>
      <p style="font-size: small; margin-bottom: 0;">
        <span style="color: purple;">string</span>
      </p>
    </td>
    <td valign="top">
      <p>Namespace contact email.</p>
    </td>
  </tr>
  <tr>
    <td colspan="2" valign="top">
      <div class="ansibleOptionAnchor" id="parameter-groups"></div>
      <p style="display: inline;"><strong>groups</strong></p>
      <a class="ansibleOptionLink" href="#parameter-groups" title="Permalink to this option"></a>
      <p style="font-size: small; margin-bottom: 0;">
        <span style="color: purple;">list</span>
        / <span style="color: purple;">elements=string</span>
      </p>
    </td>
    <td valign="top">
      <p>A list of groups that have privileges on the Namespace.</p>
      <p style="margin-top: 8px;"><b style="color: blue;">Default:</b> <code style="color: blue;">[]</code></p>
    </td>
  </tr>
  <tr>
    <td colspan="2" valign="top">
      <div class="ansibleOptionAnchor" id="parameter-links"></div>
      <p style="display: inline;"><strong>links</strong></p>
      <a class="ansibleOptionLink" href="#parameter-links" title="Permalink to this option"></a>
      <p style="font-size: small; margin-bottom: 0;">
        <span style="color: purple;">list</span>
        / <span style="color: purple;">elements=dictionary</span>
      </p>
    </td>
    <td valign="top">
      <p>A list of dictionaries of Name and url values for links related the Namespace.</p>
    </td>
  </tr>
  <tr>
    <td></td>
    <td valign="top">
      <div class="ansibleOptionAnchor" id="parameter-links/name"></div>
      <p style="display: inline;"><strong>name</strong></p>
      <a class="ansibleOptionLink" href="#parameter-links/name" title="Permalink to this option"></a>
      <p style="font-size: small; margin-bottom: 0;">
        <span style="color: purple;">string</span>
        / <span style="color: red;">required</span>
      </p>
    </td>
    <td valign="top">
      <p>Link Text.</p>
    </td>
  </tr>
  <tr>
    <td></td>
    <td valign="top">
      <div class="ansibleOptionAnchor" id="parameter-links/url"></div>
      <p style="display: inline;"><strong>url</strong></p>
      <a class="ansibleOptionLink" href="#parameter-links/url" title="Permalink to this option"></a>
      <p style="font-size: small; margin-bottom: 0;">
        <span style="color: purple;">string</span>
        / <span style="color: red;">required</span>
      </p>
    </td>
    <td valign="top">
      <p>Link URL.</p>
    </td>
  </tr>

  <tr>
    <td colspan="2" valign="top">
      <div class="ansibleOptionAnchor" id="parameter-name"></div>
      <p style="display: inline;"><strong>name</strong></p>
      <a class="ansibleOptionLink" href="#parameter-name" title="Permalink to this option"></a>
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
    <td colspan="2" valign="top">
      <div class="ansibleOptionAnchor" id="parameter-new_name"></div>
      <p style="display: inline;"><strong>new_name</strong></p>
      <a class="ansibleOptionLink" href="#parameter-new_name" title="Permalink to this option"></a>
      <p style="font-size: small; margin-bottom: 0;">
        <span style="color: purple;">string</span>
      </p>
    </td>
    <td valign="top">
      <p>Setting this option will change the existing name (looked up via the name field).</p>
    </td>
  </tr>
  <tr>
    <td colspan="2" valign="top">
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
    <td colspan="2" valign="top">
      <div class="ansibleOptionAnchor" id="parameter-resources"></div>
      <p style="display: inline;"><strong>resources</strong></p>
      <a class="ansibleOptionLink" href="#parameter-resources" title="Permalink to this option"></a>
      <p style="font-size: small; margin-bottom: 0;">
        <span style="color: purple;">string</span>
      </p>
    </td>
    <td valign="top">
      <p>Namespace resource page in Markdown format.</p>
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
      <p>Desired state of the resource.</p>
      <p style="margin-top: 8px;"><b">Choices:</b></p>
      <ul>
        <li><p><code style="color: blue;"><b>&#34;present&#34;</b></code> <span style="color: blue;">← (default)</span></p></li>
        <li><p><code>&#34;absent&#34;</code></p></li>
      </ul>

    </td>
  </tr>
  <tr>
    <td colspan="2" valign="top">
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

    - name: Create Hub Namespace template
      ansible.hub.ah_namespace:
        name: Redhat
        company: Redhat
        email: user@example.com
        avatar_url: https://pnt.redhat.com/pnt/d-11633955/LogoRedHatHatColorRGB.png
        description: This is the Redhat Namespace
        links:
          - name: "homepage"
            url: "https://www.redhat.com"
        groups:
          - system:partner-engineers
          - admins






Authors
~~~~~~~

- Sean Sullivan (@sean-m-sullivan)



Collection links
~~~~~~~~~~~~~~~~

* `Issue Tracker <https://github.com/ansible-collections/ansible\_hub/issues>`__
* `Repository (Sources) <https://github.com/ansible-collections/ansible\_hub>`__
* `Report an issue <https://github.com/ansible-collections/ansible\_hub/issues/new/choose>`__
