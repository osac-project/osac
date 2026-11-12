import { useState } from 'react';
import {
  Button,
  Card,
  CardBody,
  CardTitle,
  DescriptionList,
  DescriptionListDescription,
  DescriptionListGroup,
  DescriptionListTerm,
  Flex,
  FlexItem,
} from '@patternfly/react-core';

import type { Cluster } from '@osac/types';

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
  const catalogItem = cluster.spec?.catalogItem;
  const upgradeHistory = getControlPlaneUpgradeHistory(cluster);

  return (
    <Card isFullHeight>
      <CardTitle>{t('Cluster configuration')}</CardTitle>
      <CardBody>
        <DescriptionList isCompact>
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
          <DescriptionListGroup>
            <DescriptionListTerm>{t('Catalog item')}</DescriptionListTerm>
            <DescriptionListDescription>
              {displayValue(catalogItem?.name || catalogItem?.id)}
            </DescriptionListDescription>
          </DescriptionListGroup>
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
      </CardBody>
      {clusterHistoryOpen && upgradeHistory.length > 0 && (
        <ClusterUpgradeHistoryModal
          attempts={upgradeHistory}
          onClose={() => setClusterHistoryOpen(false)}
        />
      )}
    </Card>
  );
};
