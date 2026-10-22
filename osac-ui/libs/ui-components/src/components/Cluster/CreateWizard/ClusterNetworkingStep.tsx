import { FormSection, Stack, StackItem } from '@patternfly/react-core';

import ClusterPodCidrField from './fields/ClusterPodCidrField';
import ClusterServiceCidrField from './fields/ClusterServiceCidrField';
import { useTranslation } from '../../../hooks/useTranslation';
import OsacForm from '../../Form/OsacForm';
import NetworkFields from '../../ProvisioningWizard/fields/NetworkFields';
import ProvisioningField from '../../ProvisioningWizard/fields/ProvisioningField';

export const ClusterNetworkingStep = () => {
  const { t } = useTranslation();

  return (
    <Stack hasGutter>
      <StackItem>
        <OsacForm>
          <FormSection title={t('Infrastructure Networking')} titleElement="h2">
            <NetworkFields
              networkAttachmentName="spec.networkAttachment"
              externalIpAttachmentName="spec.autoExternalIpAttachment"
              autoExternalIpHelperText={t(
                'Automatically provision external IPs for the cluster API and ingress endpoints.',
              )}
            />
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
