import { Label, Tooltip } from '@patternfly/react-core';

import type { NetworkingResourceOrigin } from './networking-resource-origin';
import { useTranslation } from '../../hooks/useTranslation';

interface NetworkingResourceOriginBadgeProps {
  origin: NetworkingResourceOrigin;
}

/**
 * Compact `Default`/`Auto` label shown next to a networking resource name. The
 * tooltip says why the resource exists, because both origins disable Delete.
 */
const NetworkingResourceOriginBadge = ({ origin }: NetworkingResourceOriginBadgeProps) => {
  const { t } = useTranslation();
  const text = origin === 'default' ? t('Default') : t('Auto');
  const description =
    origin === 'default'
      ? t('Created by OSAC as the tenant default.')
      : t('Created automatically by OSAC.');

  return (
    <Tooltip content={description}>
      <Label isCompact color="grey">
        {text}
      </Label>
    </Tooltip>
  );
};

export default NetworkingResourceOriginBadge;
