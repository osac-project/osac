from ..module_utils.aap_object import AAPObject

__metaclass__ = type


class AAPTeam(AAPObject):
    API_ENDPOINT_NAME = "teams"
    ITEM_TYPE = "team"

    def __init__(self, module, params=None, **kwargs):
        super().__init__(module, params, **kwargs)
        self.organization = None
        self.new_organization = None

    def manage(self, **kwargs):
        self.get_organization()

        if self.absent() and self.organization.data is None:
            self.module.exit_json(**self.module.json_output)

        super().manage(**kwargs)

    def unique_field(self):
        return self.module.IDENTITY_FIELDS['teams']

    def unique_value(self):
        return {'name': self.params.get('name'), 'organization': self.organization.data['id']}

    def _get_organization(self, name_or_id):
        from ..module_utils.aap_organization import AAPOrganization

        params = {"name": name_or_id, "state": self.STATE_EXISTS}

        # If delete is required, organization doesn't need to exist
        fail_when_not_exists = not self.absent()

        organization = AAPOrganization(module=self.module, params=params)
        organization.manage(auto_exit=False, fail_when_not_exists=fail_when_not_exists)

        return organization

    def get_organization(self):
        self.organization = self._get_organization(self.params.get('organization'))

    def get_new_organization(self, name_or_id):
        self.new_organization = self._get_organization(name_or_id)

    def get_existing_item(self):
        if self.data is None:
            unique = self.unique_value()
            self.data = self.module.get_one(self.api_endpoint, name_or_id=unique['name'], **{'data': {'organization': unique['organization']}})
        return self.data

    def set_new_fields(self):
        # Create the data that gets sent for create and update
        self.set_name_field()

        description = self.params.get('description')
        if description is not None:
            self.new_fields['description'] = description

        self._set_organization_field()

        users = self.params.get('users')
        if users is not None:
            self.new_fields['users'] = users

        admins = self.params.get('admins')
        if admins is not None:
            self.new_fields['admins'] = admins

    def _set_organization_field(self):
        if self.organization:
            organization_id = None

            if self.params.get('new_organization') is not None:
                self.get_new_organization(self.params.get('new_organization'))
                if self.new_organization is not None:
                    organization_id = (self.new_organization.data or {}).get('id')
            else:
                organization_id = (self.organization.data or {}).get('id')

            if organization_id is not None:
                self.new_fields['organization'] = organization_id
