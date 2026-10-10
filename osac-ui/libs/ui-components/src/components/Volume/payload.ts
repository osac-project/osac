import type { MessageInitShape } from '@bufbuild/protobuf';

import { VolumeSchema } from '@osac/types';

import type { VolumeFormValues } from './values';

export const buildVolumeCreatePayload = (
  values: VolumeFormValues,
): MessageInitShape<typeof VolumeSchema> => ({
  metadata: {
    name: values.metadata.name,
    description: values.metadata.description,
  },
  spec: {
    storageTier: values.spec.storageTier.name,
    sizeGib: BigInt(values.spec.sizeGib),
    accessMode: values.spec.accessMode,
  },
});
