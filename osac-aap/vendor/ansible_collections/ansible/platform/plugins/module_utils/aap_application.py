from __future__ import absolute_import, division, print_function

__metaclass__ = type

from ..module_utils.aap_object import AAPObject


class AAPApplication(AAPObject):
    API_ENDPOINT_NAME = "applications"
    ITEM_TYPE = "application"

    def __init__(self, module, params=None, **kwargs):
        super().__init__(module, params, **kwargs)
        self.organization = None
        self.new_organization = None
        self.user = None

    def manage(self, **kwargs):
        self.get_organization()
        if self.present() and self.params.get('user') is not None:
            self.get_user()

        # If delete is required, and organization not found, application can't exist => exit
        if self.absent() and self.organization.data is None:
            self.module.exit_json(**self.module.json_output)

        super().manage(**kwargs)

    def unique_field(self):
        return self.module.IDENTITY_FIELDS['applications']

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

    def get_user(self):
        from ..module_utils.aap_user import AAPUser

        params = {"username": self.params.get('user'), "state": self.STATE_EXISTS}

        # If delete is required, user doesn't need to exist
        fail_when_not_exists = not self.absent()

        self.user = AAPUser(module=self.module, params=params)
        self.user.manage(auto_exit=False, fail_when_not_exists=fail_when_not_exists)
        return self.user

    def get_existing_item(self):
        if self.data is None:
            unique = self.unique_value()
            self.data = self.module.get_one(self.api_endpoint, name_or_id=unique['name'], **{'data': {'organization': unique['organization']}})
        return self.data

    def set_new_fields(self):
        # Create the data that gets sent for create and update
        self.set_name_field()
        self._set_organization_field()

        description = self.module.params.get('description')
        if description is not None:
            self.new_fields['description'] = description

        algorithm = self.module.params.get('algorithm')
        if algorithm is not None:
            self.new_fields['algorithm'] = algorithm

        authorization_grant_type = self.module.params.get('authorization_grant_type')
        if authorization_grant_type is not None:
            self.new_fields['authorization_grant_type'] = authorization_grant_type

        client_type = self.module.params.get('client_type')
        if client_type is not None:
            self.new_fields['client_type'] = client_type

        redirect_uris = self.module.params.get('redirect_uris')
        if redirect_uris is not None:
            # Has to be space separated value in API!
            if isinstance(redirect_uris, list):
                redirect_uris = ' '.join(redirect_uris)
            self.new_fields['redirect_uris'] = redirect_uris

        skip_authorization = self.module.params.get('skip_authorization')
        if skip_authorization is not None:
            self.new_fields['skip_authorization'] = skip_authorization

        post_logout_redirect_uris = self.module.params.get('post_logout_redirect_uris')
        if post_logout_redirect_uris is not None:
            # Has to be space separated value in API!
            if isinstance(post_logout_redirect_uris, list):
                post_logout_redirect_uris = ' '.join(post_logout_redirect_uris)
            self.new_fields['post_logout_redirect_uris'] = post_logout_redirect_uris

        app_url = self.module.params.get('app_url')
        if app_url is not None:
            self.new_fields['app_url'] = app_url

        if self.user:
            user_id = (self.user.data or {}).get('id')
            if user_id is not None:
                self.new_fields['user'] = user_id

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
