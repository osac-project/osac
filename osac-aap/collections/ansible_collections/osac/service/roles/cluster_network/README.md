# `osac.service.cluster_network`

This role is a no-op compatibility entry point. New CaaS networking is
handled by the Networking API dispatcher and provider-specific step roles;
the service role must not acquire provider-specific networking behavior.

The empty argument specification and no-op task file are intentional. Keep
the entry point while compatibility callers exist, and do not use it for new
CaaS workflow steps.
