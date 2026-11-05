import { useFormikContext } from 'formik';

import {
  ComputeInstanceWizardValues,
  VM_SSH_KEY_FORM_PATH,
  VM_USER_DATA_FORM_PATH,
} from './fields';
import SshPublicKeySecretField from './fields/SshPublicKeySecretField';
import OsacForm from '../../Form/OsacForm';
import UserDataField from '../../Form/UserDataField';
import ProvisioningField from '../../ProvisioningWizard/fields/ProvisioningField';
import { useWorkloadTenant } from '../../ProvisioningWizard/useWorkloadTenant';

const VmAccessStep = () => {
  const { values } = useFormikContext<ComputeInstanceWizardValues>();
  const { adminTenant } = useWorkloadTenant();

  return (
    <OsacForm>
      <ProvisioningField name={VM_SSH_KEY_FORM_PATH}>
        {({ valueName, isDisabled }) => (
          <SshPublicKeySecretField
            name={valueName}
            project={values.metadata.project}
            tenant={adminTenant}
            isDisabled={isDisabled}
          />
        )}
      </ProvisioningField>
      <ProvisioningField name={VM_USER_DATA_FORM_PATH}>
        {({ valueName, isDisabled }) => (
          <UserDataField
            name={valueName}
            projectName={values.metadata.project}
            tenant={adminTenant}
            isDisabled={isDisabled}
          />
        )}
      </ProvisioningField>
    </OsacForm>
  );
};

export default VmAccessStep;
