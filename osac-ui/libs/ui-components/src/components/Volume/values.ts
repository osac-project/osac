import { VolumeAccessMode } from '@osac/types';

import { type ResourceSelectValue, emptyResourceSelectValue } from '../Form/ResourceSelectField';

export const VOLUMES_LIST_PATH = '/storage/volumes';

export interface VolumeFormValues {
  metadata: {
    name: string;
    description: string;
  };
  spec: {
    storageTier: ResourceSelectValue;
    sizeGib: string;
    accessMode: VolumeAccessMode;
  };
}

export const getVolumeValues = (): VolumeFormValues => ({
  metadata: {
    name: '',
    description: '',
  },
  spec: {
    storageTier: emptyResourceSelectValue(),
    sizeGib: '',
    accessMode: VolumeAccessMode.UNSPECIFIED,
  },
});
