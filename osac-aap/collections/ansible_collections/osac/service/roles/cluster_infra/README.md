# `osac.service.cluster_infra`

This role is the generic cluster-infrastructure service entry point. It
dispatches create and delete operations to the provider-specific collection
selected by `network_steps_collection`.

CaaS workflows may read operator-owned Agent status from their own service
entry points when it is part of the endpoint contract. Agent creation,
approval, and deletion remain outside this provider-dispatch role.
