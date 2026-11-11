import { useState } from 'react';
import { Dropdown, DropdownItem, DropdownList, MenuToggle } from '@patternfly/react-core';
import { EllipsisVIcon } from '@patternfly/react-icons/dist/esm/icons/ellipsis-v-icon';

import { type ExternalIPAttachment, ExternalIPAttachmentState } from '@osac/types';

import ExternalIpDetachModal from './DetachExternalIpModal';
import { useTranslation } from '../../hooks/useTranslation';
import { protectedResourceReason } from '../networking/resourceOrigin';

interface ExternalIpAttachmentActionsMenuProps {
  attachment: ExternalIPAttachment;
}

const ExternalIpAttachmentActionsMenu = ({ attachment }: ExternalIpAttachmentActionsMenuProps) => {
  const { t } = useTranslation();
  const [isOpen, setIsOpen] = useState(false);
  const [isDeleteOpen, setIsDeleteOpen] = useState(false);
  // OSAC removes an automatic attachment with its workload.
  const deleteDisabledReason =
    protectedResourceReason(t, attachment) ??
    (attachment.status?.state === ExternalIPAttachmentState.EXTERNAL_IP_ATTACHMENT_STATE_DELETING
      ? t('This attachment is being deleted.')
      : undefined);

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
              name: attachment.metadata?.name || attachment.id,
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
        <ExternalIpDetachModal
          attachment={attachment}
          label={t(
            'This detaches the external IP from its target. The external IP stays allocated to your project.',
          )}
          onClose={() => setIsDeleteOpen(false)}
        />
      ) : null}
    </>
  );
};

export default ExternalIpAttachmentActionsMenu;
