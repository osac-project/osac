.. Created with antsibull-docs 2.14.0

ansible.hub.ah_build module -- Build a collection tar
+++++++++++++++++++++++++++++++++++++++++++++++++++++

This module is part of the `ansible.hub collection <https://galaxy.ansible.com/ui/repo/published/ansible/hub/>`_ (version 1.0.0).

It is not included in ``ansible-core``.
To check whether it is installed, run ``ansible-galaxy collection list``.

To install it, use: :code:`ansible-galaxy collection install ansible.hub`.

To use it in a playbook, specify: ``ansible.hub.ah_build``.


.. contents::
   :local:
   :depth: 1


Synopsis
--------

- Build a collection tar archive ready for uploading on the Automation Hub.








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
      <div class="ansibleOptionAnchor" id="parameter-force"></div>
      <p style="display: inline;"><strong>force</strong></p>
      <a class="ansibleOptionLink" href="#parameter-force" title="Permalink to this option"></a>
      <p style="font-size: small; margin-bottom: 0;">
        <span style="color: purple;">boolean</span>
      </p>
    </td>
    <td valign="top">
      <p>Whether to force the build to take place.</p>
      <p style="margin-top: 8px;"><b">Choices:</b></p>
      <ul>
        <li><p><code style="color: blue;"><b>false</b></code> <span style="color: blue;">← (default)</span></p></li>
        <li><p><code>true</code></p></li>
      </ul>

    </td>
  </tr>
  <tr>
    <td valign="top">
      <div class="ansibleOptionAnchor" id="parameter-output_path"></div>
      <p style="display: inline;"><strong>output_path</strong></p>
      <a class="ansibleOptionLink" href="#parameter-output_path" title="Permalink to this option"></a>
      <p style="font-size: small; margin-bottom: 0;">
        <span style="color: purple;">string</span>
      </p>
    </td>
    <td valign="top">
      <p>The path in which the collection is built to.</p>
      <p>The default is the current working directory.</p>
      <p style="margin-top: 8px;"><b style="color: blue;">Default:</b> <code style="color: blue;">&#34;.&#34;</code></p>
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
      <p>Path to the collection(s) directory to build.</p>
      <p>This should be the directory that contains the galaxy.yml file.</p>
      <p>The default is the current working directory.</p>
      <p style="margin-top: 8px;"><b style="color: blue;">Default:</b> <code style="color: blue;">&#34;.&#34;</code></p>
    </td>
  </tr>
  </tbody>
  </table>






Examples
--------

.. code-block:: yaml

    - name: Build ansible.hub:v1.0.0
      ansible.hub.ah_build:
        path: /home/ansible/ah_configuration
        force: true
        output_path: /var/tmp






Authors
~~~~~~~

- Tom Page (@Tompage1994)



Collection links
~~~~~~~~~~~~~~~~

* `Issue Tracker <https://github.com/ansible-collections/ansible\_hub/issues>`__
* `Repository (Sources) <https://github.com/ansible-collections/ansible\_hub>`__
* `Report an issue <https://github.com/ansible-collections/ansible\_hub/issues/new/choose>`__
