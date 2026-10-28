import { useEffect, useState } from 'react';
import { Content, Divider, ExpandableSection } from '@patternfly/react-core';
import { useField } from 'formik';

import ClusterPodCidrField from './fields/ClusterPodCidrField';
import ClusterServiceCidrField from './fields/ClusterServiceCidrField';
import { useTranslation } from '../../../hooks/useTranslation';
import OsacForm from '../../Form/OsacForm';
import NetworkFields from '../../ProvisioningWizard/fields/NetworkFields';
import ProvisioningField from '../../ProvisioningWizard/fields/ProvisioningField';

export const ClusterNetworkingStep = () => {
  const { t } = useTranslation();
  const [isClusterNetworkExpanded, setIsClusterNetworkExpanded] = useState(false);
  const [, podCidrMeta] = useField('spec.network.podCidr.value');
  const [, serviceCidrMeta] = useField('spec.network.serviceCidr.value');

  useEffect(() => {
    if (podCidrMeta.error || serviceCidrMeta.error) {
      setIsClusterNetworkExpanded(true);
    }
  }, [podCidrMeta.error, serviceCidrMeta.error]);

  return (
    <OsacForm>
      <NetworkFields
        networkAttachmentName="spec.networkAttachment"
        externalIpAttachmentName="spec.autoExternalIpAttachment"
        securityGroupsRequired
        singleSecurityGroup
      />

      <Divider />
      <ExpandableSection
        isExpanded={isClusterNetworkExpanded}
        onToggle={(_event, expanded) => setIsClusterNetworkExpanded(expanded)}
        toggleTextCollapsed={t('Customize cluster network')}
        toggleTextExpanded={t('Hide cluster network')}
        toggleWrapper="h2"
      >
        <Content component="p">
          {t('Address ranges used inside the cluster for pods and services.')}
        </Content>
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
      </ExpandableSection>
    </OsacForm>
  );
};
