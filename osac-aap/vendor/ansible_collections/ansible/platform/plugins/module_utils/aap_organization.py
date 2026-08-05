from ..module_utils.aap_object import AAPObject

__metaclass__ = type


class AAPOrganization(AAPObject):
    API_ENDPOINT_NAME = "organizations"
    ITEM_TYPE = "organization"

    def unique_field(self):
        return self.module.IDENTITY_FIELDS['organizations']

    def set_new_fields(self):
        # Create the data that gets sent for create and update
        self.set_name_field()

        description = self.params.get('description')
        if description is not None:
            self.new_fields['description'] = description

        users = self.params.get('users')
        if users is not None:
            self.new_fields['users'] = users

        admins = self.params.get('admins')
        if admins is not None:
            self.new_fields['admins'] = admins
