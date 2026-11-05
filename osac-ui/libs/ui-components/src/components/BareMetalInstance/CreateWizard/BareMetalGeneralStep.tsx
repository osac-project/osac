import { useFormikContext } from 'formik';

import ProjectField from '@osac/ui-components/components/Form/ProjectField';

import {
  BM_SSH_KEY_FORM_PATH,
  BM_USER_DATA_FORM_PATH,
  type BareMetalInstanceWizardValues,
} from './fields';
import NameField from '../../Form/NameField';
import OsacForm from '../../Form/OsacForm';
import SshKeyField from '../../Form/SshKeyField';
import ProvisioningField from '../../ProvisioningWizard/fields/ProvisioningField';
import { useWorkloadTenant } from '../../ProvisioningWizard/useWorkloadTenant';

const BareMetalGeneralStep = () => {
  const { values, setFieldValue } = useFormikContext<BareMetalInstanceWizardValues>();
  const { adminTenant } = useWorkloadTenant();

  return (
    <OsacForm>
      <ProjectField
        tenant={adminTenant}
        onSelect={() => {
          if (!values.spec.userData.locked) {
            void setFieldValue(`${BM_USER_DATA_FORM_PATH}.value.secret`, '');
          }
          if (!values.spec.sshKey.locked) {
            void setFieldValue(`${BM_SSH_KEY_FORM_PATH}.value`, '');
          }
        }}
      />
      <NameField />
      <ProvisioningField name={BM_SSH_KEY_FORM_PATH}>
        {({ valueName, isDisabled }) => <SshKeyField name={valueName} isDisabled={isDisabled} />}
      </ProvisioningField>
    </OsacForm>
  );
};

export default BareMetalGeneralStep;
