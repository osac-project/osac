import { useState } from 'react';
import { CodeBlock, CodeBlockCode, ExpandableSection } from '@patternfly/react-core';

import type { ComputeInstance } from '@osac/types';

import { useTranslation } from '../../../hooks/useTranslation';

interface VmUserDataCardProps {
  vm: ComputeInstance;
}

const VmUserDataCard = ({ vm }: VmUserDataCardProps) => {
  const { t } = useTranslation();
  const [isExpanded, setIsExpanded] = useState(false);
  const source = vm.spec?.userDataSource?.source;
  const userData = source?.case === 'inline' ? source.value : '';

  if (!userData.trim()) {
    return null;
  }

  return (
    <ExpandableSection
      isExpanded={isExpanded}
      onToggle={(_, expanded) => setIsExpanded(expanded)}
      toggleTextCollapsed={t('Cloud Init User Data')}
      toggleTextExpanded={t('Cloud Init User Data')}
      toggleAriaLabel={isExpanded ? t('Collapse user data') : t('Expand user data')}
      toggleWrapper="h2"
    >
      {isExpanded ? (
        <CodeBlock>
          <CodeBlockCode>{userData}</CodeBlockCode>
        </CodeBlock>
      ) : null}
    </ExpandableSection>
  );
};

export default VmUserDataCard;
