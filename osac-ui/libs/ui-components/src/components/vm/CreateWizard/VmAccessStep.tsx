import { useFormikContext } from 'formik';

import {
  ComputeInstanceWizardValues,
  VM_SSH_KEY_FORM_PATH,
  VM_USER_DATA_FORM_PATH,
  VM_USER_DATA_SECRET_FORM_PATH,
  VM_USER_DATA_SOURCE_FORM_PATH,
} from './fields';
import SshPublicKeySecretField from './fields/SshPublicKeySecretField';
import OsacForm from '../../Form/OsacForm';
import UserDataField from '../../Form/UserDataField';
import ProvisioningField from '../../ProvisioningWizard/fields/ProvisioningField';

const VmAccessStep = () => {
  const { values } = useFormikContext<ComputeInstanceWizardValues>();

  return (
    <OsacForm>
      <ProvisioningField name={VM_SSH_KEY_FORM_PATH}>
        {({ valueName, isDisabled }) => (
          <SshPublicKeySecretField
            name={valueName}
            project={values.metadata.project}
            isDisabled={isDisabled}
          />
        )}
      </ProvisioningField>
      <ProvisioningField name={VM_USER_DATA_FORM_PATH}>
        {({ valueName, isDisabled }) => (
          <UserDataField
            sourceName={`${VM_USER_DATA_SOURCE_FORM_PATH}.value`}
            secretRefName={`${VM_USER_DATA_SECRET_FORM_PATH}.value`}
            inlineName={valueName}
            projectName={values.metadata.project}
            isDisabled={isDisabled}
          />
        )}
      </ProvisioningField>
    </OsacForm>
  );
};

export default VmAccessStep;
