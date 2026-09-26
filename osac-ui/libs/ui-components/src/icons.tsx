import { Icon } from '@patternfly/react-core';
import { RhUiClusterIcon } from '@patternfly/react-icons/dist/esm/icons/rh-ui-cluster-icon';
import { RhUiServerStackIcon } from '@patternfly/react-icons/dist/esm/icons/rh-ui-server-stack-icon';
import { RhUiVirtualServerIcon } from '@patternfly/react-icons/dist/esm/icons/rh-ui-virtual-server-icon';

import './icons.css';

interface CatalogItemIconProps {
  kind:
    | 'osac.public.v1.ClusterCatalogItem'
    | 'osac.public.v1.BareMetalInstanceCatalogItem'
    | 'osac.public.v1.ComputeInstanceCatalogItem';
  isActive?: boolean;
}

export const CatalogItemIcon = ({ kind }: CatalogItemIconProps) => {
  let ItemIcon;
  switch (kind) {
    case 'osac.public.v1.ClusterCatalogItem':
      ItemIcon = RhUiClusterIcon;
      break;
    case 'osac.public.v1.BareMetalInstanceCatalogItem':
      ItemIcon = RhUiServerStackIcon;
      break;
    default:
      ItemIcon = RhUiVirtualServerIcon;
  }
  return (
    <span className="catalog-item-icon" aria-hidden>
      <Icon size="xl">
        <ItemIcon />
      </Icon>
    </span>
  );
};
