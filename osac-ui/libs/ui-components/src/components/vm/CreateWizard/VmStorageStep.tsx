import { AdditionalDisksArrayField } from './AdditionalDisksArrayField';
import BootDiskSizeField from './fields/BootDiskSizeField';
import BootStorageTierField from './fields/BootStorageTierField';
import { useTranslation } from '../../../hooks/useTranslation';
import OsacForm from '../../Form/OsacForm';
import ProvisioningField from '../../ProvisioningWizard/fields/ProvisioningField';
import ProvisioningFieldGroup from '../../ProvisioningWizard/fields/ProvisioningFieldGroup';

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
          <BootStorageTierField name={valueName} isDisabled={isDisabled} />
        )}
      </ProvisioningField>
      <ProvisioningField name="spec.additionalDisks">
        {({ valueName, isDisabled }) => (
          <ProvisioningFieldGroup title={t('Additional disks')} titleId="vm-additional-disks">
            <AdditionalDisksArrayField name={valueName} isDisabled={isDisabled} />
          </ProvisioningFieldGroup>
        )}
      </ProvisioningField>
    </OsacForm>
  );
};
