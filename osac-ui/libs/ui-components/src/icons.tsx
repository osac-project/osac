import { Icon } from '@patternfly/react-core';
import { RhUiClusterIcon } from '@patternfly/react-icons/dist/esm/icons/rh-ui-cluster-icon';
import { RhUiServerStackIcon } from '@patternfly/react-icons/dist/esm/icons/rh-ui-server-stack-icon';
import { RhUiVirtualServerIcon } from '@patternfly/react-icons/dist/esm/icons/rh-ui-virtual-server-icon';

import './icons.css';

interface CatalogItemIconProps {
  kind?: 'vm' | 'bm' | 'cluster';
  isActive?: boolean;
}

export const CatalogItemIcon = ({ kind, isActive }: CatalogItemIconProps) => {
  let ItemIcon;
  switch (kind) {
    case 'cluster':
      ItemIcon = <RhUiClusterIcon />;
      break;
    case 'bm':
      ItemIcon = <RhUiServerStackIcon />;
      break;
    case 'vm':
      ItemIcon = <RhUiVirtualServerIcon />;
      break;
    default:
      ItemIcon = null;
  }

  return (
    <span className={`catalog-item-icon ${isActive ? 'm-is-active' : ''}`} aria-hidden>
      <Icon size="xl">{ItemIcon}</Icon>
    </span>
  );
};
