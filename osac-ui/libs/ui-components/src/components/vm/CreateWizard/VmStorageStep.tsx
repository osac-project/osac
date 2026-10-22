import { AdditionalDisksArrayField } from './AdditionalDisksArrayField';
import BootDiskSizeField from './fields/BootDiskSizeField';
import BootStorageTierField from './fields/BootStorageTierField';
import OsacForm from '../../Form/OsacForm';
import ProvisioningField from '../../ProvisioningWizard/fields/ProvisioningField';

export const VmStorageStep = () => {
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
          <AdditionalDisksArrayField name={valueName} isDisabled={isDisabled} />
        )}
      </ProvisioningField>
    </OsacForm>
  );
};
