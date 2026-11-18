import { Label, Tooltip } from '@patternfly/react-core';

import { type ResourceOrigin, autoCreatedFor, resourceOrigin } from './resourceOrigin';
import { useTranslation } from '../../hooks/useTranslation';

interface ResourceOriginBadgeProps {
  labels?: Record<string, string>;
}

/** The Default and Auto badges, with a tooltip that says who owns the resource. */
const ResourceOriginBadge = ({ labels }: ResourceOriginBadgeProps) => {
  const { t } = useTranslation();
  const origin = resourceOrigin(labels);
  if (!origin) {
    return null;
  }

  const tooltip: Record<ResourceOrigin, string> = {
    default: t('Set by the system for your tenant. It cannot be deleted on its own.'),
    auto: autoCreatedFor(labels)
      ? t('Created automatically by {{owner}}.', { owner: autoCreatedFor(labels) })
      : t('Created automatically by the system.'),
  };

  return (
    <Tooltip content={tooltip[origin]}>
      <Label isCompact color={origin === 'default' ? 'grey' : 'blue'}>
        {origin === 'default' ? t('Default') : t('Auto')}
      </Label>
    </Tooltip>
  );
};

export default ResourceOriginBadge;
