import { useState } from 'react';
import {
  Button,
  Card,
  CardBody,
  DescriptionList,
  DescriptionListDescription,
  DescriptionListGroup,
  DescriptionListTerm,
  Divider,
  Flex,
  FlexItem,
  Stack,
  StackItem,
  Title,
} from '@patternfly/react-core';

import type { Cluster } from '@osac/types';

import ClusterNodeSetsSection from './ClusterNodeSetsSection';
import ClusterSettingsSection from './ClusterSettingsSection';
import ClusterUpgradeHistoryModal from './ClusterUpgradeHistoryModal';
import ClusterVersionControls from './ClusterVersionControls';
import { useTranslation } from '../../../hooks/useTranslation';
import { getControlPlaneUpgradeHistory } from '../ListPage/clusterDisplay';

interface ClusterConfigurationCardProps {
  cluster: Cluster;
}

export const ClusterConfigurationCard = ({ cluster }: ClusterConfigurationCardProps) => {
  const { t } = useTranslation();
  const [clusterHistoryOpen, setClusterHistoryOpen] = useState(false);
  const upgradeHistory = getControlPlaneUpgradeHistory(cluster);

  return (
    <Card className="cluster-details__secondary-card" aria-label={t('Cluster version')}>
      <CardBody>
        <Stack hasGutter>
          <StackItem>
            <Title headingLevel="h2" size="lg">
              {t('Cluster version')}
            </Title>
          </StackItem>
          <StackItem>
            <DescriptionList isCompact aria-label={t('Cluster version')}>
              <DescriptionListGroup>
                <DescriptionListTerm>
                  <Flex spaceItems={{ default: 'spaceItemsXs' }}>
                    <FlexItem>{t('Current version')}</FlexItem>
                    {upgradeHistory.length > 0 && (
                      <FlexItem>
                        <Button variant="link" isInline onClick={() => setClusterHistoryOpen(true)}>
                          {t('Upgrade history')}
                        </Button>
                      </FlexItem>
                    )}
                  </Flex>
                </DescriptionListTerm>
                <DescriptionListDescription>
                  <ClusterVersionControls cluster={cluster} />
                </DescriptionListDescription>
              </DescriptionListGroup>
            </DescriptionList>
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
            <ClusterSettingsSection cluster={cluster} />
          </StackItem>
        </Stack>
        {clusterHistoryOpen && upgradeHistory.length > 0 && (
          <ClusterUpgradeHistoryModal
            attempts={upgradeHistory}
            onClose={() => setClusterHistoryOpen(false)}
          />
        )}
      </CardBody>
    </Card>
  );
};
