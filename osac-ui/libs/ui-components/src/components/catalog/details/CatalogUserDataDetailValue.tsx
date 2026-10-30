import { useState } from 'react';
import { CodeBlock, CodeBlockCode, ExpandableSection } from '@patternfly/react-core';

import { useTranslation } from '../../../hooks/useTranslation';
import type { CatalogFieldPolicyLike } from '../authoring/catalogItemPolicy';
import { catalogUserDataValueFromPolicy } from '../authoring/catalogUserData';

interface CatalogUserDataDetailValueProps {
  policy: CatalogFieldPolicyLike;
}

const CatalogUserDataDetailValue = ({ policy }: CatalogUserDataDetailValueProps) => {
  const { t } = useTranslation();
  const [isExpanded, setIsExpanded] = useState(false);
  const userData = catalogUserDataValueFromPolicy(policy);
  const hasValue = userData.value.trim().length > 0;

  if (!hasValue) {
    return t('Not set');
  }

  if (userData.source === 'secret') {
    return t('Configured');
  }

  return (
    <ExpandableSection
      isExpanded={isExpanded}
      onToggle={(_, expanded) => setIsExpanded(expanded)}
      toggleContent={t('Configured')}
      toggleAriaLabel={isExpanded ? t('Collapse user data') : t('Expand user data')}
    >
      <CodeBlock>
        <CodeBlockCode>{userData.value}</CodeBlockCode>
      </CodeBlock>
    </ExpandableSection>
  );
};

export default CatalogUserDataDetailValue;
