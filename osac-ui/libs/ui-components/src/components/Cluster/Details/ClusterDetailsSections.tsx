import { Divider, PageSection, Stack, StackItem, Title } from '@patternfly/react-core';
import CheckCircleIcon from '@patternfly/react-icons/dist/esm/icons/check-circle-icon';
import NetworkWiredIcon from '@patternfly/react-icons/dist/esm/icons/network-wired-icon';

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
      <Stack hasGutter>
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
          <Divider />
        </StackItem>
        <StackItem>
          <div className="cluster-details__networking-region">
            <Stack hasGutter>
              <StackItem>
                <Title headingLevel="h2" size="lg" className="cluster-details__section-title">
                  <NetworkWiredIcon aria-hidden />
                  {t('Networking')}
                </Title>
              </StackItem>
              <StackItem>
                <ClusterNetworkingCard cluster={cluster} />
              </StackItem>
            </Stack>
            <ClusterNetworkingSection cluster={cluster} />
          </div>
        </StackItem>
        <StackItem>
          <Divider />
        </StackItem>
        <StackItem>
          <Stack hasGutter>
            <StackItem>
              <Title headingLevel="h2" size="lg" className="cluster-details__section-title">
                <CheckCircleIcon aria-hidden />
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
