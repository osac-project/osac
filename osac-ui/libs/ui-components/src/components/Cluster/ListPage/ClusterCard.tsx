import { Button, Flex, FlexItem } from '@patternfly/react-core';

import { type Cluster, ClusterState, type ClusterVersion } from '@osac/types';
import InstanceCard from '@osac/ui-components/components/InstanceCard/InstanceCard';
import ResourceNameField from '@osac/ui-components/components/Resource/ResourceNameField';
import { useTranslation } from '@osac/ui-components/hooks/useTranslation';

import ClusterActionsMenu from '../ClusterActionsMenu';
import { formatClusterCreatedAt } from './clusterDisplay';
import ClusterResources from './ClusterResources';
import { ClusterStatusLabel } from './ClusterStatusLabel';

interface ClusterCardProps {
  cluster: Cluster;
  clusterVersion?: ClusterVersion;
}

const ClusterCard = ({ cluster, clusterVersion }: ClusterCardProps) => {
  const { t } = useTranslation();

  const locked = cluster.status?.state === ClusterState.DELETING;
  const detailsUrl = locked ? undefined : `/clusters/${encodeURIComponent(cluster.id)}`;
  const consoleUrl = cluster.status?.consoleUrl?.trim();
  const canOpenConsole = cluster.status?.state === ClusterState.READY && consoleUrl;
  const createdAt = formatClusterCreatedAt(cluster.metadata);

  return (
    <InstanceCard
      kind="cluster"
      statusLabel={<ClusterStatusLabel state={cluster.status?.state} />}
      actionMenu={locked ? null : <ClusterActionsMenu cluster={cluster} />}
      title={
        <ResourceNameField
          resource={cluster}
          detailsUrl={detailsUrl}
          subTitle={cluster.spec?.catalogItem?.name}
        />
      }
      body={<ClusterResources cluster={cluster} clusterVersion={clusterVersion} />}
      footer={
        <Flex direction={{ default: 'column' }} gap={{ default: 'gapSm' }}>
          <FlexItem>
            <Flex
              flexWrap={{ default: 'nowrap' }}
              gap={{ default: 'gapLg' }}
              justifyContent={{ default: 'justifyContentSpaceBetween' }}
            >
              <FlexItem>{t('Project')}</FlexItem>
              <FlexItem>{cluster.metadata?.project || t('Default')}</FlexItem>
            </Flex>
          </FlexItem>
          <FlexItem>
            <Flex
              flexWrap={{ default: 'nowrap' }}
              gap={{ default: 'gapLg' }}
              justifyContent={{ default: 'justifyContentSpaceBetween' }}
            >
              <FlexItem>{t('Created')}</FlexItem>
              <FlexItem>{createdAt}</FlexItem>
            </Flex>
          </FlexItem>
        </Flex>
      }
      actionButton={
        <Button
          variant="primary"
          isBlock
          component="a"
          href={canOpenConsole ? consoleUrl : undefined}
          target="_blank"
          rel="noopener noreferrer"
          isDisabled={!canOpenConsole}
        >
          {t('Console')}
        </Button>
      }
    />
  );
};

export default ClusterCard;
