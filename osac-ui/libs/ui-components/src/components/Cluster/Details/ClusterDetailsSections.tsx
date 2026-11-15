import { Divider, PageSection, Stack, StackItem, Title } from '@patternfly/react-core';

import type { Cluster } from '@osac/types';

import { ClusterConfigurationCard } from './ClusterConfigurationCard';
import ClusterNetworkingSection from './ClusterNetworkingSection';
import ClusterNodeSetsSection from './ClusterNodeSetsSection';
import { useTranslation } from '../../../hooks/useTranslation';
import { ResourceConditionsTable } from '../../Resource/ResourceConditionsTable';

interface ClusterDetailsSectionsProps {
  cluster: Cluster;
}

const ClusterDetailsSections = ({ cluster }: ClusterDetailsSectionsProps) => {
  const { t } = useTranslation();

  return (
    <PageSection hasBodyWrapper={false}>
      <Stack hasGutter>
        <StackItem>
          <ClusterConfigurationCard cluster={cluster} />
        </StackItem>
        <StackItem>
          <Divider />
        </StackItem>
        <StackItem>
          <ClusterNetworkingSection cluster={cluster} />
        </StackItem>
        <StackItem>
          <Divider />
        </StackItem>
        <StackItem>
          <ClusterNodeSetsSection cluster={cluster} />
        </StackItem>
        <StackItem>
          <Divider />
        </StackItem>
        <StackItem>
          <Stack hasGutter>
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
