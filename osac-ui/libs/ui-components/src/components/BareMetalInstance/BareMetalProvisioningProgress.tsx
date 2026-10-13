import {
  Card,
  CardBody,
  CardTitle,
  Divider,
  ProgressStep,
  type ProgressStepProps,
  ProgressStepVariant,
  ProgressStepper,
  Stack,
  StackItem,
} from '@patternfly/react-core';
import { RhUiInProgressIcon } from '@patternfly/react-icons/dist/esm/icons/rh-ui-in-progress-icon';
import { TFunction } from 'i18next';

import {
  type BareMetalInstance,
  type BareMetalInstanceCondition,
  BareMetalInstanceConditionType,
  ConditionStatus,
} from '@osac/types';

import { useTranslation } from '../../hooks/useTranslation';

type StepKey = 'host' | 'network' | 'provision' | 'finalizing';

type StepStatus = Pick<ProgressStepProps, 'variant' | 'description' | 'isCurrent'>;

type StepStatusMap = {
  [key in StepKey]?: StepStatus;
};

const getSteps = (conditions: BareMetalInstanceCondition[] = []): StepStatusMap => {
  const provisionedCondition = conditions.find(
    (c) => c.type === BareMetalInstanceConditionType.PROVISIONED,
  );

  if (provisionedCondition?.status === ConditionStatus.TRUE) {
    const readyCondition = conditions.find((c) => c.type === BareMetalInstanceConditionType.READY);

    return {
      host: {
        variant: ProgressStepVariant.success,
      },
      network: {
        variant: ProgressStepVariant.success,
      },
      provision: {
        variant: ProgressStepVariant.success,
      },
      finalizing: {
        isCurrent: true,
        description: readyCondition?.message,
        variant:
          readyCondition?.reason === 'ReadyTimeout'
            ? ProgressStepVariant.danger
            : ProgressStepVariant.pending,
      },
    };
  }

  if (
    [
      'NetworkSetup',
      'NetworkAttachmentFailed',
      'NetworkHandoffFailed',
      'IPDiscoveryFailed',
    ].includes(provisionedCondition?.reason || '')
  ) {
    return {
      host: {
        variant: ProgressStepVariant.success,
      },
      provision: {
        variant: ProgressStepVariant.success,
      },
      network: {
        isCurrent: true,
        description: provisionedCondition?.message,
        variant:
          provisionedCondition?.reason === 'NetworkSetup'
            ? ProgressStepVariant.pending
            : ProgressStepVariant.danger,
      },
    };
  }

  if (['Provisioning', 'ProvisionJobFailed'].includes(provisionedCondition?.reason || '')) {
    return {
      host: {
        variant: ProgressStepVariant.success,
      },
      provision: {
        isCurrent: true,
        description: provisionedCondition?.message,
        variant:
          provisionedCondition?.reason === 'Provisioning'
            ? ProgressStepVariant.pending
            : ProgressStepVariant.danger,
      },
    };
  }

  return {
    host: {
      isCurrent: true,
      description: provisionedCondition?.message,
      variant:
        provisionedCondition?.reason === undefined ||
        provisionedCondition?.reason === 'HostAllocation'
          ? ProgressStepVariant.pending
          : ProgressStepVariant.danger,
    },
  };
};

const getStatusLabel = (step: StepStatus, t: TFunction): string | undefined => {
  if (step.variant === ProgressStepVariant.success) {
    return t('Complete');
  }

  if (step.variant === ProgressStepVariant.danger) {
    return t('Failed');
  }

  if (step.isCurrent) {
    return t('In progress');
  }

  return undefined;
};

const BMProgressStep = ({
  stepId,
  title,
  stepStatus,
}: {
  stepId: StepKey;
  title: string;
  stepStatus: StepStatus | undefined;
}) => {
  const { t } = useTranslation();

  const stepProps = stepStatus ?? { variant: ProgressStepVariant.default };

  const statusLabel = getStatusLabel(stepProps, t);
  const description = stepProps.description || statusLabel;
  const ariaLabel = [
    title,
    statusLabel,
    typeof stepProps.description === 'string' ? stepProps.description : undefined,
  ]
    .filter(Boolean)
    .join(', ');

  return (
    <ProgressStep
      {...stepProps}
      id={stepId}
      titleId={`${stepId}-title`}
      aria-label={ariaLabel}
      description={description}
      icon={stepProps.variant === 'pending' ? <RhUiInProgressIcon /> : undefined}
    >
      {title}
    </ProgressStep>
  );
};

interface Props {
  instance: BareMetalInstance;
}

const BareMetalProvisioningProgress = ({ instance }: Props) => {
  const { t } = useTranslation();

  const step = getSteps(instance.status?.conditions);

  return (
    <Card aria-labelledby="provisioning-progress">
      <CardTitle component="h2" id="provisioning-progress">
        {t('Boot log')}
      </CardTitle>
      <CardBody>
        <Stack hasGutter>
          <StackItem>
            <Divider />
          </StackItem>
          <StackItem>
            <ProgressStepper isCenterAligned aria-label={t('Provisioning progress steps')}>
              <BMProgressStep
                stepId="host"
                stepStatus={step['host']}
                title={t('Host allocation')}
              />
              <BMProgressStep
                stepId="provision"
                stepStatus={step['provision']}
                title={t('Provisioning')}
              />
              <BMProgressStep
                stepId="network"
                stepStatus={step['network']}
                title={t('Network setup')}
              />
              <BMProgressStep
                stepId="finalizing"
                stepStatus={step['finalizing']}
                title={t('Finalizing')}
              />
            </ProgressStepper>
          </StackItem>
        </Stack>
      </CardBody>
    </Card>
  );
};

export default BareMetalProvisioningProgress;
