import { Stack, StackItem } from '@patternfly/react-core';
import { useFormikContext } from 'formik';

import {
  BM_DISK_IMAGE_FORM_PATH,
  BM_INSTANCE_TYPE_FORM_PATH,
  BM_USER_DATA_FORM_PATH,
  type BareMetalInstanceWizardValues,
} from './fields';
import BareMetalInstanceTypeField from './fields/BareMetalInstanceTypeField';
import OsacForm from '../../Form/OsacForm';
import UserDataField from '../../Form/UserDataField';
import DiskImageField from '../../ProvisioningWizard/fields/DiskImageField';
import ProvisioningField from '../../ProvisioningWizard/fields/ProvisioningField';

const BareMetalConfigurationStep = () => {
  const { values } = useFormikContext<BareMetalInstanceWizardValues>();

  return (
    <Stack hasGutter>
      <StackItem>
        <OsacForm>
          <ProvisioningField name={BM_INSTANCE_TYPE_FORM_PATH}>
            {({ valueName, isDisabled }) => (
              <BareMetalInstanceTypeField name={valueName} isDisabled={isDisabled} />
            )}
          </ProvisioningField>
          <ProvisioningField name={BM_DISK_IMAGE_FORM_PATH}>
            {({ valueName, isDisabled }) => (
              <DiskImageField
                name={valueName}
                guestOsFamilyName="spec.guestOsFamily"
                isDisabled={isDisabled}
              />
            )}
          </ProvisioningField>
          <ProvisioningField name={BM_USER_DATA_FORM_PATH}>
            {({ valueName, isDisabled }) => (
              <UserDataField
                name={valueName}
                projectName={values.metadata.project}
                isDisabled={isDisabled}
              />
            )}
          </ProvisioningField>
        </OsacForm>
      </StackItem>
    </Stack>
  );
};

export default BareMetalConfigurationStep;
