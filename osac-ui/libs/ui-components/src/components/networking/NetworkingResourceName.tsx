import { Link } from 'react-router-dom';
import { Flex, FlexItem } from '@patternfly/react-core';

import type { LabeledResource } from './resourceOrigin';
import ResourceOriginBadge from './ResourceOriginBadge';

interface NetworkingResourceNameProps {
  resource: LabeledResource & { id: string; metadata?: { name?: string } };
  /** Overrides the name (metadata.name, else the ID). */
  title?: string;
  /** When set, the name links to the resource's details page. */
  detailsUrl?: string;
}

/**
 * A networking resource name with its Default or Auto badge. Every networking link uses
 * the same plain link style, whether it is a name or a reference to another resource.
 */
const NetworkingResourceName = ({ resource, title, detailsUrl }: NetworkingResourceNameProps) => {
  const name = title ?? (resource.metadata?.name || resource.id);
  return (
    <Flex
      spaceItems={{ default: 'spaceItemsSm' }}
      alignItems={{ default: 'alignItemsCenter' }}
      flexWrap={{ default: 'nowrap' }}
    >
      <FlexItem>{detailsUrl ? <Link to={detailsUrl}>{name}</Link> : name}</FlexItem>
      <FlexItem>
        <ResourceOriginBadge resource={resource} />
      </FlexItem>
    </Flex>
  );
};

export default NetworkingResourceName;
