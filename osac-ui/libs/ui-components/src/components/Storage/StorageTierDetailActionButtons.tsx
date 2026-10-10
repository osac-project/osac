import { useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { Button, Flex, FlexItem } from '@patternfly/react-core';

import type { StorageTier } from '@osac/types/private';
import DeleteResourceButton from '@osac/ui-components/components/Resource/DeleteResourceButton';

import StorageTierDeleteConfirmModal from './StorageTierDeleteConfirmModal';
import { useTranslation } from '../../hooks/useTranslation';

const TIERS_LIST_PATH = '/admin/infrastructure/storage/tiers';

interface StorageTierDetailActionButtonsProps {
  tier: StorageTier;
}

export const StorageTierDetailActionButtons = ({ tier }: StorageTierDetailActionButtonsProps) => {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const [deleteOpen, setDeleteOpen] = useState(false);

  return (
    <>
      {deleteOpen && (
        <StorageTierDeleteConfirmModal
          tier={tier}
          onClose={() => setDeleteOpen(false)}
          onSuccess={() => navigate(TIERS_LIST_PATH)}
        />
      )}
      <Flex spaceItems={{ default: 'spaceItemsSm' }} flexWrap={{ default: 'wrap' }}>
        <FlexItem>
          <Button
            variant="secondary"
            onClick={() => navigate(`${TIERS_LIST_PATH}/${tier.id}/edit`)}
          >
            {t('Edit')}
          </Button>
        </FlexItem>
        <FlexItem>
          <DeleteResourceButton onClick={() => setDeleteOpen(true)} />
        </FlexItem>
      </Flex>
    </>
  );
};
