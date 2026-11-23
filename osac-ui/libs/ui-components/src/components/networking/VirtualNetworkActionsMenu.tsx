import { useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { Dropdown, DropdownItem, DropdownList, MenuToggle, Tooltip } from '@patternfly/react-core';
import { EllipsisVIcon } from '@patternfly/react-icons/dist/esm/icons/ellipsis-v-icon';

import { type VirtualNetwork, VirtualNetworks } from '@osac/types';

import { protectedResourceReason } from './resourceOrigin';
import { getVirtualNetworkName } from './virtual-network-list-utils';
import { useDeleteResource } from '../../api/use-resource';
import { useTranslation } from '../../hooks/useTranslation';
import DeleteResourceModal from '../Resource/DeleteResourceModal';
import { useToast } from '../Toast/useToast';

interface VirtualNetworkActionsMenuProps {
  virtualNetwork: VirtualNetwork;
}

const VirtualNetworkActionsMenu = ({ virtualNetwork }: VirtualNetworkActionsMenuProps) => {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const { addToast } = useToast();
  const [open, setOpen] = useState(false);
  const [isDeleteOpen, setIsDeleteOpen] = useState(false);
  const remove = useDeleteResource(VirtualNetworks);

  const name = getVirtualNetworkName(virtualNetwork);
  const blockedReason = protectedResourceReason(t, virtualNetwork.metadata?.labels);

  return (
    <>
      <Dropdown
        isOpen={open}
        onOpenChange={setOpen}
        toggle={(ref) => (
          <MenuToggle
            ref={ref}
            variant="plain"
            onClick={() => setOpen((o) => !o)}
            aria-label={t('Actions for {{name}}', { name })}
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
          <Tooltip content={blockedReason} isVisible={Boolean(blockedReason)}>
            <DropdownItem
              value="delete"
              isDisabled={Boolean(blockedReason)}
              description={blockedReason}
              onClick={() => {
                setOpen(false);
                setIsDeleteOpen(true);
              }}
            >
              {t('Delete')}
            </DropdownItem>
          </Tooltip>
        </DropdownList>
      </Dropdown>

      {isDeleteOpen && (
        <DeleteResourceModal
          resourceName={name}
          label={t(
            'Remove its subnets, security groups and NAT gateway first. A network that still has dependents cannot be deleted.',
          )}
          errorLabel={t('Failed to delete virtual network')}
          mutation={remove}
          variables={{ id: virtualNetwork.id }}
          onClose={() => setIsDeleteOpen(false)}
          onSuccess={() => {
            setIsDeleteOpen(false);
            addToast({
              variant: 'success',
              title: t('Virtual network {{name}} deleted', { name }),
            });
          }}
        />
      )}
    </>
  );
};

export default VirtualNetworkActionsMenu;
