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
} from '@patternfly/react-core';

import type { Cluster, ClusterVersion } from '@osac/types';

import { cel } from '../../../api/cel';
import {
  CLUSTER_VERSION_ALL_STATES_LIST_FILTER,
  useClusterVersions,
} from '../../../api/v1/cluster-versions';
import { useTranslation } from '../../../hooks/useTranslation';
import { displayValue } from '../../../utils/detailFormatters';
import { Timestamp } from '../../Primitives/Timestamp';
import ClusterVersionLifecycleLabel from '../ClusterVersionLifecycleLabel';

interface ClusterConfigurationCardProps {
  cluster: Cluster;
}

export const ClusterConfigurationCard = ({ cluster }: ClusterConfigurationCardProps) => {
  const { t } = useTranslation();
  const catalogItem = cluster.spec?.catalogItem;
  const currentVersion = cluster.status?.observedVersion.trim() ?? '';
  const { data: clusterVersions = [] } = useClusterVersions(
    {
      filter: cel<ClusterVersion>((filter) =>
        filter.and(
          filter.field('spec.version').equals(currentVersion),
          CLUSTER_VERSION_ALL_STATES_LIST_FILTER,
        ),
      ),
    },
    { enabled: Boolean(currentVersion) },
  );
  const matchingClusterVersion = clusterVersions.find(
    (version) => version.spec?.version === currentVersion,
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
            <DescriptionListTerm>{t('Current version')}</DescriptionListTerm>
            <DescriptionListDescription>
              <Flex
                spaceItems={{ default: 'spaceItemsSm' }}
                alignItems={{ default: 'alignItemsCenter' }}
              >
                <FlexItem>{displayValue(currentVersion)}</FlexItem>
                {matchingClusterVersion && (
                  <FlexItem>
                    <ClusterVersionLifecycleLabel clusterVersion={matchingClusterVersion} />
                  </FlexItem>
                )}
              </Flex>
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
