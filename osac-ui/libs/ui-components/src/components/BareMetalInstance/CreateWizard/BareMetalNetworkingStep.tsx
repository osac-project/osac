import { Stack, StackItem } from '@patternfly/react-core';

import { useTranslation } from '../../../hooks/useTranslation';
import OsacForm from '../../Form/OsacForm';
import NetworkFields from '../../ProvisioningWizard/fields/NetworkFields';

export const BareMetalNetworkingStep = () => {
  const { t } = useTranslation();

  return (
    <Stack hasGutter>
      <StackItem>
        <OsacForm>
          <NetworkFields
            networkAttachmentName="spec.networkAttachment"
            externalIpAttachmentName="spec.autoExternalIpAttachment"
            autoExternalIpHelperText={t(
              'The system will auto-create an ExternalIP and ExternalIPAttachment bound to the primary attachment. Auto-created resources are deleted when the instance is deleted.',
            )}
          />
        </OsacForm>
      </StackItem>
    </Stack>
  );
};
