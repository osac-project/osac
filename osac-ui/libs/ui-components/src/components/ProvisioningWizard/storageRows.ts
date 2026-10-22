import type { TFunction } from 'i18next';

import type { ComputeInstanceDisk, StorageTierReference } from '@osac/types';

import { displayValue } from '../../utils/detailFormatters';

const formatDiskSize = (value: unknown): string => {
  if (value === undefined || value === null || value === '') {
    return '—';
  }
  return `${typeof value === 'object' ? (JSON.stringify(value) ?? '—') : String(value as string | number | boolean)} GB`;
};

type StorageDisk = {
  sizeGib?: ComputeInstanceDisk['sizeGib'] | string;
  storageTier?: Pick<StorageTierReference, 'id' | 'name'>;
};

export interface VmStorageRow {
  name: string;
  size: string;
  storageTier: string;
}

export const getVmStorageRows = (
  t: TFunction,
  bootDisk: StorageDisk | undefined,
  additionalDisks: StorageDisk[] | undefined,
): VmStorageRow[] => {
  return [
    {
      name: t('Boot disk'),
      size: formatDiskSize(bootDisk?.sizeGib),
      storageTier: displayValue(bootDisk?.storageTier?.name || bootDisk?.storageTier?.id),
    },
    ...(additionalDisks ?? []).map((disk, index) => ({
      name: t('Additional disk {{number}}', { number: index + 1 }),
      size: formatDiskSize(disk.sizeGib),
      storageTier: displayValue(disk.storageTier?.name || disk.storageTier?.id),
    })),
  ];
};
