import { Alert, List, ListItem } from '@patternfly/react-core';

import {
  type ComputeInstance,
  ComputeInstanceConditionType,
  ComputeInstanceState,
  ConditionStatus,
} from '@osac/types';

import { useTranslation } from '../../hooks/useTranslation';

export const hasRestartRequiredCondition = (vm: ComputeInstance): boolean =>
  (vm.status?.conditions ?? []).some(
    (condition) =>
      condition.type === ComputeInstanceConditionType.RESTART_REQUIRED &&
      condition.status === ConditionStatus.TRUE,
  );

const RestartRequiredBanner = ({ vm }: { vm: ComputeInstance }) => {
  const { t } = useTranslation();

  if (!hasRestartRequiredCondition(vm)) {
    return null;
  }

  const paused = vm.status?.state === ComputeInstanceState.PAUSED;

  return (
    <Alert variant="warning" isInline isExpandable title={t('Restart required')}>
      {paused
        ? t(
            'This virtual machine was resized while paused. Changes apply after the virtual machine is restarted.',
          )
        : t('Changes apply after the virtual machine is restarted.')}
      <List>
        <ListItem>{t('Detected changes')}</ListItem>
        <ListItem>{t('Details > Instance type')}</ListItem>
      </List>
    </Alert>
  );
};

export default RestartRequiredBanner;
