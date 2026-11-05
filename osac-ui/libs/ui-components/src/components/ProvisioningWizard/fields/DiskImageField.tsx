import { useEffect } from 'react';
import { Alert, Button, Stack, StackItem } from '@patternfly/react-core';
import { useField, useFormikContext } from 'formik';

import { type DiskImage, DiskImages, GuestOSFamily } from '@osac/types';
import { useGetResource } from '@osac/ui-components/api/use-resource';
import { DISK_IMAGE_NON_OBSOLETE_FILTER } from '@osac/ui-components/api/v1/disk-image';
import { ResourceSelectField } from '@osac/ui-components/components/Form/ResourceSelectField';

import GuestOsFamilyField from './GuestOsFamilyField';
import ProvisioningFieldGroup from './ProvisioningFieldGroup';
import type { ProvisioningFieldControlProps } from './provisioningFieldValue';
import { cel } from '../../../api/cel';
import { useTranslation } from '../../../hooks/useTranslation';
import { getErrorMessage } from '../../../utils/error';
import { type ResourceSelectValue, emptyResourceSelectValue } from '../../Form/resourceSelectValue';

interface DiskImageFieldProps extends ProvisioningFieldControlProps {
  fieldId?: string;
  guestOsFamilyName: string;
  autoSelectSingleOption?: boolean;
  /** Set for a cloud provider admin: limits the list to the workload's tenant. */
  tenant?: string;
}

const DiskImageField = ({
  name,
  fieldId = 'disk-image',
  guestOsFamilyName,
  isDisabled = false,
  isRequired = true,
  labelInfo,
  autoSelectSingleOption = true,
  tenant,
}: DiskImageFieldProps) => {
  const { t } = useTranslation();
  const [diskImageField] = useField<ResourceSelectValue>(name);
  const [guestOsFamilyField] = useField<GuestOSFamily | undefined>(guestOsFamilyName);
  const { setFieldValue } = useFormikContext();
  const diskImageId = diskImageField.value?.id;
  const {
    data: initialDiskImageResponse,
    isLoading: isInitialDiskImageLoading,
    error: initialDiskImageError,
    refetch: refetchInitialDiskImage,
  } = useGetResource(
    DiskImages,
    { id: diskImageId },
    {
      enabled: Boolean(diskImageId && guestOsFamilyField.value === undefined),
    },
  );
  const initialDiskImage = initialDiskImageResponse?.object;
  const rawGuestOsFamily = guestOsFamilyField.value;
  const guestOsFamily = guestOsFamilyField.value ?? GuestOSFamily.GUEST_OS_FAMILY_UNSPECIFIED;
  const guestOsFamilyLabel =
    {
      [GuestOSFamily.GUEST_OS_FAMILY_LINUX]: t('Linux'),
      [GuestOSFamily.GUEST_OS_FAMILY_WINDOWS]: t('Microsoft Windows'),
      [GuestOSFamily.GUEST_OS_FAMILY_UNSPECIFIED]: t('Unspecified'),
    }[guestOsFamily] ?? t('Unspecified');

  useEffect(() => {
    if (
      rawGuestOsFamily !== undefined ||
      !diskImageId ||
      isInitialDiskImageLoading ||
      initialDiskImageError
    ) {
      return;
    }

    const initialGuestOsFamily = initialDiskImage?.spec?.guestOsFamily;
    if (
      initialGuestOsFamily === undefined ||
      initialGuestOsFamily === GuestOSFamily.GUEST_OS_FAMILY_UNSPECIFIED
    ) {
      return;
    }

    void setFieldValue(guestOsFamilyName, initialGuestOsFamily);
  }, [
    diskImageId,
    guestOsFamilyName,
    initialDiskImage,
    initialDiskImageError,
    isInitialDiskImageLoading,
    rawGuestOsFamily,
    setFieldValue,
  ]);

  const diskImageFilter = cel<DiskImage>((filter) =>
    filter.and(
      DISK_IMAGE_NON_OBSOLETE_FILTER,
      // A tenant sees the global images and its own.
      tenant === undefined
        ? undefined
        : filter.or(
            filter.field('metadata.tenant').equals('shared'),
            filter.field('metadata.tenant').equals(tenant),
          ),
      guestOsFamily === GuestOSFamily.GUEST_OS_FAMILY_UNSPECIFIED
        ? undefined
        : filter.field('spec.guestOsFamily').equals(guestOsFamily),
    ),
  );

  const handleGuestOsFamilySelect = (selectedGuestOsFamily: GuestOSFamily) => {
    if (guestOsFamily !== selectedGuestOsFamily) {
      void setFieldValue(name, emptyResourceSelectValue(), false);
    }
  };

  return (
    <ProvisioningFieldGroup
      title={t('Disk image')}
      titleId={`${fieldId}-group`}
      actions={labelInfo}
    >
      <GuestOsFamilyField
        name={guestOsFamilyName}
        isDisabled={isDisabled}
        onSelect={handleGuestOsFamilySelect}
      />
      {initialDiskImageError && diskImageId && rawGuestOsFamily === undefined ? (
        <Alert variant="danger" isInline title={t('Could not load disk images')}>
          <Stack hasGutter>
            <StackItem>{getErrorMessage(initialDiskImageError)}</StackItem>
            <StackItem>
              <Button variant="link" isInline onClick={() => void refetchInitialDiskImage()}>
                {t('Retry')}
              </Button>
            </StackItem>
          </Stack>
        </Alert>
      ) : null}
      <ResourceSelectField<DiskImage>
        // Remount so PatternFly's internally managed typeahead reflects Formik resets.
        key={diskImageField.value?.id ?? ''}
        name={name}
        label={t('Select the image reference')}
        fieldId={fieldId}
        service={DiskImages}
        request={{ filter: diskImageFilter }}
        isRequired={isRequired}
        isDisabled={isDisabled}
        autoSelectSingleOption={autoSelectSingleOption}
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
