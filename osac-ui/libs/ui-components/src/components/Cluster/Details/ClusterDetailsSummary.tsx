import {
  DescriptionList,
  DescriptionListDescription,
  DescriptionListGroup,
  DescriptionListTerm,
  Stack,
  StackItem,
  Title,
} from '@patternfly/react-core';

import type { Cluster } from '@osac/types';

import { useTranslation } from '../../../hooks/useTranslation';
import { displayValue } from '../../../utils/detailFormatters';

interface ClusterDetailsSummaryProps {
  cluster: Cluster;
}

const sumNodeSetSizes = (nodeSets?: Record<string, { size?: number }>) =>
  Object.values(nodeSets ?? {}).reduce((sum, nodeSet) => sum + (nodeSet?.size ?? 0), 0);

const ClusterDetailsSummary = ({ cluster }: ClusterDetailsSummaryProps) => {
  const { t } = useTranslation();
  const catalogItem = cluster.spec?.catalogItem;

  return (
    <Stack hasGutter>
      <StackItem>
        <Title headingLevel="h2" size="lg">
          {t('Overview')}
        </Title>
      </StackItem>
      <StackItem>
        <DescriptionList isCompact aria-label={t('Cluster overview')}>
          <DescriptionListGroup>
            <DescriptionListTerm>{t('Catalog item')}</DescriptionListTerm>
            <DescriptionListDescription>
              {displayValue(catalogItem?.name || catalogItem?.id)}
            </DescriptionListDescription>
          </DescriptionListGroup>
          <DescriptionListGroup>
            <DescriptionListTerm>{t('Worker nodes')}</DescriptionListTerm>
            <DescriptionListDescription>
              {sumNodeSetSizes(cluster.spec?.nodeSets)}
            </DescriptionListDescription>
          </DescriptionListGroup>
          <DescriptionListGroup>
            <DescriptionListTerm>{t('API URL')}</DescriptionListTerm>
            <DescriptionListDescription>
              {displayValue(cluster.status?.apiUrl)}
            </DescriptionListDescription>
          </DescriptionListGroup>
          <DescriptionListGroup>
            <DescriptionListTerm>{t('Console URL')}</DescriptionListTerm>
            <DescriptionListDescription>
              {displayValue(cluster.status?.consoleUrl)}
            </DescriptionListDescription>
          </DescriptionListGroup>
        </DescriptionList>
      </StackItem>
    </Stack>
  );
};

export default ClusterDetailsSummary;
