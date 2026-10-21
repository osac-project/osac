import { Content, Stack, StackItem, Title } from '@patternfly/react-core';

import { useTranslation } from '../../../../hooks/useTranslation';
import ClusterNodeSetsArrayField from '../../../Cluster/CreateWizard/ClusterNodeSetsArrayField';
import ClusterVersionField from '../../../Cluster/CreateWizard/fields/ClusterVersionField';
import OsacForm from '../../../Form/OsacForm';
import ProvisioningFieldGroup from '../../../ProvisioningWizard/fields/ProvisioningFieldGroup';
import CatalogField from '../CatalogField';

const ClusterCatalogConfigurationStep = () => {
  const { t } = useTranslation();

  return (
    <Stack hasGutter>
      <StackItem>
        <Title headingLevel="h2" size="lg">
          {t('Configuration')}
        </Title>
      </StackItem>
      <StackItem>
        <Content component="p">
          {t(
            'Optionally set cluster version and node set defaults for this catalog item. Leave fields Not set if tenants should choose when creating a cluster.',
          )}
        </Content>
      </StackItem>
      <StackItem>
        <OsacForm>
          <CatalogField name="version">
            {({ valueName, isDisabled, isRequired, labelInfo }) => (
              <ClusterVersionField
                name={valueName}
                fieldId="cluster-catalog-version"
                isDisabled={isDisabled}
                isRequired={isRequired}
                labelInfo={labelInfo}
              />
            )}
          </CatalogField>
          <CatalogField name="nodeSets">
            {({ valueName, isDisabled, isRequired, labelInfo }) => (
              <ProvisioningFieldGroup
                title={t('Node sets')}
                titleId="cluster-catalog-node-sets"
                actions={labelInfo}
              >
                <ClusterNodeSetsArrayField
                  name={valueName}
                  isDisabled={isDisabled}
                  isRequired={isRequired}
                  allowEmpty
                />
              </ProvisioningFieldGroup>
            )}
          </CatalogField>
        </OsacForm>
      </StackItem>
    </Stack>
  );
};

export default ClusterCatalogConfigurationStep;
