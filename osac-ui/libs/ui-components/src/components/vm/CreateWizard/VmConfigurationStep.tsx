import { Divider } from '@patternfly/react-core';

import { VM_DISK_IMAGE_FORM_PATH } from './fields';
import ComputeInstanceTypeField from './fields/ComputeInstanceTypeField';
import OsacForm from '../../Form/OsacForm';
import DiskImageField from '../../ProvisioningWizard/fields/DiskImageField';
import ProvisioningField from '../../ProvisioningWizard/fields/ProvisioningField';
import { useWorkloadTenant } from '../../ProvisioningWizard/useWorkloadTenant';

export const VmConfigurationStep = () => {
  const { adminTenant } = useWorkloadTenant();
  return (
    <OsacForm>
      <ProvisioningField name={VM_DISK_IMAGE_FORM_PATH}>
        {({ valueName, isDisabled }) => (
          <DiskImageField
            name={valueName}
            guestOsFamilyName="spec.guestOsFamily"
            tenant={adminTenant}
            isDisabled={isDisabled}
          />
        )}
      </ProvisioningField>
      <Divider />
      <ProvisioningField name="spec.instanceType">
        {({ valueName, isDisabled }) => (
          <ComputeInstanceTypeField name={valueName} isDisabled={isDisabled} />
        )}
      </ProvisioningField>
    </OsacForm>
  );
};
