import { Modal, ModalBody, ModalHeader } from '@patternfly/react-core';
import { Table, Tbody, Td, Th, Thead, Tr } from '@patternfly/react-table';

import { type ClusterUpgradeHistoryEntry, ClusterUpgradeState } from '@osac/types';

import { useTranslation } from '../../../hooks/useTranslation';
import { Timestamp } from '../../Primitives/Timestamp';

interface ClusterUpgradeHistoryModalProps {
  attempts: ClusterUpgradeHistoryEntry[];
  onClose: () => void;
}

const ClusterUpgradeHistoryModal = ({ attempts, onClose }: ClusterUpgradeHistoryModalProps) => {
  const { t } = useTranslation();

  const stateLabel = (state: ClusterUpgradeState) => {
    switch (state) {
      case ClusterUpgradeState.PENDING:
        return t('Pending');
      case ClusterUpgradeState.PROGRESSING:
        return t('In progress');
      case ClusterUpgradeState.SUCCEEDED:
        return t('Succeeded');
      case ClusterUpgradeState.FAILED:
        return t('Failed');
      case ClusterUpgradeState.TIMED_OUT:
        return t('Timed out');
      default:
        return t('Unknown');
    }
  };

  return (
    <Modal isOpen onClose={onClose} variant="large" aria-labelledby="cluster-upgrade-history-title">
      <ModalHeader title={t('Upgrade history')} labelId="cluster-upgrade-history-title" />
      <ModalBody>
        <Table aria-label={t('Upgrade history')} variant="compact">
          <Thead>
            <Tr>
              <Th>{t('Upgrade')}</Th>
              <Th>{t('Status')}</Th>
              <Th>{t('Accepted')}</Th>
              <Th>{t('Completed')}</Th>
              <Th>{t('Details')}</Th>
            </Tr>
          </Thead>
          <Tbody>
            {attempts.map((attempt, index) => {
              const completed =
                attempt.state === ClusterUpgradeState.SUCCEEDED ||
                attempt.state === ClusterUpgradeState.FAILED;
              return (
                <Tr key={index}>
                  <Td dataLabel={t('Upgrade')}>
                    {attempt.sourceVersion || '—'} → {attempt.targetVersion || '—'}
                  </Td>
                  <Td dataLabel={t('Status')}>{stateLabel(attempt.state)}</Td>
                  <Td dataLabel={t('Accepted')}>
                    <Timestamp value={attempt.acceptedTime} />
                  </Td>
                  <Td dataLabel={t('Completed')}>
                    <Timestamp value={completed ? attempt.stateTransitionTime : undefined} />
                  </Td>
                  <Td dataLabel={t('Details')}>{attempt.message || '—'}</Td>
                </Tr>
              );
            })}
          </Tbody>
        </Table>
      </ModalBody>
    </Modal>
  );
};

export default ClusterUpgradeHistoryModal;
