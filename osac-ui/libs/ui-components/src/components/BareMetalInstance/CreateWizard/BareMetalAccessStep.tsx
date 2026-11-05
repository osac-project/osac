import { useFormikContext } from 'formik';

import {
  BM_SSH_KEY_FORM_PATH,
  BM_USER_DATA_FORM_PATH,
  type BareMetalInstanceWizardValues,
} from './fields';
import OsacForm from '../../Form/OsacForm';
import SshPublicKeySecretField from '../../Form/SshPublicKeySecretField';
import UserDataField from '../../Form/UserDataField';
import ProvisioningField from '../../ProvisioningWizard/fields/ProvisioningField';
import { useWorkloadTenant } from '../../ProvisioningWizard/useWorkloadTenant';

const BareMetalAccessStep = () => {
  const { values } = useFormikContext<BareMetalInstanceWizardValues>();
  const { adminTenant } = useWorkloadTenant();

  return (
    <OsacForm>
      <ProvisioningField name={BM_SSH_KEY_FORM_PATH}>
        {({ valueName, isDisabled }) => (
          <SshPublicKeySecretField
            name={valueName}
            project={values.metadata.project}
            tenant={adminTenant}
            isDisabled={isDisabled}
          />
        )}
      </ProvisioningField>
      <ProvisioningField name={BM_USER_DATA_FORM_PATH}>
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

export default BareMetalAccessStep;
