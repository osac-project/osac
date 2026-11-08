import { useFormikContext } from 'formik';

import ProjectField from '@osac/ui-components/components/Form/ProjectField';

import {
  BM_SSH_KEY_FORM_PATH,
  BM_USER_DATA_FORM_PATH,
  type BareMetalInstanceWizardValues,
} from './fields';
import NameField from '../../Form/NameField';
import OsacForm from '../../Form/OsacForm';

const BareMetalGeneralStep = () => {
  const { values, setFieldValue } = useFormikContext<BareMetalInstanceWizardValues>();

  return (
    <OsacForm>
      <ProjectField
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
    </OsacForm>
  );
};

export default BareMetalGeneralStep;
