import type { TFunction } from 'i18next';

import { Architecture, GuestOSFamily } from '@osac/types';

export const DISK_IMAGE_NAME_PREVIEW_LENGTH = 32;

export const architectureLabels = (t: TFunction): Record<Architecture, string> => ({
  [Architecture.UNSPECIFIED]: t('Unspecified'),
  [Architecture.AMD64]: 'amd64',
  [Architecture.ARM64]: 'arm64',
  [Architecture.S390X]: 's390x',
});
export const guestOsFamilyLabels = (t: TFunction): Record<GuestOSFamily, string> => ({
  [GuestOSFamily.GUEST_OS_FAMILY_UNSPECIFIED]: t('Unspecified'),
  [GuestOSFamily.GUEST_OS_FAMILY_LINUX]: t('Linux'),
  [GuestOSFamily.GUEST_OS_FAMILY_WINDOWS]: t('Windows'),
});
