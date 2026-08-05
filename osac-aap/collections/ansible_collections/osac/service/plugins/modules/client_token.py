from ansible.module_utils.basic import AnsibleModule
from kubernetes import client, config

import durationpy


DOCUMENTATION = r'''
---
module: client_token

short_description: Creates an SA token

description: Creates a time based service account token in Kubernetes

options:
    audience:
        description: Intended audiences of the token
        required: false
        default: ['https://kubernetes.default.svc']
        type: list
        element: str
    duration:
        description: The amount of time the token remains valid
        required: false
        type: str
    namespace:
        description: The namespace containing the service account
        required: true
        type: str
    service_account:
        description: The name of the service account
        required: true
        type: str
'''

EXAMPLES = r'''
- name: Create a client token for 30 minutes
  osac.service.client_token:
    service_account: "client"
    namespace: "default"
    duration: "30m"
'''

RETURN = r'''
token:
    desciption: The created JWT
    type: str
    returned: success
'''


def run():
    module_args = dict(
        audience=dict(type='list', default=['https://kubernetes.default.svc']),
        duration=dict(type='str'),
        namespace=dict(type='str', required=True),
        service_account=dict(type='str', required=True),
    )
    module = AnsibleModule(
        argument_spec=module_args,
        supports_check_mode=True,
    )
    audience = module.params['audience']
    duration = module.params['duration']
    namespace = module.params['namespace']
    service_account = module.params['service_account']

    token_request_spec = client.V1TokenRequestSpec(audiences=audience)
    if duration:
        try:
            duration_seconds = durationpy.from_str(duration).total_seconds()
        except durationpy.DurationError as err:
            module.fail_json(msg=err)
        token_request_spec.expiration_seconds = int(duration_seconds)

    token_request = client.AuthenticationV1TokenRequest(
        api_version='authentication.k8s.io/v1',
        kind='TokenRequest',
        spec=token_request_spec,
    )

    if module.check_mode:
        module.exit_json(changed=True, token="[token would be created]")

    config.load_config()
    client_api = client.CoreV1Api()
    token_response = client_api.create_namespaced_service_account_token(
        service_account,
        namespace,
        token_request,
    )

    module.exit_json(changed=True, token=token_response.status.token)


def main():
    run()


if __name__ == '__main__':
    main()
