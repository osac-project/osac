import { type MessageInitShape } from '@bufbuild/protobuf';

import { BareMetalInstanceSchema, ClusterSchema, ComputeInstanceSchema } from '@osac/types';

import { BareMetalInstanceWizardValues } from './wizard/adapters/bareMetalInstance/fields';
import type { ClusterWizardValues } from './wizard/adapters/cluster/fields';
import type { ComputeInstanceWizardValues } from './wizard/adapters/computeInstance/fields';

export type CatalogProvisionPayload =
  | MessageInitShape<typeof ComputeInstanceSchema>
  | MessageInitShape<typeof ClusterSchema>
  | MessageInitShape<typeof BareMetalInstanceSchema>;

export type CatalogProvisionWizardValues =
  ComputeInstanceWizardValues | ClusterWizardValues | BareMetalInstanceWizardValues;
