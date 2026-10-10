import { useState } from 'react';
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

import type { Cluster } from '@osac/types';

import ClusterDetailsActionButtons from './ClusterDetailsActionButtons';
import ClusterDetailsSummary from './ClusterDetailsSummary';
import { ClusterNetworkingDetailsTab } from './ClusterNetworkingDetailsTab';
import ClusterNodeSetsTab from './ClusterNetworkingTab';
import { ClusterOverviewTab } from './ClusterOverviewTab';
import { useTranslation } from '../../../hooks/useTranslation';
import { ResourceConditionsTable } from '../../Resource/ResourceConditionsTable';
import { ResourceDetailHeader } from '../../Resource/ResourceDetailHeader';
import { ClusterStatusLabel } from '../ClusterStatusLabel';

interface ClusterDetailViewProps {
  cluster: Cluster;
}

const CLUSTER_DETAIL_OVERVIEW_TAB_ID = 'cluster-detail-overview';
const CLUSTER_DETAIL_NETWORKING_TAB_ID = 'cluster-detail-networking';
const CLUSTER_DETAIL_NODE_SETS_TAB_ID = 'cluster-detail-node-sets';

const ClusterDetailsPageContent = ({ cluster }: ClusterDetailViewProps) => {
  const { t } = useTranslation();
  const [activeTabKey, setActiveTabKey] = useState(0);

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
                  parentTo="/clusters"
                  parentLabel={t('Clusters')}
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
            <Tabs
              activeKey={activeTabKey}
              onSelect={(_event, tabIndex) => setActiveTabKey(Number(tabIndex))}
              id="cluster-detail-tabs"
            >
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
          </GridItem>
        </Grid>
      </PageSection>
    </>
  );
};

export default ClusterDetailsPageContent;
