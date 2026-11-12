import { Icon, Modal, ModalBody, ModalHeader } from '@patternfly/react-core';
import CheckCircleIcon from '@patternfly/react-icons/dist/esm/icons/check-circle-icon';
import ClockIcon from '@patternfly/react-icons/dist/esm/icons/clock-icon';
import ExclamationCircleIcon from '@patternfly/react-icons/dist/esm/icons/exclamation-circle-icon';
import HourglassHalfIcon from '@patternfly/react-icons/dist/esm/icons/hourglass-half-icon';
import InProgressIcon from '@patternfly/react-icons/dist/esm/icons/in-progress-icon';
import QuestionCircleIcon from '@patternfly/react-icons/dist/esm/icons/question-circle-icon';
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

  const stateDisplay = (state: ClusterUpgradeState) => {
    switch (state) {
      case ClusterUpgradeState.PENDING:
        return { label: t('Pending'), icon: ClockIcon, color: 'info' as const };
      case ClusterUpgradeState.PROGRESSING:
        return { label: t('In progress'), icon: InProgressIcon, color: 'info' as const };
      case ClusterUpgradeState.SUCCEEDED:
        return { label: t('Succeeded'), icon: CheckCircleIcon, color: 'success' as const };
      case ClusterUpgradeState.FAILED:
        return { label: t('Failed'), icon: ExclamationCircleIcon, color: 'danger' as const };
      case ClusterUpgradeState.TIMED_OUT:
        return { label: t('Timed out'), icon: HourglassHalfIcon, color: 'warning' as const };
      default:
        return { label: t('Unknown'), icon: QuestionCircleIcon, color: 'custom' as const };
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
              const { label, icon: StatusIcon, color } = stateDisplay(attempt.state);
              return (
                <Tr key={index}>
                  <Td dataLabel={t('Upgrade')}>
                    {attempt.sourceVersion || '—'} → {attempt.targetVersion || '—'}
                  </Td>
                  <Td dataLabel={t('Status')}>
                    <Icon status={color} isInline className="pf-v6-u-mr-xs" aria-hidden>
                      <StatusIcon />
                    </Icon>
                    {label}
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
