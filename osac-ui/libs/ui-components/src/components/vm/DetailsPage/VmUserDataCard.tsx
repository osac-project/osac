import { useState } from 'react';
import {
  Card,
  CardBody,
  CardExpandableContent,
  CardHeader,
  CardTitle,
} from '@patternfly/react-core';

import type { ComputeInstance } from '@osac/types';

import { useTranslation } from '../../../hooks/useTranslation';

interface VmUserDataCardProps {
  vm: ComputeInstance;
}

const VmUserDataCard = (_props: VmUserDataCardProps) => {
  const { t } = useTranslation();
  const [isExpanded, setIsExpanded] = useState(false);
  const userData = undefined;

  if (!userData) {
    return null;
  }

  const cardId = 'vm-user-data-card';
  const titleId = 'vm-user-data-card-title';
  const toggleId = 'vm-user-data-toggle';

  return (
    <Card id={cardId} isExpanded={isExpanded}>
      <CardHeader
        onExpand={() => setIsExpanded((current) => !current)}
        toggleButtonProps={{
          id: toggleId,
          'aria-label': isExpanded ? t('Collapse user data') : t('Expand user data'),
          'aria-labelledby': `${titleId} ${toggleId}`,
          'aria-expanded': isExpanded,
        }}
      >
        <CardTitle id={titleId}>{t('Cloud Init User Data')}</CardTitle>
      </CardHeader>
      <CardExpandableContent>
        <CardBody>
          <pre style={{ margin: 0, whiteSpace: 'pre-wrap' }}>{userData}</pre>
        </CardBody>
      </CardExpandableContent>
    </Card>
  );
};

export default VmUserDataCard;
