import { useState } from 'react';
import type { ReactNode } from 'react';
import { Link } from 'react-router-dom';
import { CodeBlock, CodeBlockCode, ExpandableSection } from '@patternfly/react-core';

import { useTranslation } from '../../hooks/useTranslation';

interface UserDataValue {
  source: 'inline' | 'secret';
  value: string;
  secretId?: string;
}

interface UserDataDetailValueProps {
  userData?: UserDataValue;
  emptyValue: ReactNode;
}

const UserDataDetailValue = ({ userData, emptyValue }: UserDataDetailValueProps) => {
  const { t } = useTranslation();
  const [isExpanded, setIsExpanded] = useState(false);
  const hasValue = userData
    ? userData.source === 'secret'
      ? Boolean(userData.value.trim() || userData.secretId)
      : Boolean(userData.value.trim())
    : false;

  if (!userData || !hasValue) {
    return emptyValue;
  }

  if (userData.source === 'secret') {
    const secretLabel = userData.value.trim() || userData.secretId || t('Configured');

    return userData.secretId ? (
      <Link to={`/secrets/${userData.secretId}`}>{secretLabel}</Link>
    ) : (
      secretLabel
    );
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

export default UserDataDetailValue;
