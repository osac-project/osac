import {
  Card,
  CardBody,
  CardTitle,
  DescriptionList,
  DescriptionListDescription,
  DescriptionListGroup,
  DescriptionListTerm,
  Stack,
  StackItem,
} from '@patternfly/react-core';

import type { Cluster } from '@osac/types';

import ExternalLink from '../../Primitives/ExternalLink';
import { ClusterStatusLabel } from '../ListPage/ClusterStatusLabel';

interface ClusterStatusCardProps {
  cluster: Cluster;
}

export const ClusterAccessCard = ({ cluster }: ClusterStatusCardProps) => {
  const apiUrl = cluster.status?.apiUrl;
  const consoleUrl = cluster.status?.consoleUrl;

  return (
    <Card isFullHeight>
      <CardTitle>Access</CardTitle>
      <CardBody>
        <Stack hasGutter>
          <StackItem>
            <ClusterStatusLabel state={cluster.status?.state} />
          </StackItem>
          <StackItem>
            <DescriptionList isCompact>
              <DescriptionListGroup>
                <DescriptionListTerm>API URL</DescriptionListTerm>
                <DescriptionListDescription>
                  <ExternalLink href={apiUrl} showUnsafeAsText />
                </DescriptionListDescription>
              </DescriptionListGroup>
              <DescriptionListGroup>
                <DescriptionListTerm>Console URL</DescriptionListTerm>
                <DescriptionListDescription>
                  <ExternalLink href={consoleUrl} showUnsafeAsText />
                </DescriptionListDescription>
              </DescriptionListGroup>
            </DescriptionList>
          </StackItem>
        </Stack>
      </CardBody>
    </Card>
  );
};
