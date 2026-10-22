import { AdditionalDisksArrayField } from './AdditionalDisksArrayField';
import BootDiskSizeField from './fields/BootDiskSizeField';
import { useTranslation } from '../../../hooks/useTranslation';
import OsacForm from '../../Form/OsacForm';
import { StorageTierSelectField } from '../../Form/StorageTierSelectField';
import ProvisioningField from '../../ProvisioningWizard/fields/ProvisioningField';

export const VmStorageStep = () => {
  const { t } = useTranslation();

  return (
    <OsacForm>
      <ProvisioningField name="spec.bootDisk.sizeGib">
        {({ valueName, isDisabled }) => (
          <BootDiskSizeField name={valueName} isDisabled={isDisabled} />
        )}
      </ProvisioningField>
      <ProvisioningField name="spec.bootDisk.storageTier">
        {({ valueName, isDisabled }) => (
          <StorageTierSelectField
            name={valueName}
            label={t('Storage tier')}
            fieldId="vm-boot-disk-storage-tier"
            isDisabled={isDisabled}
          />
        )}
      </ProvisioningField>
      <ProvisioningField name="spec.additionalDisks">
        {({ valueName, isDisabled }) => (
          <AdditionalDisksArrayField name={valueName} isDisabled={isDisabled} />
        )}
      </ProvisioningField>
    </OsacForm>
  );
};
