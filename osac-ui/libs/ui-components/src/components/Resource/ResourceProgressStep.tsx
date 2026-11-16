import { ProgressStep, type ProgressStepProps, ProgressStepVariant } from '@patternfly/react-core';
import { RhUiInProgressIcon } from '@patternfly/react-icons/dist/esm/icons/rh-ui-in-progress-icon';

import { useTranslation } from '../../hooks/useTranslation';

interface ResourceProgressStepProps {
  stepId: string;
  title: string;
  stepStatus?: Pick<ProgressStepProps, 'variant' | 'description' | 'isCurrent'>;
}

const ResourceProgressStep = ({ stepId, title, stepStatus }: ResourceProgressStepProps) => {
  const { t } = useTranslation();
  const variant = stepStatus?.variant ?? ProgressStepVariant.default;
  const statusLabel =
    variant === ProgressStepVariant.success
      ? t('Complete')
      : variant === ProgressStepVariant.danger
        ? t('Failed')
        : variant === ProgressStepVariant.pending && stepStatus?.isCurrent
          ? t('In progress')
          : undefined;
  const ariaLabel = [
    title,
    statusLabel !== title ? statusLabel : undefined,
    typeof stepStatus?.description === 'string' ? stepStatus.description : undefined,
  ]
    .filter(Boolean)
    .join(', ');

  return (
    <ProgressStep
      {...stepStatus}
      id={stepId}
      titleId={`${stepId}-title`}
      variant={variant}
      aria-label={ariaLabel}
      description={stepStatus?.description || statusLabel}
      icon={variant === ProgressStepVariant.pending ? <RhUiInProgressIcon /> : undefined}
    >
      {title}
    </ProgressStep>
  );
};

export default ResourceProgressStep;
