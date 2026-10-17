import { Stack, StackItem } from '@patternfly/react-core';

import type { BareMetalInstanceCatalogItem } from '@osac/types';

import { useTranslation } from '../../../hooks/useTranslation';
import OsacForm from '../../Form/OsacForm';
import NetworkAttachmentFields from '../../ProvisioningWizard/fields/NetworkAttachmentFields';
import ProvisioningField from '../../ProvisioningWizard/fields/ProvisioningField';

interface Props {
  catalogItem: BareMetalInstanceCatalogItem | null;
}

export const BareMetalNetworkingStep = ({ catalogItem }: Props) => {
  const { t } = useTranslation();

  if (!catalogItem) {
    return null;
  }

  return (
    <Stack hasGutter>
      <StackItem>
        <OsacForm>
          <ProvisioningField name="spec.networking.networkAttachment">
            {({ isDisabled, valueName }) => (
              <NetworkAttachmentFields
                useDefaultNetworkName={`${valueName}.useDefaults`}
                virtualNetworkName={`${valueName}.attachments.0.virtualNetwork`}
                subnetName={`${valueName}.attachments.0.subnet`}
                securityGroupsName={`${valueName}.attachments.0.securityGroups`}
                autoExternalIpName="spec.networking.attachExternalIp"
                fieldIdPrefix="bm-attachment-0"
                isDisabled={isDisabled}
                autoExternalIpHelperText={t(
                  'The system will auto-create an ExternalIP and ExternalIPAttachment bound to the primary attachment. Auto-created resources are deleted when the instance is deleted.',
                )}
              />
            )}
          </ProvisioningField>
        </OsacForm>
      </StackItem>
    </Stack>
  );
};
