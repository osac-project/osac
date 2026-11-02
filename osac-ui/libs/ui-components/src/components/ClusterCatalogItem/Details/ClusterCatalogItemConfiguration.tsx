import { Content, DescriptionList } from '@patternfly/react-core';

import type { ClusterCatalogItem } from '@osac/types';

import { useTranslation } from '../../../hooks/useTranslation';
import { displayValue } from '../../../utils/detailFormatters';
import { catalogFieldPolicyValue } from '../../catalog/catalogFieldPolicyDisplay';
import {
  clusterCatalogItemFields,
  clusterNodeSetMapPolicy,
  clusterVersionReference,
  formatClusterCatalogVersionLabel,
} from '../../catalog/clusterCatalogItemResourceDisplay';
import {
  CatalogConfigurationRow,
  hasConfiguredPolicy,
} from '../../CatalogItem/Details/CatalogConfigurationRow';
import CatalogNodeSets from '../../CatalogItem/Details/CatalogNodeSets';

interface ClusterCatalogItemConfigurationProps {
  item: ClusterCatalogItem;
}

const ClusterCatalogItemConfiguration = ({ item }: ClusterCatalogItemConfigurationProps) => {
  const { t } = useTranslation();
  const fields = clusterCatalogItemFields(item);
  const versionPolicy = fields?.version;
  const versionReference = clusterVersionReference(fields);
  const nodeSetsPolicy = clusterNodeSetMapPolicy(fields);
  const network = fields?.network;
  const hasConfiguredField = hasConfiguredPolicy(
    versionPolicy,
    nodeSetsPolicy,
    network?.podCidr,
    network?.serviceCidr,
  );

  if (!hasConfiguredField) {
    return <Content component="p">{t('No governed configuration')}</Content>;
  }

  return (
    <DescriptionList isCompact>
      <CatalogConfigurationRow
        label={t('Cluster version')}
        value={
          versionReference
            ? displayValue(formatClusterCatalogVersionLabel(undefined, versionReference))
            : undefined
        }
        policy={versionPolicy}
      />
      <CatalogNodeSets policy={nodeSetsPolicy} />
      <CatalogConfigurationRow
        label={t('Pod CIDR')}
        value={displayValue(catalogFieldPolicyValue(network?.podCidr))}
        policy={network?.podCidr}
      />
      <CatalogConfigurationRow
        label={t('Service CIDR')}
        value={displayValue(catalogFieldPolicyValue(network?.serviceCidr))}
        policy={network?.serviceCidr}
      />
    </DescriptionList>
  );
};

export default ClusterCatalogItemConfiguration;
