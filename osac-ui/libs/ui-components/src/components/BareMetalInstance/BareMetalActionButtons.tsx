import { useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { Button, Flex } from '@patternfly/react-core';
import PlayIcon from '@patternfly/react-icons/dist/esm/icons/play-icon';
import StopIcon from '@patternfly/react-icons/dist/esm/icons/stop-icon';
import SyncAltIcon from '@patternfly/react-icons/dist/esm/icons/sync-alt-icon';

import type { BareMetalInstance } from '@osac/types';
import DeleteResourceButton from '@osac/ui-components/components/Resource/DeleteResourceButton';

import BareMetalDeleteConfirmModal from './BareMetalDeleteConfirmModal';
import { useBareMetalActions } from './useBareMetalActions';
import { useTranslation } from '../../hooks/useTranslation';

interface BareMetalActionButtonsProps {
  instance: BareMetalInstance;
}

const BareMetalActionButtons = ({ instance }: BareMetalActionButtonsProps) => {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const [deleteOpen, setDeleteOpen] = useState(false);

  const { canStart, canStop, canRestart, canDelete, start, stop, restart } =
    useBareMetalActions(instance);

  return (
    <>
      {deleteOpen && (
        <BareMetalDeleteConfirmModal
          instance={instance}
          onClose={() => setDeleteOpen(false)}
          onSuccess={() => navigate('/bare-metal')}
        />
      )}
      <Flex
        justifyContent={{ default: 'justifyContentFlexEnd' }}
        spaceItems={{ default: 'spaceItemsSm' }}
        flexWrap={{ default: 'wrap' }}
      >
        <Button variant="primary" icon={<PlayIcon />} isDisabled={!canStart} onClick={start}>
          {t('Start')}
        </Button>
        <Button variant="secondary" icon={<StopIcon />} isDisabled={!canStop} onClick={stop}>
          {t('Stop')}
        </Button>
        <Button
          variant="secondary"
          icon={<SyncAltIcon />}
          isDisabled={!canRestart}
          onClick={restart}
        >
          {t('Restart')}
        </Button>
        <DeleteResourceButton
          showIcon
          canDelete={canDelete}
          onClick={() => {
            if (canDelete) {
              setDeleteOpen(true);
            }
          }}
        />
      </Flex>
    </>
  );
};

export default BareMetalActionButtons;
