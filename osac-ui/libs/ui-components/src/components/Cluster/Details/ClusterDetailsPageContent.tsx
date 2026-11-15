import { Divider, Flex, FlexItem, PageSection, Stack, StackItem } from '@patternfly/react-core';

import type { Cluster } from '@osac/types';

import ClusterDetailsActionButtons from './ClusterDetailsActionButtons';
import ClusterDetailsSections from './ClusterDetailsSections';
import { useTranslation } from '../../../hooks/useTranslation';
import { ResourceDetailHeader } from '../../Resource/ResourceDetailHeader';
import { ClusterStatusLabel } from '../ListPage/ClusterStatusLabel';

interface ClusterDetailViewProps {
  cluster: Cluster;
}

const ClusterDetailsPageContent = ({ cluster }: ClusterDetailViewProps) => {
  const { t } = useTranslation();

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
                  parents={[{ to: '/clusters', label: t('Clusters') }]}
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
            <Divider />
          </StackItem>
        </Stack>
      </PageSection>
      <ClusterDetailsSections key={cluster.id} cluster={cluster} />
    </>
  );
};

export default ClusterDetailsPageContent;
