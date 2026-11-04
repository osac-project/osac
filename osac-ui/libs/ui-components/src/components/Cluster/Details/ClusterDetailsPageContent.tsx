import { type SyntheticEvent, useEffect, useState } from 'react';
import {
  Card,
  CardBody,
  CardTitle,
  Divider,
  Flex,
  FlexItem,
  Grid,
  GridItem,
  PageSection,
  Stack,
  StackItem,
  Tab,
  TabContent,
  TabContentBody,
  TabTitleText,
  Tabs,
} from '@patternfly/react-core';

import { ExternalIPAttachmentEndpoint } from '@osac/types';
import type { Cluster, ClusterVersion, ExternalIPAttachment } from '@osac/types';

import AttachExternalIpModal from './AttachExternalIpModal';
import ClusterDetailsActionButtons from './ClusterDetailsActionButtons';
import ClusterDetailsSummary from './ClusterDetailsSummary';
import ClusterExternalIpCard from './ClusterExternalIpCard';
import { ClusterNetworkingDetailsTab } from './ClusterNetworkingDetailsTab';
import ClusterNodeSetsTab from './ClusterNetworkingTab';
import { ClusterOverviewTab } from './ClusterOverviewTab';
import ClusterVersionRail from './ClusterVersionRail';
import type { UpgradeActionState } from './ClusterVersionRail';
import DetachExternalIpModal from './DetachExternalIpModal';
import UpgradeClusterDialog from './UpgradeClusterDialog';
import { useTranslation } from '../../../hooks/useTranslation';
import { ResourceConditionsTable } from '../../Resource/ResourceConditionsTable';
import { ResourceDetailHeader } from '../../Resource/ResourceDetailHeader';
import { ClusterStatusLabel } from '../ClusterStatusLabel';

interface ClusterDetailViewProps {
  cluster: Cluster;
  upgrade?: {
    action: UpgradeActionState;
    eligibleVersions: ClusterVersion[];
  };
}

const CLUSTER_DETAIL_OVERVIEW_TAB_ID = 'cluster-detail-overview';
const CLUSTER_DETAIL_NETWORKING_TAB_ID = 'cluster-detail-networking';
const CLUSTER_DETAIL_NODE_SETS_TAB_ID = 'cluster-detail-node-sets';

const countCurrentWorkerNodes = (cluster: Cluster): number => {
  const statusNodeSets = cluster.status?.nodeSets ?? {};
  const specNodeSets = cluster.spec?.nodeSets ?? {};
  const currentNodeSets = Object.keys(statusNodeSets).length ? statusNodeSets : specNodeSets;
  return Object.entries(currentNodeSets).reduce(
    (total, [name, nodeSet]) => total + (nodeSet.size ?? specNodeSets[name]?.size ?? 0),
    0,
  );
};

