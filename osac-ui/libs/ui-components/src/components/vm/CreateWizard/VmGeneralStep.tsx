import { useFormikContext } from 'formik';

import ProjectField from '@osac/ui-components/components/Form/ProjectField';

import {
  ComputeInstanceWizardValues,
  VM_SSH_KEY_FORM_PATH,
  VM_USER_DATA_SECRET_FORM_PATH,
} from './fields';
import NameField from '../../Form/NameField';
import OsacForm from '../../Form/OsacForm';

const VmGeneralStep = () => {
  const { values, setFieldValue } = useFormikContext<ComputeInstanceWizardValues>();

  return (
    <OsacForm>
      <ProjectField
        onSelect={() => {
          if (!values.spec.userDataSecret.locked) {
            void setFieldValue(`${VM_USER_DATA_SECRET_FORM_PATH}.value`, '');
          }
          if (!values.spec.sshKey.locked) {
            void setFieldValue(`${VM_SSH_KEY_FORM_PATH}.value`, '');
          }
        }}
      />
      <NameField />
    </OsacForm>
  );
};

export default VmGeneralStep;
