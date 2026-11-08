import { useState } from 'react';
import { Dropdown, DropdownItem, DropdownList, MenuToggle } from '@patternfly/react-core';
import { EllipsisVIcon } from '@patternfly/react-icons/dist/esm/icons/ellipsis-v-icon';

import type { NATGateway } from '@osac/types';

import { useNatGatewayDeleteDisabledReason } from './deleteDisabledReasons';
import { DetachNatGatewayModal } from './DetachNatGatewayModal';
import { useTranslation } from '../../hooks/useTranslation';

interface NatGatewayActionsMenuProps {
  natGateway: NATGateway;
}

const NatGatewayActionsMenu = ({ natGateway }: NatGatewayActionsMenuProps) => {
  const { t } = useTranslation();
  const [isOpen, setIsOpen] = useState(false);
  const [isDeleteOpen, setIsDeleteOpen] = useState(false);
  // The default NAT gateway, and one that clusters on its network use, cannot be deleted.
  const deleteDisabledReason = useNatGatewayDeleteDisabledReason(natGateway);

  return (
    <>
      <Dropdown
        isOpen={isOpen}
        onOpenChange={setIsOpen}
        onSelect={() => setIsOpen(false)}
        toggle={(ref) => (
          <MenuToggle
            ref={ref}
            variant="plain"
            onClick={() => setIsOpen((wasOpen) => !wasOpen)}
            isExpanded={isOpen}
            aria-label={t('Actions for {{name}}', {
              name: natGateway.metadata?.name || natGateway.id,
            })}
          >
            <EllipsisVIcon />
          </MenuToggle>
        )}
        popperProps={{ position: 'right' }}
      >
        <DropdownList>
          <DropdownItem
            isAriaDisabled={Boolean(deleteDisabledReason)}
            tooltipProps={deleteDisabledReason ? { content: deleteDisabledReason } : undefined}
            onClick={() => {
              if (deleteDisabledReason) {
                return;
              }
              setIsDeleteOpen(true);
              setIsOpen(false);
            }}
          >
            {t('Delete')}
          </DropdownItem>
        </DropdownList>
      </Dropdown>
      {isDeleteOpen ? (
        <DetachNatGatewayModal natGateway={natGateway} onClose={() => setIsDeleteOpen(false)} />
      ) : null}
    </>
  );
};

export default NatGatewayActionsMenu;
