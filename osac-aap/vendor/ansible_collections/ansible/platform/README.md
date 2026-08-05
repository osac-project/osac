# Ansible Platform Collection

## Changelog for v2.5.20250326

* Added support for setting URL for applications

## Description

This collection contains modules that can be used to automate the creation of resources on an install of Ansible Automation Platform.


## Requirements

This collection supports python versions >=3.11 and requires an ansible-core version of >=2.16.0. 

It also requires an existing install of Ansible Automation Platform as a target. 


## Installation

Before using this collection, you need to install it with the Ansible Galaxy command-line tool:

```
ansible-galaxy collection install ansible.platform
```

You can also include it in a requirements.yml file and install it with ansible-galaxy collection install -r requirements.yml, using the format:


```yaml
collections:
  - name: ansible.platform.
```

Note that if you install any collections from Ansible Galaxy, they will not be upgraded automatically when you upgrade the Ansible package.
To upgrade the collection to the latest available version, run the following command:

```
ansible-galaxy collection install ansible.platform --upgrade
```

You can also install a specific version of the collection, for example, if you need to downgrade when something is broken in the latest version (please report an issue in this repository). Use the following syntax to install version 2.5.0:

```
ansible-galaxy collection install ansible.platform:==2.5.0
```

See [using Ansible collections](https://docs.ansible.com/ansible/devel/user_guide/collections_using.html) for more details.

## Use Cases

This collection can be used to automate to the creation of resources inside of the Ansible Automation Platform. Things such as users, organizations and teams can be created using this collection. 

Adding services (Controller, Event Driven Automation, Automation) can also be done with this collection. Nodes for those services can also be added. 


## Testing

This collection is tested using integration tests which can be called via `ansible-test integration`. If you wish to run the tests manually, we recommend using the parent Makefile via `make collection-test`. It will require a running version of Ansible Automation Platform.

The collection is tested against current version of Ansible Automation Platform.


## Support

This collection is supported by RedHat Engineering. Support cases can be opened at: https://access.redhat.com/support/

## Release Notes and Roadmap

Changelogs can be found in the changelogs directory. 


## Related Information

Please refer to Ansible Automation Platform Documentation for further documentation needs: https://docs.redhat.com/en/documentation/red_hat_ansible_automation_platform/2.5


## License Information

[GPLv3](https://github.com/ansible/aap-gateway/ansible_platform_collection/COPYING)

## Authors

[Sean Sullivan](https://github.com/sean-m-sullivan)
[Martin Slemr](https://github.com/slemrmartin)
[Jake Jackson](https://github.com/thedboubl3j)
[Brennan Paciorek](https://github.com/brennanpaciorek)
[John Westcott](https://github.com/john-westcott-iv)
[Jessica Steurer](https://github.com/jay-steurer)
