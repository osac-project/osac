import { FormSection, Stack, StackItem } from '@patternfly/react-core';

import type { ClusterCatalogItem } from '@osac/types';

import { useTranslation } from '../../../hooks/useTranslation';
import OsacForm from '../../Form/OsacForm';
import ClusterPodCidrField from '../../ProvisioningWizard/fields/ClusterPodCidrField';
import ClusterServiceCidrField from '../../ProvisioningWizard/fields/ClusterServiceCidrField';
import NetworkAttachmentFields from '../../ProvisioningWizard/fields/NetworkAttachmentFields';
import ProvisioningField from '../../ProvisioningWizard/fields/ProvisioningField';

interface Props {
  catalogItem: ClusterCatalogItem | null;
}

export const ClusterNetworkingStep = ({ catalogItem }: Props) => {
  const { t } = useTranslation();

  if (!catalogItem) {
    return null;
  }

  return (
    <Stack hasGutter>
      <StackItem>
        <OsacForm>
          <FormSection title={t('Infrastructure Networking')} titleElement="h2">
            <ProvisioningField name="spec.useDefaultNetwork">
              {({ isDisabled, valueName }) => (
                <NetworkAttachmentFields
                  useDefaultNetworkName={valueName}
                  virtualNetworkName="spec.networkAttachment.virtualNetwork"
                  subnetName="spec.networkAttachment.subnet"
                  securityGroupsName="spec.networkAttachment.securityGroups"
                  autoExternalIpName="spec.autoExternalIpAttachment"
                  fieldIdPrefix="cluster"
                  isDisabled={isDisabled}
                  autoExternalIpHelperText={t(
                    'Automatically provision external IPs for the cluster API and ingress endpoints.',
                  )}
                />
              )}
            </ProvisioningField>
          </FormSection>
          <FormSection title={t('Cluster Networking')} titleElement="h2">
            <ProvisioningField name="spec.network.podCidr">
              {({ valueName, isDisabled }) => (
                <ClusterPodCidrField name={valueName} isDisabled={isDisabled} />
              )}
            </ProvisioningField>
            <ProvisioningField name="spec.network.serviceCidr">
              {({ valueName, isDisabled }) => (
                <ClusterServiceCidrField name={valueName} isDisabled={isDisabled} />
              )}
            </ProvisioningField>
          </FormSection>
        </OsacForm>
      </StackItem>
    </Stack>
  );
};
