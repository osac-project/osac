"""Validate the internal API URL before registering a reader credential."""

from ansible.errors import AnsibleError
from ansible_collections.osac.service.plugins.module_utils.fulfillment_url import parse_fulfillment_api_url


def fulfillment_api_url(value):
    try:
        parse_fulfillment_api_url(value)
    except AnsibleError:
        return False
    return True


class TestModule:
    def tests(self):
        return {"fulfillment_api_url": fulfillment_api_url}
