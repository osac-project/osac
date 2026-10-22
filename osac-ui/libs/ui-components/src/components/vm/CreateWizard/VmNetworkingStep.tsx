import { Stack, StackItem } from '@patternfly/react-core';

import { useTranslation } from '../../../hooks/useTranslation';
import OsacForm from '../../Form/OsacForm';
import NetworkFields from '../../ProvisioningWizard/fields/NetworkFields';

export const VmNetworkingStep = () => {
  const { t } = useTranslation();

  return (
    <Stack hasGutter>
      <StackItem>
        <OsacForm>
          <NetworkFields
            networkAttachmentName="spec.networking.networkAttachment"
            externalIpAttachmentName="spec.networking.autoExternalIpAttachment"
            autoExternalIpHelperText={t(
              'Automatically provision an external IP for this virtual machine.',
            )}
          />
        </OsacForm>
      </StackItem>
    </Stack>
  );
};
