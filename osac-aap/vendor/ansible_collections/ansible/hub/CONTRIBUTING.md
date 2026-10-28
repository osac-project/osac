# Contributing

Refer to the [Ansible community guide](https://docs.ansible.com/ansible/devel/community/index.html).

## Module options

Any option that can hold a credential (password, token, key, etc.) must be
declared `no_log=True` in the module's `argument_spec`. Shared code in
`ah_api_module.py` can surface a server's raw error response verbatim when it
doesn't recognize the error shape; `no_log` is what keeps a credential out of
that message if the server ever echoes it back.
