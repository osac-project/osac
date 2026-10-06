import {
  Card,
  CardBody,
  CardTitle,
  DescriptionList,
  DescriptionListDescription,
  DescriptionListGroup,
  DescriptionListTerm,
  Flex,
  FlexItem,
  Skeleton,
} from '@patternfly/react-core';

import type { Cluster } from '@osac/types';

import { useClusterVersion } from '../../../api/v1/cluster-versions';
import { displayValue } from '../../../utils/detailFormatters';
import { Timestamp } from '../../Primitives/Timestamp';
import ClusterVersionLifecycleLabel from '../ClusterVersionLifecycleLabel';

interface ClusterConfigurationCardProps {
  cluster: Cluster;
}

export const ClusterConfigurationCard = ({ cluster }: ClusterConfigurationCardProps) => {
  const catalogItem = cluster.spec?.catalogItem;
  const { data: clusterVersion, isLoading: isVersionLoading } = useClusterVersion(
    cluster.spec?.version?.id,
  );
  return (
    <Card isFullHeight>
      <CardTitle>Cluster configuration</CardTitle>
      <CardBody>
        <DescriptionList isCompact>
          <DescriptionListGroup>
            <DescriptionListTerm>Catalog item</DescriptionListTerm>
            <DescriptionListDescription>
              {displayValue(catalogItem?.name || catalogItem?.id)}
            </DescriptionListDescription>
          </DescriptionListGroup>
          <DescriptionListGroup>
            <DescriptionListTerm>Version</DescriptionListTerm>
            <DescriptionListDescription>
              {isVersionLoading ? (
                <Skeleton width="150px" />
              ) : clusterVersion ? (
                <Flex
                  spaceItems={{ default: 'spaceItemsSm' }}
                  alignItems={{ default: 'alignItemsCenter' }}
                >
                  <FlexItem>{displayValue(clusterVersion.spec?.version)}</FlexItem>
                  <FlexItem>
                    <ClusterVersionLifecycleLabel clusterVersion={clusterVersion} />
                  </FlexItem>
                </Flex>
              ) : (
                displayValue(cluster.spec?.version?.name)
              )}
            </DescriptionListDescription>
          </DescriptionListGroup>

          <DescriptionListGroup>
            <DescriptionListTerm>Created</DescriptionListTerm>
            <DescriptionListDescription>
              <Timestamp value={cluster.metadata?.creationTimestamp} />
            </DescriptionListDescription>
          </DescriptionListGroup>
          <DescriptionListGroup>
            <DescriptionListTerm>Creator</DescriptionListTerm>
            <DescriptionListDescription>
              {displayValue(cluster.metadata?.creator)}
            </DescriptionListDescription>
          </DescriptionListGroup>
        </DescriptionList>
      </CardBody>
    </Card>
  );
};
