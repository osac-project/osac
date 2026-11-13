import { Modal, ModalBody, ModalHeader } from '@patternfly/react-core';
import ClockIcon from '@patternfly/react-icons/dist/esm/icons/clock-icon';
import HourglassHalfIcon from '@patternfly/react-icons/dist/esm/icons/hourglass-half-icon';
import { Table, Tbody, Td, Th, Thead, Tr } from '@patternfly/react-table';

import { type ClusterUpgradeHistoryEntry, ClusterUpgradeState } from '@osac/types';

import { useTranslation } from '../../../hooks/useTranslation';
import { Timestamp } from '../../Primitives/Timestamp';
import { ResourceStatusLabel, type StatusLabelProps } from '../../Resource/ResourceStatusLabel';

interface ClusterUpgradeHistoryModalProps {
  attempts: ClusterUpgradeHistoryEntry[];
  onClose: () => void;
}

const ClusterUpgradeHistoryModal = ({ attempts, onClose }: ClusterUpgradeHistoryModalProps) => {
  const { t } = useTranslation();

  const stateDisplay = (state: ClusterUpgradeState): StatusLabelProps => {
    switch (state) {
      case ClusterUpgradeState.PENDING:
        return { text: t('Pending'), status: 'progressing', icon: ClockIcon };
      case ClusterUpgradeState.PROGRESSING:
        return { text: t('In progress'), status: 'progressing' };
      case ClusterUpgradeState.SUCCEEDED:
        return { text: t('Succeeded'), status: 'ready' };
      case ClusterUpgradeState.FAILED:
        return { text: t('Failed'), status: 'failed' };
      case ClusterUpgradeState.TIMED_OUT:
        return {
          text: t('Timed out'),
          status: 'unspecified',
          icon: HourglassHalfIcon,
          iconStatus: 'warning',
        };
      default:
        return { text: t('Unknown'), status: 'unspecified' };
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
                  <Td dataLabel={t('Status')}>
                    <ResourceStatusLabel {...stateDisplay(attempt.state)} noIcon={false} isPlain />
                  </Td>
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
