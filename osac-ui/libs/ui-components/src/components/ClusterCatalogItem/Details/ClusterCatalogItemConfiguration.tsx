import { Content, DescriptionList } from '@patternfly/react-core';

import type { ClusterCatalogItem } from '@osac/types';

import { useTranslation } from '../../../hooks/useTranslation';
import { displayValue } from '../../../utils/detailFormatters';
import {
  catalogFieldPolicyIsConfigured,
  catalogFieldPolicyValue,
} from '../../catalog/catalogFieldPolicyDisplay';
import { formatClusterCatalogVersionLabel } from '../../catalog/clusterCatalogItemResourceDisplay';
import { CatalogConfigurationRow } from '../../CatalogItem/Details/CatalogConfigurationRow';
import CatalogNodeSets from '../../CatalogItem/Details/CatalogNodeSets';

interface ClusterCatalogItemConfigurationProps {
  item: ClusterCatalogItem;
}

const ClusterCatalogItemConfiguration = ({ item }: ClusterCatalogItemConfigurationProps) => {
  const { t } = useTranslation();
  const fields = item.fields;
  const versionPolicy = fields?.version;
  const versionReference = catalogFieldPolicyValue(fields?.version);
  const nodeSetsPolicy = fields?.nodeSets;
  const network = fields?.network;
  const hasConfiguredField = [
    versionPolicy,
    nodeSetsPolicy,
    network?.podCidr,
    network?.serviceCidr,
  ].some(catalogFieldPolicyIsConfigured);

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
            : null
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
