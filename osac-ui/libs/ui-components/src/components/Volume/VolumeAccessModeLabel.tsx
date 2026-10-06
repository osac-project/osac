import { VolumeAccessMode } from '@osac/types';

import { useTranslation } from '../../hooks/useTranslation';

interface VolumeAccessModeLabelProps {
  accessMode?: VolumeAccessMode;
}

const resolveAccessMode = (
  accessMode: VolumeAccessMode | undefined,
  t: (key: string) => string,
): string => {
  switch (accessMode) {
    case VolumeAccessMode.READ_WRITE_ONCE:
      return t('ReadWriteOnce');
    case VolumeAccessMode.READ_ONLY_MANY:
      return t('ReadOnlyMany');
    case VolumeAccessMode.READ_WRITE_MANY:
      return t('ReadWriteMany');
    case VolumeAccessMode.READ_WRITE_ONCE_POD:
      return t('ReadWriteOncePod');
    default:
      return t('Unspecified');
  }
};

export const VolumeAccessModeLabel = ({ accessMode }: VolumeAccessModeLabelProps) => {
  const { t } = useTranslation();
  return <>{resolveAccessMode(accessMode, t)}</>;
};
