from __future__ import absolute_import, division, print_function

__metaclass__ = type

from ..module_utils.aap_object import AAPObject


class AAPAuthenticator(AAPObject):
    API_ENDPOINT_NAME = "authenticators"
    ITEM_TYPE = "authenticator"

    def unique_field(self):
        return self.module.IDENTITY_FIELDS['http_ports']

    def _get_authenticator(self, name_or_id):
        params = {"name": name_or_id, "state": self.STATE_EXISTS}

        fail_when_not_exists = not self.absent()

        authenticator = AAPAuthenticator(module=self.module, params=params)
        authenticator.manage(auto_exit=False, fail_when_not_exists=fail_when_not_exists)

        return authenticator

    def get_auto_migrate_to_authenticator(self):
        self.auto_migrate_to_authenticator = self._get_authenticator(self.params.get('auto_migrate_to_authenticator'))

    def set_new_fields(self):
        # Create the data that gets sent for create and update
        self.set_name_field()

        slug = self.module.params.get('slug')
        if slug is not None:
            self.new_fields['slug'] = slug

        enabled = self.module.params.get('enabled')
        if enabled is not None:
            self.new_fields['enabled'] = enabled

        create_objects = self.module.params.get('create_objects')
        if create_objects is not None:
            self.new_fields['create_objects'] = create_objects

        remove_users = self.module.params.get('remove_users')
        if remove_users is not None:
            self.new_fields['remove_users'] = remove_users

        configuration = self.module.params.get('configuration')
        if configuration is not None:
            self.new_fields['configuration'] = configuration

        _type = self.module.params.get('type')
        if _type is not None:
            self.new_fields['type'] = _type

        order = self.module.params.get('order')
        if order is not None:
            self.new_fields['order'] = order

        auto_migrate_users_to = self.module.params.get('auto_migrate_users_to')
        if auto_migrate_users_to is not None:
            authenticator = self._get_authenticator(auto_migrate_users_to)
            authenticator_id = (authenticator.data or {}).get('id')
            self.new_fields['auto_migrate_users_to'] = authenticator_id
