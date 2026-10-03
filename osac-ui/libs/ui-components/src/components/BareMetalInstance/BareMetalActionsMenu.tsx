import { useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { Dropdown, DropdownItem, DropdownList, MenuToggle } from '@patternfly/react-core';
import { EllipsisVIcon } from '@patternfly/react-icons/dist/esm/icons/ellipsis-v-icon';

import { BareMetalInstance, BareMetalInstanceState } from '@osac/types';
import { getBareMetalSshHost } from '@osac/ui-components/components/BareMetalInstance/bareMetalInstanceDisplay';
import { useSession } from '@osac/ui-components/hooks/use-session';

import BareMetalConnectSshModal from './BareMetalConnectSshModal';
import BareMetalDeleteConfirmModal from './BareMetalDeleteConfirmModal';
import { useBareMetalActions } from './useBareMetalActions';
import { useTranslation } from '../../hooks/useTranslation';

interface BareMetalActionsMenuProps {
  instance: BareMetalInstance;
  onDeleted?: () => void;
}

export const BareMetalActionsMenu = ({ instance, onDeleted }: BareMetalActionsMenuProps) => {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const [deleteOpen, setDeleteOpen] = useState(false);
  const [connectSshOpen, setConnectSshOpen] = useState(false);
  const navigate = useNavigate();
  const { username } = useSession();
  const sshHost = getBareMetalSshHost(instance);
  const canConnectSsh =
    instance.status?.state === BareMetalInstanceState.RUNNING && Boolean(sshHost);

  const { canStart, canStop, canRestart, canDelete, start, stop, restart } =
    useBareMetalActions(instance);

  return (
    <>
      {connectSshOpen && sshHost && (
        <BareMetalConnectSshModal
          host={sshHost}
          username={username}
          onClose={() => setConnectSshOpen(false)}
        />
      )}
      {deleteOpen && (
        <BareMetalDeleteConfirmModal
          instance={instance}
          onClose={() => setDeleteOpen(false)}
          onSuccess={() => {
            setDeleteOpen(false);
            onDeleted?.();
          }}
        />
      )}
      <Dropdown
        isOpen={open}
        onOpenChange={setOpen}
        toggle={(ref) => (
          <MenuToggle
            ref={ref}
            variant="plain"
            onClick={() => setOpen((o) => !o)}
            aria-label={t('Actions for {{name}}', { name: instance.metadata?.name ?? instance.id })}
          >
            <EllipsisVIcon />
          </MenuToggle>
        )}
        popperProps={{ position: 'right' }}
      >
        <DropdownList>
          <DropdownItem
            component="a"
            onClick={() => {
              setOpen(false);
              navigate(`/bare-metal/${instance.id}`);
            }}
          >
            {t('View details')}
          </DropdownItem>
          <DropdownItem
            isDisabled={!canConnectSsh}
            onClick={() => {
              setConnectSshOpen(true);
              setOpen(false);
            }}
          >
            {t('Connect via SSH')}
          </DropdownItem>
          <DropdownItem
            isDisabled={!canStart}
            onClick={() => {
              start();
              setOpen(false);
            }}
          >
            {t('Start')}
          </DropdownItem>
          <DropdownItem
            isDisabled={!canStop}
            onClick={() => {
              stop();
              setOpen(false);
            }}
          >
            {t('Stop')}
          </DropdownItem>
          <DropdownItem
            isDisabled={!canRestart}
            onClick={() => {
              restart();
              setOpen(false);
            }}
          >
            {t('Restart')}
          </DropdownItem>
          <DropdownItem
            isDisabled={!canDelete}
            isDanger
            onClick={() => {
              if (canDelete) {
                setDeleteOpen(true);
                setOpen(false);
              }
            }}
          >
            {t('Delete')}
          </DropdownItem>
        </DropdownList>
      </Dropdown>
    </>
  );
};
