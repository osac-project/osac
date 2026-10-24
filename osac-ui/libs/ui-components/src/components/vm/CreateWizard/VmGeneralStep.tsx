import ProjectField from '@osac/ui-components/components/Form/ProjectField';

import { VM_DISK_IMAGE_FORM_PATH } from './fields';
import NameField from '../../Form/NameField';
import OsacForm from '../../Form/OsacForm';
import DiskImageField from '../../ProvisioningWizard/fields/DiskImageField';
import ProvisioningField from '../../ProvisioningWizard/fields/ProvisioningField';

const VmGeneralStep = () => {
  return (
    <OsacForm>
      <ProjectField />
      <NameField />
      <ProvisioningField name={VM_DISK_IMAGE_FORM_PATH}>
        {({ valueName, isDisabled }) => <DiskImageField name={valueName} isDisabled={isDisabled} />}
      </ProvisioningField>
    </OsacForm>
  );
};

export default VmGeneralStep;
