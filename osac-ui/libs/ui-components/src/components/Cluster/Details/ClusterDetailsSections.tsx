import { Divider, PageSection, Stack, StackItem, Title } from '@patternfly/react-core';

import type { Cluster } from '@osac/types';

import { ClusterConfigurationCard } from './ClusterConfigurationCard';
import ClusterDetailsSummary from './ClusterDetailsSummary';
import ClusterLifecycleSection from './ClusterLifecycleSection';
import ClusterNetworkingCard from './ClusterNetworkingCard';
import ClusterNetworkingSection from './ClusterNetworkingSection';
import { useTranslation } from '../../../hooks/useTranslation';
import { ResourceConditionsTable } from '../../Resource/ResourceConditionsTable';

import './cluster-details.css';

interface ClusterDetailsSectionsProps {
  cluster: Cluster;
}

const ClusterDetailsSections = ({ cluster }: ClusterDetailsSectionsProps) => {
  const { t } = useTranslation();

  return (
    <PageSection hasBodyWrapper={false} className="cluster-details">
      <Stack hasGutter className="cluster-details__sections">
        <StackItem>
          <div className="cluster-details__top-region">
            <div className="cluster-details__overview-grid">
              <ClusterDetailsSummary cluster={cluster} />
              <ClusterLifecycleSection cluster={cluster} />
            </div>
            <ClusterConfigurationCard cluster={cluster} />
          </div>
        </StackItem>
        <StackItem>
          <Divider className="cluster-details__section-divider" />
        </StackItem>
        <StackItem>
          <div className="cluster-details__networking-region">
            <Stack hasGutter>
              <StackItem>
                <Title headingLevel="h2" size="lg">
                  {t('Networking')}
                </Title>
              </StackItem>
              <StackItem className="cluster-details__networking-table">
                <ClusterNetworkingCard cluster={cluster} />
              </StackItem>
            </Stack>
            <ClusterNetworkingSection cluster={cluster} />
          </div>
        </StackItem>
        <StackItem>
          <Divider className="cluster-details__section-divider" />
        </StackItem>
        <StackItem>
          <Stack hasGutter className="cluster-details__conditions">
            <StackItem>
              <Title headingLevel="h2" size="lg">
                {t('Conditions')}
              </Title>
            </StackItem>
            <StackItem>
              <ResourceConditionsTable
                ariaLabel={t('Cluster conditions')}
                conditions={cluster.status?.conditions ?? []}
                conditionResourceKind="cluster"
              />
            </StackItem>
          </Stack>
        </StackItem>
      </Stack>
    </PageSection>
  );
};

export default ClusterDetailsSections;
