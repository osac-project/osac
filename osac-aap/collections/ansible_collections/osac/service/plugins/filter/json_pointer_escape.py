#!/usr/bin/python3

def json_pointer_escape(input_text):
    """ Escape json patch key characters from input_text.
        https://jsonpatch.com/#json-pointer """
    return input_text.replace("~", "~0").replace("/", "~1")


class FilterModule(object):
    def filters(self):
        return {
            'json_pointer_escape': json_pointer_escape
        }
