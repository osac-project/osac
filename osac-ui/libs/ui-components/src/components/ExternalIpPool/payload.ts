import { IPFamily } from '@osac/types/private';

import type { ExternalIpPoolFormValues } from './values';

export const toCreateRequest = (values: ExternalIpPoolFormValues) => ({
  object: {
    metadata: {
      name: values.metadata.name,
      tenant: values.metadata.tenant.id,
    },
    spec: {
      ipFamily: IPFamily.IP_FAMILY_IPV4,
      cidrs: values.cidrs,
    },
  },
});
