import { Stack, StackItem, Title } from '@patternfly/react-core';

import { useTranslation } from '../../../../hooks/useTranslation';
import ClusterNodeSetsArrayField from '../../../Cluster/CreateWizard/ClusterNodeSetsArrayField';
import ClusterVersionField from '../../../Cluster/CreateWizard/fields/ClusterVersionField';
import OsacForm from '../../../Form/OsacForm';
import ProvisioningFieldGroup from '../../../ProvisioningWizard/fields/ProvisioningFieldGroup';
import CatalogField from '../CatalogField';
import CatalogFieldPolicyDescription from '../CatalogFieldPolicyDescription';

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
        <CatalogFieldPolicyDescription
          description={t(
            'Configure the cluster version and node sets for clusters created from this catalog item.',
          )}
        />
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
                autoSelectSingleOption={false}
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
                  autoSelectSingleOption={false}
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
