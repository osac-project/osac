import { useState } from 'react';
import { Dropdown, DropdownItem, DropdownList, MenuToggle } from '@patternfly/react-core';
import { EllipsisVIcon } from '@patternfly/react-icons/dist/esm/icons/ellipsis-v-icon';

import type { ExternalIP } from '@osac/types';

import ExternalIpDeleteModal from './ExternalIpDeleteModal';
import { deleteDisabledReason } from './utils';
import { useTranslation } from '../../hooks/useTranslation';

interface ExternalIpActionsMenuProps {
  externalIp: ExternalIP;
}

const ExternalIpActionsMenu = ({ externalIp }: ExternalIpActionsMenuProps) => {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const [deleteOpen, setDeleteOpen] = useState(false);
  const deleteDisabledReasonText = deleteDisabledReason(externalIp, t);
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
            isDanger={canDelete}
            isDisabled={!canDelete}
            description={deleteDisabledReasonText}
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
