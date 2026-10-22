import { useFormikContext } from 'formik';

import ProjectField from '@osac/ui-components/components/Form/ProjectField';

import {
  BM_SSH_KEY_FORM_PATH,
  BM_USER_DATA_SECRET_FORM_PATH,
  type BareMetalInstanceWizardValues,
} from './fields';
import NameField from '../../Form/NameField';
import OsacForm from '../../Form/OsacForm';
import SshKeyField from '../../Form/SshKeyField';
import ProvisioningField from '../../ProvisioningWizard/fields/ProvisioningField';

const BareMetalGeneralStep = () => {
  const { values, setFieldValue } = useFormikContext<BareMetalInstanceWizardValues>();

  return (
    <OsacForm>
      <ProjectField
        onSelect={() => {
          if (!values.spec.userDataSecret.locked) {
            void setFieldValue(`${BM_USER_DATA_SECRET_FORM_PATH}.value`, '');
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
