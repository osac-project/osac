from __future__ import absolute_import, division, print_function

__metaclass__ = type

from .aap_object import AAPObject


class AAPAuthenticatorUser(AAPObject):
    API_ENDPOINT_NAME = "authenticator_users"
    ITEM_TYPE = "authenticator_user"

    def unique_value(self):
        return self.params.get(self.unique_field())

    def unique_field(self):
        return self.module.IDENTITY_FIELDS['authenticator_users']

    def get_existing_item(self):
        if self.data is None:
            unique = self.unique_value()
            self.data = self.module.get_endpoint(f"{self.api_endpoint}/{unique}").get('json')
        return self.data


class AAPAuthenticatorUserMove(AAPObject):
    def __init__(self, module):
        self.module = module
        super().__init__(module)

    def get_existing_item(self):
        authenticator = AAPAuthenticatorUser(module=self.module, params=self.params)
        self.data = authenticator.get_existing_item()
        return self.data

    def set_new_fields(self):
        summary_fields = self.data.get('summary_fields', {})
        new_authenticator_id = self.params.get('authenticator')
        existing_authenticator_id = str(summary_fields.get('provider', {}).get('id'))
        if new_authenticator_id != existing_authenticator_id:
            self.new_fields['new_authenticator'] = new_authenticator_id

        existing_uid = str(self.data.get('uid'))
        new_uid = self.params.get('new_uid')
        if new_uid and existing_uid != new_uid:
            self.new_fields['uid'] = new_uid

        merge_with_user = self.params.get('merge_with_user')
        if merge_with_user is not None:
            existing_user = str(summary_fields.get('user', {}).get('id'))
            if merge_with_user and merge_with_user != existing_user:
                self.new_fields['merge_with_user'] = merge_with_user

        merge_accounts_with_same_uid = self.params.get('merge_accounts_with_same_uid')
        if merge_accounts_with_same_uid and 'merge_with_user' not in self.new_fields:
            self.new_fields['merge_accounts_with_same_uid'] = merge_accounts_with_same_uid
        else:
            self.new_fields['merge_accounts_with_same_uid'] = False

        for field in ['keep_memberships', 'remove_other_authenticators']:
            if value := self.params.get(field) is not None:
                self.new_fields[field] = value
            else:
                self.new_fields[field] = False

    def manage(self, auto_exit=True, fail_when_not_exists=True, **kwargs):
        self.get_existing_item()
        self.api_endpoint = f"authenticator_users/{self.module.params.get('authenticator_user_id')}/move/"
        self.ITEM_TYPE = self.api_endpoint
        self.set_new_fields()
        if self.present():
            if 'new_authenticator' not in self.new_fields:
                if auto_exit:
                    self.module.exit_json(**self.module.json_output)

            # The `api/gateway/v1/authenticator_users/<pk>/move/` API supports
            # only POST method. So, we need to pass existing_item as None.
            self.data = self.module.create_if_needed(None, self.new_fields, endpoint=self.api_endpoint, item_type=self.ITEM_TYPE)
            for output_field in kwargs.get('json_output_fields', []):
                if output_field in self.data:
                    self.module.json_output[output_field] = self.data[output_field]

            if auto_exit:
                self.module.exit_json(**self.module.json_output)
        elif self.exists():
            error_message = ""
            if self.data is None:
                error_message = f"Item {self.ITEM_TYPE} does not exist for authenticator_user_id {self.unique_value()}."
            else:
                summary_fields = self.data.get('summary_fields', {})
                if 'new_authenticator' in self.new_fields:
                    new_authenticator_id = self.params.get('authenticator')
                    existing_authenticator_id = summary_fields.get('provider', {}).get('id')
                    error_message += f"Exiting authenticator id is {existing_authenticator_id} however expected is {new_authenticator_id}.\n"

                if 'uid' in self.new_fields:
                    existing_uid = self.data.get('uid')
                    new_uid = self.params.get('new_uid')
                    error_message += f"Exiting uid is {existing_uid} however expected is {new_uid}.\n"

                if 'merge_with_user' in self.new_fields:
                    merge_with_user = self.params.get('merge_with_user')
                    existing_user = self.data.get('user')
                    error_message += f"Exiting merged user is {existing_user} however expected is {merge_with_user}."

            if fail_when_not_exists and error_message != "":
                self.module.fail_json(msg=error_message)

            self.module.json_output["id"] = self.data['id']
            if auto_exit:
                self.module.exit_json(**self.module.json_output)
