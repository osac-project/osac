import { Label, Tooltip } from '@patternfly/react-core';

import {
  type LabeledResource,
  autoCreatedTooltip,
  defaultResourceTooltip,
  isAutoCreatedResource,
  isDefaultResource,
} from './resourceOrigin';
import { useTranslation } from '../../hooks/useTranslation';

interface ResourceOriginBadgeProps {
  resource?: LabeledResource;
}

/** Shows the Default or Auto badge of a networking resource, or nothing. */
const ResourceOriginBadge = ({ resource }: ResourceOriginBadgeProps) => {
  const { t } = useTranslation();

  if (isAutoCreatedResource(resource)) {
    return (
      <Tooltip content={autoCreatedTooltip(t, resource)}>
        <Label color="blue" isCompact tabIndex={0}>
          {t('Auto')}
        </Label>
      </Tooltip>
    );
  }
  if (isDefaultResource(resource)) {
    return (
      <Tooltip content={defaultResourceTooltip(t)}>
        <Label color="grey" isCompact tabIndex={0}>
          {t('Default')}
        </Label>
      </Tooltip>
    );
  }
  return null;
};

export default ResourceOriginBadge;
