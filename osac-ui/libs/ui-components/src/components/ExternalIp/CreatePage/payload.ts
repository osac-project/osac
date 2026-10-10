import type { MessageInitShape } from '@bufbuild/protobuf';

import { ExternalIPSchema } from '@osac/types';

import type { ExternalIpFormValues } from './values';

export const buildExternalIpCreatePayload = (
  values: ExternalIpFormValues,
): MessageInitShape<typeof ExternalIPSchema> => ({
  metadata: {
    name: values.metadata.name,
    project: values.metadata.project,
    description: values.metadata.description,
  },
  spec: {
    pool: {
      id: values.pool.id,
      name: values.pool.name,
    },
  },
});
