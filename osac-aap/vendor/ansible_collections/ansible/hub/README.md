# Ansible Collection for Automation Hub Configuration

## Description

This Ansible collection allows for easy interaction with an Ansible Automation Hub server via Ansible playbooks. It provides modules and plugins to manage collections, users, groups, and other Automation Hub resources, enabling automation administrators to configure and maintain their Hub instances efficiently.


## Installation


Before using this collection, install it from Automation Hub with the `ansible-galaxy` command-line tool:

```bash
ansible-galaxy collection install ansible.hub
```

You can also include it in a `requirements.yml` file and install it with `ansible-galaxy collection install -r requirements.yml`:

```yaml
---
collections:
  - name: ansible.hub
```

To upgrade to the latest available version:

```bash
ansible-galaxy collection install ansible.hub --upgrade
```

To install a specific version:

```bash
ansible-galaxy collection install ansible.hub:==1.0.1
```

See [Managing automation content](https://docs.redhat.com/en/documentation/red_hat_ansible_automation_platform/2.5/html/managing_automation_content/index) for more details.

## Use Cases

### 1. Collection Management
Automate the upload, approval, and management of collections in your Automation Hub:

```yaml
- name: Create namespace
  ansible.hub.ah_namespace:
    name: "organization"
    description: "Collections for my organization"
    state: present

- name: Upload collection to Hub
  ansible.hub.ah_collection:
    namespace: "organization"
    name: "collection"
    path: "/path/to/organization-collection.tar.gz"
    state: present
```

### 2. User and Group Administration
Manage users and groups across your Automation Hub instance:

```yaml
- name: Create group
  ansible.hub.ah_group:
    name: "developers"
    state: present
```


### 3. Namespace Configuration
Configure and manage namespaces for collection organization:

```yaml
- name: Create namespace
  ansible.hub.ah_namespace:
    name: "my_organization"
    description: "Collections for my organization"
    state: present
```

### 4. Token Management
Manage API tokens for automation workflows:

```yaml
- name: Create API token
  ansible.hub.ah_token:
    ah_username: "admin"
    ah_password: "admin"
    state: present
  register: ah_token
  no_log: false
```

## Contributing

The content of this collection is made by people like you, a community of individuals collaborating on making the world better through developing automation software.

We are actively accepting new contributors and all types of contributions are very welcome.

Don't know how to start? Refer to the [Ansible community guide](https://docs.ansible.com/ansible/devel/community/index.html)!

Want to submit code changes? Take a look at the [Quick-start development guide](https://docs.ansible.com/ansible/devel/community/create_pr_quick_start.html).

We also use the following guidelines:

* [Collection review checklist](https://docs.ansible.com/ansible/devel/community/collection_contributors/collection_reviewing.html)
* [Ansible development guide](https://docs.ansible.com/ansible/devel/dev_guide/index.html)
* [Ansible collection development guide](https://docs.ansible.com/ansible/devel/dev_guide/developing_collections.html#contributing-to-collections)

The current maintainers are listed in the [MAINTAINERS](https://github.com/ansible-collections/ansible_hub/blob/main/MAINTAINERS) file. If you have questions or need help, feel free to mention them in the proposals.

## Support

This collection is maintained by the Red Hat Ansible team.

As Red Hat Ansible Certified Content, this collection is entitled to support through the Ansible Automation Platform (AAP) using the **Create issue** button on the top right corner. If a support case cannot be opened with Red Hat and the collection has been obtained either from Galaxy or GitHub, there may community help available on the [Ansible Forum](https://forum.ansible.com/).


## Release Notes

See the [changelog](https://github.com/ansible-collections/ansible_hub/blob/main/CHANGELOG.rst) for release notes and version history.


## Related Information

- [Ansible Automation Hub Documentation](https://docs.ansible.com/ansible/latest/galaxy/user_guide.html)
- [Ansible user guide](https://docs.ansible.com/ansible/devel/user_guide/index.html)
- [Ansible developer guide](https://docs.ansible.com/ansible/devel/dev_guide/index.html)
- [Ansible collections requirements](https://docs.ansible.com/ansible/devel/community/collection_contributors/collection_requirements.html)
- [Ansible community Code of Conduct](https://docs.ansible.com/ansible/devel/community/code_of_conduct.html)
- [The Bullhorn (the Ansible contributor newsletter)](https://docs.ansible.com/ansible/devel/community/communication.html#the-bullhorn)
- [Important announcements for maintainers](https://github.com/ansible-collections/news-for-maintainers)

## License Information

GNU General Public License v3.0 or later.

See [LICENSE](https://www.gnu.org/licenses/gpl-3.0.txt) to see the full text.
