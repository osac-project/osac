import { useState } from 'react';
import {
  Button,
  Card,
  CardBody,
  CardHeader,
  CardTitle,
  DescriptionList,
  DescriptionListDescription,
  DescriptionListGroup,
  DescriptionListTerm,
  Divider,
  Stack,
  StackItem,
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
    <Card variant="secondary" aria-label={t('Cluster version')}>
      <CardHeader
        hasWrap
        actions={{
          actions: (
            <Button variant="link" isInline onClick={() => setClusterHistoryOpen(true)}>
              {t('Upgrade history')}
            </Button>
          ),
          hasNoOffset: true,
          className: 'pf-v6-u-ml-auto',
        }}
      >
        <CardTitle component="h2" className="pf-v6-u-min-width pf-v6-u-text-break-word">
          {t('Cluster version')}
        </CardTitle>
      </CardHeader>
      <Divider />
      <CardBody>
        <Stack hasGutter>
          <StackItem>
            <DescriptionList isCompact aria-label={t('Cluster version')}>
              <DescriptionListGroup>
                <DescriptionListTerm>{t('Current')}</DescriptionListTerm>
                <DescriptionListDescription>
                  <ClusterVersionControls cluster={cluster} />
                </DescriptionListDescription>
              </DescriptionListGroup>
            </DescriptionList>
          </StackItem>
          <StackItem>
            <ClusterNodeSetsSection cluster={cluster} />
          </StackItem>
          <StackItem>
            <ClusterSettingsSection cluster={cluster} />
          </StackItem>
        </Stack>
        {clusterHistoryOpen && (
          <ClusterUpgradeHistoryModal
            attempts={upgradeHistory}
            onClose={() => setClusterHistoryOpen(false)}
          />
        )}
      </CardBody>
    </Card>
  );
};
