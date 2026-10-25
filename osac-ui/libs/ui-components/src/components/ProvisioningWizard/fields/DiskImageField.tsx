import { DiskImages } from '@osac/types';
import { DISK_IMAGE_NON_OBSOLETE_FILTER } from '@osac/ui-components/api/v1/disk-image';
import { ResourceSelectField } from '@osac/ui-components/components/Form/ResourceSelectField';

import GuestOsFamilyField from './GuestOsFamilyField';
import ProvisioningFieldGroup from './ProvisioningFieldGroup';
import type { ProvisioningFieldControlProps } from './provisioningFieldValue';
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
        request={{ filter: DISK_IMAGE_NON_OBSOLETE_FILTER }}
        isRequired={isRequired}
        isDisabled={isDisabled}
        placeholder={t('Select a disk image')}
        loadErrorTitle={t('Could not load disk images')}
        emptyTitle={t('No disk images available')}
        emptyDescription={t('Contact your administrator to have a disk image provisioned.')}
      />
    </ProvisioningFieldGroup>
  );
};

export default DiskImageField;
