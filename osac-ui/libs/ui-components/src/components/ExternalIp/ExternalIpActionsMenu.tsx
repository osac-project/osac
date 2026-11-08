import { useState } from 'react';
import { Dropdown, DropdownItem, DropdownList, MenuToggle } from '@patternfly/react-core';
import { EllipsisVIcon } from '@patternfly/react-icons/dist/esm/icons/ellipsis-v-icon';

import type { ExternalIP } from '@osac/types';
import type { ExternalIpAttachedTarget } from '@osac/ui-components/api/v1/external-ip-data';

import ExternalIpDeleteModal from './ExternalIpDeleteModal';
import { deleteDisabledReason } from './utils';
import { useTranslation } from '../../hooks/useTranslation';
import { protectedResourceReason } from '../networking/resourceOrigin';

interface ExternalIpActionsMenuProps {
  externalIp: ExternalIP;
  consumer?: ExternalIpAttachedTarget;
}

const ExternalIpActionsMenu = ({ externalIp, consumer }: ExternalIpActionsMenuProps) => {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const [deleteOpen, setDeleteOpen] = useState(false);
  // Default and automatic external IPs, and ones in use, cannot be deleted.
  const deleteDisabledReasonText =
    protectedResourceReason(t, externalIp) ?? deleteDisabledReason(externalIp, t, consumer);
  const canDelete = !deleteDisabledReasonText;

  return (
    <>
      {deleteOpen && (
        <ExternalIpDeleteModal
          externalIp={externalIp}
          onClose={() => setDeleteOpen(false)}
          onSuccess={() => setDeleteOpen(false)}
        />
      )}
      <Dropdown
        isOpen={open}
        onOpenChange={setOpen}
        onSelect={() => setOpen(false)}
        toggle={(ref) => (
          <MenuToggle
            ref={ref}
            variant="plain"
            onClick={() => setOpen((wasOpen) => !wasOpen)}
            isExpanded={open}
            aria-label={t('Actions for {{name}}', { name: externalIp.metadata?.name })}
          >
            <EllipsisVIcon />
          </MenuToggle>
        )}
        popperProps={{ position: 'right' }}
      >
        <DropdownList>
          <DropdownItem
            isAriaDisabled={!canDelete}
            tooltipProps={canDelete ? undefined : { content: deleteDisabledReasonText }}
            onClick={() => {
              if (!canDelete) {
                return;
              }
              setDeleteOpen(true);
              setOpen(false);
            }}
          >
            {t('Delete')}
          </DropdownItem>
        </DropdownList>
      </Dropdown>
    </>
  );
};

export default ExternalIpActionsMenu;
