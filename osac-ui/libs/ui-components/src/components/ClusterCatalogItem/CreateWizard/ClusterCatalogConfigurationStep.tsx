import { Stack, StackItem, Title } from '@patternfly/react-core';

import { type ClusterVersion, ClusterVersions } from '@osac/types';

import { CLUSTER_VERSION_ACTIVE_LIST_FILTER } from '../../../api/v1/cluster-versions';
import { useTranslation } from '../../../hooks/useTranslation';
import CatalogField from '../../CatalogItem/CatalogField';
import CatalogFieldPolicyDescription from '../../CatalogItem/CatalogFieldPolicyDescription';
import ClusterNodeSetsArrayField from '../../Cluster/CreateWizard/ClusterNodeSetsArrayField';
import { isDeprecatedVersion, versionDisplayName } from '../../Cluster/CreateWizard/versionUtils';
import OsacForm from '../../Form/OsacForm';
import { ResourceSelectField } from '../../Form/ResourceSelectField';
import ProvisioningFieldGroup from '../../ProvisioningWizard/fields/ProvisioningFieldGroup';

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
              <ResourceSelectField<ClusterVersion>
                name={valueName}
                label={t('Version')}
                fieldId="cluster-catalog-version"
                service={ClusterVersions}
                request={{ filter: CLUSTER_VERSION_ACTIVE_LIST_FILTER }}
                isDisabled={isDisabled}
                isRequired={isRequired}
                labelInfo={labelInfo}
                autoSelectSingleOption={false}
                placeholder={t('Select a version')}
                loadErrorTitle={t('Could not load cluster versions')}
                formatOptionLabel={(version) => {
                  const versionName = version.metadata?.name ?? '';
                  const label = versionDisplayName(version, versionName);
                  return isDeprecatedVersion(version)
                    ? t('{{version}} (deprecated)', { version: label })
                    : label;
                }}
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
