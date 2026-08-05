from __future__ import absolute_import, division, print_function

__metaclass__ = type

from ..module_utils.aap_object import AAPObject


class AAPAuthenticatorMap(AAPObject):
    API_ENDPOINT_NAME = "authenticator_maps"
    ITEM_TYPE = "authenticator_map"

    def __init__(self, module, params=None, **kwargs):
        super().__init__(module, params, **kwargs)
        self.authenticator = None
        self.new_authenticator = None

    def manage(self, **kwargs):
        self.get_authenticator()

        if self.absent() and self.authenticator.data is None:
            self.module.exit_json(**self.module.json_output)

        super().manage(**kwargs)

    def unique_field(self):
        return self.module.IDENTITY_FIELDS['authenticators']

    def unique_value(self):
        return {'name': self.params.get('name'), 'authenticator': self.authenticator.data['id']}

    def _get_authenticator(self, name_or_id):
        from ..module_utils.aap_authenticator import AAPAuthenticator

        params = {"name": name_or_id, "state": self.STATE_EXISTS}

        # If delete is required, cluster doesn't need to exist
        fail_when_not_exists = not self.absent()

        authenticator = AAPAuthenticator(module=self.module, params=params)
        authenticator.manage(auto_exit=False, fail_when_not_exists=fail_when_not_exists)

        return authenticator

    def get_authenticator(self):
        self.authenticator = self._get_authenticator(self.params.get('authenticator'))

    def get_new_authenticator(self, name_or_id):
        self.new_authenticator = self._get_authenticator(name_or_id)

    def get_existing_item(self):
        if self.data is None:
            unique = self.unique_value()
            self.data = self.module.get_one(self.api_endpoint, name_or_id=unique['name'], **{'data': {'authenticator': unique['authenticator']}})
        return self.data

    def set_new_fields(self):
        # Create the data that gets sent for create and update
        self.set_name_field()

        self._set_authenticator_field()

        revoke = self.params.get('revoke')
        if revoke is not None:
            self.new_fields['revoke'] = revoke

        map_type = self.params.get('map_type')
        if map_type is not None:
            self.new_fields['map_type'] = map_type

        team = self.params.get('team')
        if team is not None:
            self.new_fields['team'] = team

        organization = self.params.get('organization')
        if organization is not None:
            self.new_fields['organization'] = organization

        role = self.params.get('role')
        if role is not None:
            self.new_fields['role'] = role

        triggers = self.params.get('triggers')
        if triggers is not None:
            self.new_fields['triggers'] = triggers

        order = self.params.get('order')
        if order is not None:
            self.new_fields['order'] = order

    def _set_authenticator_field(self):
        if self.authenticator:
            authenticator_id = None

            if self.params.get('new_authenticator') is not None:
                self.get_new_authenticator(self.params.get('new_authenticator'))
                if self.new_authenticator is not None:
                    authenticator_id = (self.new_authenticator.data or {}).get('id')
            else:
                authenticator_id = (self.authenticator.data or {}).get('id')

            if authenticator_id is not None:
                self.new_fields['authenticator'] = authenticator_id
