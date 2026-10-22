import { DiskImages } from '@osac/types';
import { DISK_IMAGE_NON_OBSOLETE_FILTER } from '@osac/ui-components/api/v1/disk-image';
import { ResourceSelectField } from '@osac/ui-components/components/Form/ResourceSelectField';

import type { ProvisioningFieldControlProps } from './provisioningFieldValue';
import { useTranslation } from '../../../hooks/useTranslation';

interface DiskImageFieldProps extends ProvisioningFieldControlProps {
  fieldId?: string;
}

const DiskImageField = ({
  name,
  fieldId = 'disk-image',
  isDisabled = false,
}: DiskImageFieldProps) => {
  const { t } = useTranslation();

  return (
    <ResourceSelectField
      name={name}
      label={t('Disk image')}
      fieldId={fieldId}
      service={DiskImages}
      request={{ filter: DISK_IMAGE_NON_OBSOLETE_FILTER }}
      isRequired
      isDisabled={isDisabled}
      placeholder={t('Select a disk image')}
      loadErrorTitle={t('Could not load disk images')}
      emptyTitle={t('No disk images available')}
      emptyDescription={t('Contact your administrator to have a disk image provisioned.')}
    />
  );
};

export default DiskImageField;
