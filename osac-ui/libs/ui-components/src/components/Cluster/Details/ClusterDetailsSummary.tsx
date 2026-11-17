import {
  DescriptionList,
  DescriptionListDescription,
  DescriptionListGroup,
  DescriptionListTerm,
  Stack,
  StackItem,
  Title,
} from '@patternfly/react-core';
import InfoAltIcon from '@patternfly/react-icons/dist/esm/icons/info-alt-icon';

import { type Cluster, ClusterCatalogItems } from '@osac/types';

import ClusterProjectLink from './ClusterProjectLink';
import ClusterResourceLink from './ClusterResourceLink';
import { useTranslation } from '../../../hooks/useTranslation';
import { displayValue } from '../../../utils/detailFormatters';
import { ClusterStatusLabel } from '../ListPage/ClusterStatusLabel';

interface ClusterDetailsSummaryProps {
  cluster: Cluster;
}

const ClusterDetailsSummary = ({ cluster }: ClusterDetailsSummaryProps) => {
  const { t } = useTranslation();
  const catalogItem = cluster.spec?.catalogItem;

  return (
    <Stack hasGutter>
      <StackItem>
        <Title headingLevel="h2" size="lg" className="cluster-details__section-title">
          <InfoAltIcon aria-hidden />
          {t('Overview')}
        </Title>
      </StackItem>
      <StackItem>
        <DescriptionList isCompact aria-label={t('Cluster overview')}>
          <DescriptionListGroup>
            <DescriptionListTerm>{t('Status')}</DescriptionListTerm>
            <DescriptionListDescription>
              <ClusterStatusLabel state={cluster.status?.state} />
            </DescriptionListDescription>
          </DescriptionListGroup>
          <DescriptionListGroup>
            <DescriptionListTerm>{t('Catalog item')}</DescriptionListTerm>
            <DescriptionListDescription>
              <ClusterResourceLink
                service={ClusterCatalogItems}
                basePath="/catalog/cluster"
                id={catalogItem?.id}
                name={catalogItem?.name}
              />
            </DescriptionListDescription>
          </DescriptionListGroup>
          <DescriptionListGroup>
            <DescriptionListTerm>{t('Project')}</DescriptionListTerm>
            <DescriptionListDescription>
              <ClusterProjectLink cluster={cluster} />
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
