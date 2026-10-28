import ComputeInstanceTypeField from './fields/ComputeInstanceTypeField';
import OsacForm from '../../Form/OsacForm';
import ProvisioningField from '../../ProvisioningWizard/fields/ProvisioningField';

export const VmConfigurationStep = () => {
  return (
    <OsacForm>
      <ProvisioningField name="spec.instanceType">
        {({ valueName, isDisabled }) => (
          <ComputeInstanceTypeField name={valueName} isDisabled={isDisabled} />
        )}
      </ProvisioningField>
    </OsacForm>
  );
};
