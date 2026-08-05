from ..module_utils.aap_object import AAPObject  # noqa

__metaclass__ = type


class AAPUser(AAPObject):
    API_ENDPOINT_NAME = "users"
    ITEM_TYPE = "user"

    def unique_field(self):
        return self.module.IDENTITY_FIELDS['users']

    def set_new_fields(self):
        # Create the data that gets sent for create and update

        username = self.module.params.get('username')
        if username is not None:
            self.new_fields['username'] = self.module.get_item_name(self.data) if self.data else username

        first_name = self.module.params.get('first_name')
        if first_name is not None:
            self.new_fields['first_name'] = first_name

        last_name = self.module.params.get('last_name')
        if last_name is not None:
            self.new_fields['last_name'] = last_name

        email = self.module.params.get('email')
        if email is not None:
            self.new_fields['email'] = email

        is_superuser = self.module.params.get('is_superuser')
        if is_superuser is not None:
            self.new_fields['is_superuser'] = is_superuser

        password = self.module.params.get('password')
        if password is not None:
            self.new_fields['password'] = password

        organizations = self.module.params.get('organizations')
        if organizations is not None:
            self.new_fields['organizations'] = organizations

        authenticators = self.module.params.get('authenticators')
        if authenticators is not None:
            self.new_fields['authenticators'] = authenticators

        authenticator_uid = self.module.params.get('authenticator_uid')
        if authenticator_uid is not None:
            self.new_fields['authenticator_uid'] = authenticator_uid
