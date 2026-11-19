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
import ResourceLink from '../../Resource/ResourceLink';
import { ClusterStatusLabel } from '../ListPage/ClusterStatusLabel';

interface ClusterDetailsSummaryProps {
  cluster: Cluster;
}

const ClusterDetailsSummary = ({ cluster }: ClusterDetailsSummaryProps) => {
  const { t } = useTranslation();
  const catalogItem = cluster.spec?.catalogItem;
  const project = cluster.metadata?.project;

  return (
    <Stack hasGutter className="pf-v6-u-min-width">
      <StackItem>
        <Title headingLevel="h2" size="lg">
          {t('Overview')}
        </Title>
      </StackItem>
      <StackItem>
        <DescriptionList
          isCompact
          aria-label={t('Cluster overview')}
          className="pf-v6-u-text-break-word"
        >
          <DescriptionListGroup>
            <DescriptionListTerm>{t('Status')}</DescriptionListTerm>
            <DescriptionListDescription>
              <ClusterStatusLabel state={cluster.status?.state} />
            </DescriptionListDescription>
          </DescriptionListGroup>
          <DescriptionListGroup>
            <DescriptionListTerm>{t('Catalog item')}</DescriptionListTerm>
            <DescriptionListDescription>
              {catalogItem?.id?.trim() ? (
                <ResourceLink
                  basePath="/catalog/cluster"
                  id={catalogItem.id}
                  name={displayValue(catalogItem.name)}
                />
              ) : (
                displayValue(catalogItem?.name)
              )}
            </DescriptionListDescription>
          </DescriptionListGroup>
          <DescriptionListGroup>
            <DescriptionListTerm>{t('Project')}</DescriptionListTerm>
            <DescriptionListDescription>
              {project === '' ? t('Root') : displayValue(project)}
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
