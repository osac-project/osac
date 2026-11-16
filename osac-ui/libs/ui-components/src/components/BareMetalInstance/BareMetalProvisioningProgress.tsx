import {
  Card,
  CardBody,
  CardTitle,
  Divider,
  type ProgressStepProps,
  ProgressStepVariant,
  ProgressStepper,
  Stack,
  StackItem,
} from '@patternfly/react-core';

import {
  type BareMetalInstance,
  type BareMetalInstanceCondition,
  BareMetalInstanceConditionType,
  ConditionStatus,
} from '@osac/types';

import { useTranslation } from '../../hooks/useTranslation';
import ResourceProgressStep from '../Resource/ResourceProgressStep';

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
              <ResourceProgressStep
                stepId="host"
                stepStatus={step['host']}
                title={t('Host allocation')}
              />
              <ResourceProgressStep
                stepId="provision"
                stepStatus={step['provision']}
                title={t('Provisioning')}
              />
              <ResourceProgressStep
                stepId="network"
                stepStatus={step['network']}
                title={t('Network setup')}
              />
              <ResourceProgressStep
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
