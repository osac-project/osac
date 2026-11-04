import { useState } from 'react';
import type { ReactNode } from 'react';
import { CodeBlock, CodeBlockCode, ExpandableSection } from '@patternfly/react-core';

import type { UserDataSource } from '@osac/types';

import { useTranslation } from '../../hooks/useTranslation';
import ResourceLink from '../Resource/ResourceLink';

interface UserDataDetailValueProps {
  userData?: UserDataSource;
  emptyValue: ReactNode;
}

const UserDataDetailValue = ({ userData, emptyValue }: UserDataDetailValueProps) => {
  const { t } = useTranslation();
  const [isExpanded, setIsExpanded] = useState(false);
  const source = userData?.source;
  if (source?.case === 'secret' && source.value) {
    const secretLabel = source.value.name;

    return source.value.id ? (
      <ResourceLink basePath="/secrets" id={source.value.id} name={secretLabel} />
    ) : (
      secretLabel
    );
  }

  if (!source?.value) {
    return emptyValue;
  }

  return (
    <ExpandableSection
      isExpanded={isExpanded}
      onToggle={(_, expanded) => setIsExpanded(expanded)}
      toggleContent={t('Configured')}
      toggleAriaLabel={isExpanded ? t('Collapse user data') : t('Expand user data')}
    >
      <CodeBlock>
        <CodeBlockCode>{source.value}</CodeBlockCode>
      </CodeBlock>
    </ExpandableSection>
  );
};

export default UserDataDetailValue;
