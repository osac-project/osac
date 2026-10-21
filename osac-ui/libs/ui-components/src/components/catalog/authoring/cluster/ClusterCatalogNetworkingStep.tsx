import { Content, Stack, StackItem, Title } from '@patternfly/react-core';

import ClusterPodCidrField from '../../../Cluster/CreateWizard/fields/ClusterPodCidrField';
import ClusterServiceCidrField from '../../../Cluster/CreateWizard/fields/ClusterServiceCidrField';
import { useTranslation } from '../../../../hooks/useTranslation';
import OsacForm from '../../../Form/OsacForm';
import CatalogField from '../CatalogField';

const ClusterCatalogNetworkingStep = () => {
  const { t } = useTranslation();

  return (
    <Stack hasGutter>
      <StackItem>
        <Title headingLevel="h2" size="lg">
          {t('Networking')}
        </Title>
      </StackItem>
      <StackItem>
        <Content component="p">
          {t(
            'Optionally set pod CIDR and service CIDR defaults for this catalog item. Leave fields Not set if tenants should choose when creating a cluster.',
          )}
        </Content>
      </StackItem>
      <StackItem>
        <OsacForm>
          <CatalogField name="podCidr">
            {({ valueName, isDisabled, isRequired, labelInfo }) => (
              <ClusterPodCidrField
                name={valueName}
                fieldId="cluster-catalog-pod-cidr"
                isDisabled={isDisabled}
                isRequired={isRequired}
                labelInfo={labelInfo}
              />
            )}
          </CatalogField>
          <CatalogField name="serviceCidr">
            {({ valueName, isDisabled, isRequired, labelInfo }) => (
              <ClusterServiceCidrField
                name={valueName}
                fieldId="cluster-catalog-service-cidr"
                isDisabled={isDisabled}
                isRequired={isRequired}
                labelInfo={labelInfo}
              />
            )}
          </CatalogField>
        </OsacForm>
      </StackItem>
    </Stack>
  );
};

export default ClusterCatalogNetworkingStep;
