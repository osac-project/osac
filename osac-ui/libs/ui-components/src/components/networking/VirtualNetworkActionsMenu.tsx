import { useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { Dropdown, DropdownItem, DropdownList, MenuToggle } from '@patternfly/react-core';
import { EllipsisVIcon } from '@patternfly/react-icons/dist/esm/icons/ellipsis-v-icon';

import type { VirtualNetwork } from '@osac/types';
import { getVirtualNetworkName } from '@osac/ui-components/components/networking/virtual-network-list-utils';

import { useTranslation } from '../../hooks/useTranslation';

interface VirtualNetworkActionsMenuProps {
  virtualNetwork: VirtualNetwork;
}

const VirtualNetworkActionsMenu = ({ virtualNetwork }: VirtualNetworkActionsMenuProps) => {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const [open, setOpen] = useState(false);

  return (
    <Dropdown
      isOpen={open}
      onOpenChange={setOpen}
      toggle={(ref) => (
        <MenuToggle
          ref={ref}
          variant="plain"
          onClick={() => setOpen((o) => !o)}
          aria-label={t('Actions for {{name}}', {
            name: getVirtualNetworkName(virtualNetwork),
          })}
        >
          <EllipsisVIcon />
        </MenuToggle>
      )}
      popperProps={{ position: 'right' }}
    >
      <DropdownList>
        <DropdownItem
          value="view-details"
          onClick={() => navigate(`/networking/virtual-networks/${virtualNetwork.id}`)}
        >
          {t('View details')}
        </DropdownItem>
      </DropdownList>
    </Dropdown>
  );
};

export default VirtualNetworkActionsMenu;
