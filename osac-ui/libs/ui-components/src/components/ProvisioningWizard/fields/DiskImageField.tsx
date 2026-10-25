import { useField } from 'formik';

import { type DiskImage, DiskImages, GuestOSFamily } from '@osac/types';
import { DISK_IMAGE_NON_OBSOLETE_FILTER } from '@osac/ui-components/api/v1/disk-image';
import { ResourceSelectField } from '@osac/ui-components/components/Form/ResourceSelectField';

import GuestOsFamilyField from './GuestOsFamilyField';
import ProvisioningFieldGroup from './ProvisioningFieldGroup';
import type { ProvisioningFieldControlProps } from './provisioningFieldValue';
import { cel } from '../../../api/cel';
import { useTranslation } from '../../../hooks/useTranslation';

interface DiskImageFieldProps extends ProvisioningFieldControlProps {
  fieldId?: string;
  guestOsFamilyName: string;
}

const DiskImageField = ({
  name,
  fieldId = 'disk-image',
  guestOsFamilyName,
  isDisabled = false,
  isRequired = true,
  labelInfo,
}: DiskImageFieldProps) => {
  const { t } = useTranslation();
  const [guestOsFamilyField] = useField<GuestOSFamily>(guestOsFamilyName);
  const guestOsFamily = guestOsFamilyField.value ?? GuestOSFamily.GUEST_OS_FAMILY_UNSPECIFIED;
  const guestOsFamilyLabel =
    {
      [GuestOSFamily.GUEST_OS_FAMILY_LINUX]: t('Linux'),
      [GuestOSFamily.GUEST_OS_FAMILY_WINDOWS]: t('Microsoft Windows'),
      [GuestOSFamily.GUEST_OS_FAMILY_UNSPECIFIED]: t('Unspecified'),
    }[guestOsFamily] ?? t('Unspecified');
  // OSAC-6054: prevent selecting a disk image for a different guest OS family.
  const diskImageFilter = cel<DiskImage>((filter) =>
    filter.and(
      DISK_IMAGE_NON_OBSOLETE_FILTER,
      filter.field('spec.guestOsFamily').equals(guestOsFamily),
    ),
  );

  return (
    <ProvisioningFieldGroup
      title={t('Disk image')}
      titleId={`${fieldId}-group`}
      actions={labelInfo}
    >
      <GuestOsFamilyField name={guestOsFamilyName} isDisabled={isDisabled} />
      <ResourceSelectField
        name={name}
        label={t('Select the image reference')}
        fieldId={fieldId}
        service={DiskImages}
        request={{ filter: diskImageFilter }}
        isRequired={isRequired}
        isDisabled={isDisabled}
        placeholder={t('Select a disk image')}
        loadErrorTitle={t('Could not load disk images')}
        emptyTitle={t('No disk images available of guest OS family: {{guestOsFamily}}', {
          guestOsFamily: guestOsFamilyLabel,
        })}
      />
    </ProvisioningFieldGroup>
  );
};

export default DiskImageField;
