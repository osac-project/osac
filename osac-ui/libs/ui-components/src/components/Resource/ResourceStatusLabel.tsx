import type { ComponentType, SVGProps } from 'react';
import { Icon, Label } from '@patternfly/react-core';
import CheckCircleIcon from '@patternfly/react-icons/dist/esm/icons/check-circle-icon';
import ExclamationCircleIcon from '@patternfly/react-icons/dist/esm/icons/exclamation-circle-icon';
import InProgressIcon from '@patternfly/react-icons/dist/esm/icons/in-progress-icon';
import QuestionCircleIcon from '@patternfly/react-icons/dist/esm/icons/question-circle-icon';

export type StatusKind = 'ready' | 'failed' | 'progressing' | 'unspecified';

export type LabelColor = 'green' | 'red' | 'blue' | 'grey';

type IconStatus = 'success' | 'danger' | 'info' | 'warning' | 'custom';

type StatusStyle = {
  color: LabelColor;
  icon: ComponentType<SVGProps<SVGSVGElement>>;
  iconStatus: IconStatus;
};

const STATUS_STYLE: Record<StatusKind, StatusStyle> = {
  ready: { color: 'green', icon: CheckCircleIcon, iconStatus: 'success' },
  failed: { color: 'red', icon: ExclamationCircleIcon, iconStatus: 'danger' },
  progressing: { color: 'blue', icon: InProgressIcon, iconStatus: 'info' },
  unspecified: { color: 'grey', icon: QuestionCircleIcon, iconStatus: 'custom' },
};

export interface StatusLabelProps {
  status: StatusKind;
  text: string;
  color?: LabelColor;
  icon?: ComponentType<SVGProps<SVGSVGElement>>;
  noIcon?: boolean;
  isCompact?: boolean;
  isPlain?: boolean;
  iconStatus?: IconStatus;
}

export const ResourceStatusLabel = ({
  status,
  text,
  color,
  icon,
  noIcon = true,
  isCompact = true,
  isPlain = false,
  iconStatus,
}: StatusLabelProps) => {
  const style = STATUS_STYLE[status];
  const labelColor = color ?? style.color;
  const StatusIcon = icon ?? style.icon;
  if (isPlain) {
    return (
      <span>
        {!noIcon && (
          <Icon
            status={iconStatus ?? style.iconStatus}
            isInline
            className="pf-v6-u-mr-xs"
            aria-hidden
          >
            <StatusIcon />
          </Icon>
        )}
        {text}
      </span>
    );
  }
  return (
    <Label
      color={labelColor}
      icon={noIcon ? undefined : <StatusIcon aria-hidden />}
      isCompact={isCompact}
    >
      {text}
    </Label>
  );
};
