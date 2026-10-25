import { useEffect, useRef } from 'react';
import { useField, useFormikContext } from 'formik';

import { type DiskImage, DiskImages, GuestOSFamily } from '@osac/types';
import { DISK_IMAGE_NON_OBSOLETE_FILTER } from '@osac/ui-components/api/v1/disk-image';
import { ResourceSelectField } from '@osac/ui-components/components/Form/ResourceSelectField';

import GuestOsFamilyField from './GuestOsFamilyField';
import ProvisioningFieldGroup from './ProvisioningFieldGroup';
import type { ProvisioningFieldControlProps } from './provisioningFieldValue';
import { cel } from '../../../api/cel';
import { useTranslation } from '../../../hooks/useTranslation';
import { emptyResourceSelectValue } from '../../Form/resourceSelectValue';

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
  const { setFieldValue } = useFormikContext();
  const guestOsFamily = guestOsFamilyField.value ?? GuestOSFamily.GUEST_OS_FAMILY_UNSPECIFIED;
  const guestOsFamilyLabel =
    {
      [GuestOSFamily.GUEST_OS_FAMILY_LINUX]: t('Linux'),
      [GuestOSFamily.GUEST_OS_FAMILY_WINDOWS]: t('Microsoft Windows'),
      [GuestOSFamily.GUEST_OS_FAMILY_UNSPECIFIED]: t('Unspecified'),
    }[guestOsFamily] ?? t('Unspecified');
  const previousGuestOsFamily = useRef(guestOsFamily);
  const synchronizedGuestOsFamily = useRef<GuestOSFamily | undefined>(undefined);

  useEffect(() => {
    if (previousGuestOsFamily.current === guestOsFamily) {
      return;
    }

    previousGuestOsFamily.current = guestOsFamily;
    if (synchronizedGuestOsFamily.current === guestOsFamily) {
      synchronizedGuestOsFamily.current = undefined;
      return;
    }

    if (guestOsFamily !== GuestOSFamily.GUEST_OS_FAMILY_UNSPECIFIED) {
      void setFieldValue(name, emptyResourceSelectValue(), false);
    }
  }, [guestOsFamily, name, setFieldValue]);

  const diskImageFilter =
    guestOsFamily === GuestOSFamily.GUEST_OS_FAMILY_UNSPECIFIED
      ? DISK_IMAGE_NON_OBSOLETE_FILTER
      : cel<DiskImage>((filter) =>
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
      hasDivider
    >
      <GuestOsFamilyField name={guestOsFamilyName} isDisabled={isDisabled} />
      <ResourceSelectField<DiskImage>
        name={name}
        label={t('Select the image reference')}
        fieldId={fieldId}
        service={DiskImages}
        request={{ filter: diskImageFilter }}
        isRequired={isRequired}
        isDisabled={isDisabled}
        showOptionDescriptions={false}
        placeholder={t('Select a disk image')}
        loadErrorTitle={t('Could not load disk images')}
        onSelectResource={(_, diskImage) => {
          const selectedGuestOsFamily = diskImage?.spec?.guestOsFamily;
          if (
            guestOsFamily === GuestOSFamily.GUEST_OS_FAMILY_UNSPECIFIED &&
            selectedGuestOsFamily !== undefined &&
            selectedGuestOsFamily !== GuestOSFamily.GUEST_OS_FAMILY_UNSPECIFIED
          ) {
            synchronizedGuestOsFamily.current = selectedGuestOsFamily;
            void setFieldValue(guestOsFamilyName, selectedGuestOsFamily);
          }
        }}
        emptyTitle={t('No disk images available of guest OS family: {{guestOsFamily}}', {
          guestOsFamily: guestOsFamilyLabel,
        })}
      />
    </ProvisioningFieldGroup>
  );
};

export default DiskImageField;