const ClusterDetailsPageContent = ({ cluster, upgrade }: ClusterDetailViewProps) => {
  const { t } = useTranslation();
  const [activeTabKey, setActiveTabKey] = useState(0);
  const [upgradeDialogClusterId, setUpgradeDialogClusterId] = useState<string>();
  useEffect(() => {
    setUpgradeDialogClusterId(undefined);
  }, [cluster.id]);
  const upgradeAction: UpgradeActionState = upgrade?.action ?? {
    kind: 'blocked',
    reason: t('Upgrade eligibility is unavailable.'),
  };
  const [attachEndpoint, setAttachEndpoint] = useState<ExternalIPAttachmentEndpoint | undefined>();
  const [detachAttachment, setDetachAttachment] = useState<ExternalIPAttachment>();

  const handleTabSelect = (_event: SyntheticEvent, tabIndex: string | number) => {
    setActiveTabKey(Number(tabIndex));
    setAttachEndpoint(undefined);
    setDetachAttachment(undefined);
  };

  return (
    <>
      <PageSection hasBodyWrapper={false}>
        <Stack hasGutter>
          <StackItem>
            <Flex
              justifyContent={{ default: 'justifyContentSpaceBetween' }}
              alignItems={{ default: 'alignItemsFlexStart' }}
              flexWrap={{ default: 'wrap' }}
              spaceItems={{ default: 'spaceItemsMd' }}
            >
              <FlexItem>
                <ResourceDetailHeader
                  parents={[
                    {
                      to: '/clusters',
                      label: t('Clusters'),
                    },
                  ]}
                  resourceName={cluster.metadata?.name ?? cluster.id}
                  titleAddon={<ClusterStatusLabel state={cluster.status?.state} />}
                />
              </FlexItem>
              <FlexItem>
                <ClusterDetailsActionButtons cluster={cluster} />
              </FlexItem>
            </Flex>
          </StackItem>
          <StackItem>
            <ClusterDetailsSummary cluster={cluster} />
          </StackItem>
          <StackItem>
            <Divider />
          </StackItem>
          <StackItem>
            <Tabs activeKey={activeTabKey} onSelect={handleTabSelect} id="cluster-detail-tabs">
              <Tab
                eventKey={0}
                title={<TabTitleText>{t('Overview')}</TabTitleText>}
                tabContentId={CLUSTER_DETAIL_OVERVIEW_TAB_ID}
              />
              <Tab
                eventKey={1}
                title={<TabTitleText>{t('Networking')}</TabTitleText>}
                tabContentId={CLUSTER_DETAIL_NETWORKING_TAB_ID}
              />
              <Tab
                eventKey={2}
                title={<TabTitleText>{t('Node sets')}</TabTitleText>}
                tabContentId={CLUSTER_DETAIL_NODE_SETS_TAB_ID}
              />
            </Tabs>
          </StackItem>
        </Stack>
      </PageSection>

      <PageSection hasBodyWrapper={false}>
        <Grid hasGutter>
          <GridItem md={6}>
            <TabContent
              eventKey={0}
              id={CLUSTER_DETAIL_OVERVIEW_TAB_ID}
              activeKey={activeTabKey}
              hidden={0 !== activeTabKey}
            >
              <TabContentBody>
                <ClusterOverviewTab cluster={cluster} />
              </TabContentBody>
            </TabContent>
            <TabContent
              eventKey={1}
              id={CLUSTER_DETAIL_NETWORKING_TAB_ID}
              activeKey={activeTabKey}
              hidden={1 !== activeTabKey}
            >
              <TabContentBody>
                <ClusterNetworkingDetailsTab cluster={cluster} />
              </TabContentBody>
            </TabContent>
            <TabContent
              eventKey={2}
              id={CLUSTER_DETAIL_NODE_SETS_TAB_ID}
              activeKey={activeTabKey}
              hidden={2 !== activeTabKey}
            >
              <TabContentBody>
                <ClusterNodeSetsTab cluster={cluster} />
              </TabContentBody>
            </TabContent>
          </GridItem>

          <GridItem md={6}>
            <Stack hasGutter>
              {activeTabKey === 0 && (
                <StackItem>
                  <ClusterVersionRail
                    currentVersion={cluster.status?.observedVersion}
                    action={upgradeAction}
                    onUpgrade={() => setUpgradeDialogClusterId(cluster.id)}
                  />
                </StackItem>
              )}
              {activeTabKey === 0 && (
                <StackItem>
                  <Card isFullHeight>
                    <CardTitle>{t('Conditions')}</CardTitle>
                    <CardBody>
                      <ResourceConditionsTable
                        ariaLabel={t('Cluster conditions')}
                        conditions={cluster.status?.conditions ?? []}
                        conditionResourceKind="cluster"
                      />
                    </CardBody>
                  </Card>
                </StackItem>
              )}
              {activeTabKey === 1 && (
                <StackItem>
                  <ClusterExternalIpCard
                    cluster={cluster}
                    onAttach={setAttachEndpoint}
                    onDetach={setDetachAttachment}
                  />
                </StackItem>
              )}
            </Stack>
          </GridItem>
        </Grid>
      </PageSection>

      {upgradeDialogClusterId === cluster.id && upgrade && (
        <UpgradeClusterDialog
          clusterId={cluster.id}
          currentVersion={cluster.status?.observedVersion}
          eligibleVersions={upgrade.eligibleVersions}
          workerNodeCount={countCurrentWorkerNodes(cluster)}
          onClose={() => setUpgradeDialogClusterId(undefined)}
        />
      )}
      {attachEndpoint !== undefined && (
        <AttachExternalIpModal
          cluster={cluster}
          endpoint={attachEndpoint}
          onClose={() => setAttachEndpoint(undefined)}
        />
      )}
      {detachAttachment && (
        <DetachExternalIpModal
          attachment={detachAttachment}
          onClose={() => setDetachAttachment(undefined)}
        />
      )}
    </>
  );
};

export default ClusterDetailsPageContent;
