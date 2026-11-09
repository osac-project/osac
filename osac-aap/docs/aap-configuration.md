# AAP Configuration

## Hosted cluster DNS

The CaaS endpoint workflow publishes API and ingress records under
`HOSTED_CLUSTER_BASE_DOMAIN`. The domain must also be listed in
`EXTERNAL_ACCESS_SUPPORTED_BASE_DOMAINS`.

`DNS_ZONE` selects the Route 53 hosted zone used for those records. It must be
authoritative for the published names. Its default is
`EXTERNAL_ACCESS_BASE_DOMAIN`; set it explicitly when that default is not the
right zone for `HOSTED_CLUSTER_BASE_DOMAIN`. An explicitly configured parent
zone is preserved and can contain the hosted-cluster domain.
