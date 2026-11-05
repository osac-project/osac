import {
  Content,
  Divider,
  Flex,
  FlexItem,
  Label,
  Stack,
  StackItem,
  Title,
} from '@patternfly/react-core';
import { useFormikContext } from 'formik';

import { GuestOSFamily } from '@osac/types';

import {
  ComputeInstanceWizardValues,
  VM_SSH_KEY_FORM_PATH,
  VM_USER_DATA_FORM_PATH,
} from './fields';
import { useTranslation } from '../../../hooks/useTranslation';
import OsacForm from '../../Form/OsacForm';
import SshPublicKeySecretField from '../../Form/SshPublicKeySecretField';
import UserDataField from '../../Form/UserDataField';
import ProvisioningField from '../../ProvisioningWizard/fields/ProvisioningField';
import { useWorkloadTenant } from '../../ProvisioningWizard/useWorkloadTenant';

const VmAccessStep = () => {
  const { t } = useTranslation();
  const { values } = useFormikContext<ComputeInstanceWizardValues>();
  const { adminTenant } = useWorkloadTenant();
  const isLinux =
    values.spec.guestOsFamily === GuestOSFamily.GUEST_OS_FAMILY_LINUX ||
    values.spec.guestOsFamily === GuestOSFamily.GUEST_OS_FAMILY_UNSPECIFIED;
  const userDataLabel = isLinux ? t('Cloud-init') : t('Sysprep');
  const userDataDescription = isLinux
    ? t('User data applied when the virtual machine starts.')
    : t('Supply an Unattend.xml for first-boot customization.');
  const headerDescription = isLinux
    ? t('Configure access and the first-boot initialization for this virtual machine.')
    : t('Configure first-boot customization for this Windows virtual machine.');

  return (
    <Stack hasGutter>
      <StackItem>
        <Title headingLevel="h2" size="lg">
          {t('Access & Initial run')}
        </Title>
      </StackItem>
      <StackItem>
        <Content component="p">{headerDescription}</Content>
      </StackItem>
      <StackItem>
        <OsacForm>
          {isLinux && (
            <ProvisioningField name={VM_SSH_KEY_FORM_PATH}>
              {({ valueName, isDisabled }) => (
                <SshPublicKeySecretField
                  name={valueName}
                  project={values.metadata.project}
                  tenant={adminTenant}
                  isDisabled={isDisabled}
                  placeholder={t('Select an SSH public key')}
                />
              )}
            </ProvisioningField>
          )}
          <Divider />
          <Flex
            alignItems={{ default: 'alignItemsCenter' }}
            spaceItems={{ default: 'spaceItemsSm' }}
          >
            <FlexItem>
              <Title headingLevel="h3" size="md">
                {userDataLabel}
              </Title>
            </FlexItem>
            {!isLinux && (
              <FlexItem>
                <Label color="grey" variant="outline" isCompact>
                  {t('Windows only')}
                </Label>
              </FlexItem>
            )}
          </Flex>
          <div>{userDataDescription}</div>
          <ProvisioningField name={VM_USER_DATA_FORM_PATH}>
            {({ valueName, isDisabled }) => (
              <UserDataField
                label=""
                inlinePlaceholder={
                  isLinux ? t('Add cloud-init configuration') : t('Paste Unattend.xml content')
                }
                name={valueName}
                projectName={values.metadata.project}
                tenant={adminTenant}
                isDisabled={isDisabled}
              />
            )}
          </ProvisioningField>
        </OsacForm>
      </StackItem>
    </Stack>
  );
};

export default VmAccessStep;
