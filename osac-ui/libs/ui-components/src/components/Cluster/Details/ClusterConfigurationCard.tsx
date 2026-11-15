import { useState } from 'react';
import {
  Button,
  DescriptionList,
  DescriptionListDescription,
  DescriptionListGroup,
  DescriptionListTerm,
  Flex,
  FlexItem,
  Grid,
  GridItem,
  Stack,
  StackItem,
  Title,
} from '@patternfly/react-core';

import type { Cluster } from '@osac/types';

import ClusterDetailsSummary from './ClusterDetailsSummary';
import ClusterUpgradeHistoryModal from './ClusterUpgradeHistoryModal';
import ClusterVersionControls from './ClusterVersionControls';
import { useTranslation } from '../../../hooks/useTranslation';
import { displayValue } from '../../../utils/detailFormatters';
import { Timestamp } from '../../Primitives/Timestamp';
import { getControlPlaneUpgradeHistory } from '../ListPage/clusterDisplay';

interface ClusterConfigurationCardProps {
  cluster: Cluster;
}

export const ClusterConfigurationCard = ({ cluster }: ClusterConfigurationCardProps) => {
  const { t } = useTranslation();
  const [clusterHistoryOpen, setClusterHistoryOpen] = useState(false);
  const upgradeHistory = getControlPlaneUpgradeHistory(cluster);

  return (
    <Grid hasGutter>
      <GridItem md={4}>
        <ClusterDetailsSummary cluster={cluster} />
      </GridItem>
      <GridItem md={4}>
        <Stack hasGutter>
          <StackItem>
            <Title headingLevel="h2" size="lg">
              {t('Lifecycle')}
            </Title>
          </StackItem>
          <StackItem>
            <DescriptionList isCompact aria-label={t('Cluster lifecycle')}>
              <DescriptionListGroup>
                <DescriptionListTerm>{t('Created')}</DescriptionListTerm>
                <DescriptionListDescription>
                  <Timestamp value={cluster.metadata?.creationTimestamp} />
                </DescriptionListDescription>
              </DescriptionListGroup>
              <DescriptionListGroup>
                <DescriptionListTerm>{t('Creator')}</DescriptionListTerm>
                <DescriptionListDescription>
                  {displayValue(cluster.metadata?.creator)}
                </DescriptionListDescription>
              </DescriptionListGroup>
            </DescriptionList>
          </StackItem>
        </Stack>
      </GridItem>
      <GridItem md={4}>
        <Stack hasGutter>
          <StackItem>
            <Title headingLevel="h2" size="lg">
              {t('Version')}
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
        </Stack>
        {clusterHistoryOpen && upgradeHistory.length > 0 && (
          <ClusterUpgradeHistoryModal
            attempts={upgradeHistory}
            onClose={() => setClusterHistoryOpen(false)}
          />
        )}
      </GridItem>
    </Grid>
  );
};
