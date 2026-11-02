import type { ReactNode } from 'react';
import {
  DescriptionListDescription,
  DescriptionListGroup,
  DescriptionListTerm,
  Flex,
  FlexItem,
} from '@patternfly/react-core';

import { useTranslation } from '../../../hooks/useTranslation';
import CatalogFieldEditabilityLabel from '../../catalog/CatalogFieldEditabilityLabel';
import {
  type FieldPolicyLike,
  catalogFieldPolicyBehavior,
  catalogFieldPolicyIsConfigured,
} from '../../catalog/catalogFieldPolicyDisplay';

interface CatalogConfigurationRowProps {
  label: string;
  value?: string;
  policy: unknown;
  valueContent?: ReactNode;
}

export const CatalogConfigurationRow = ({
  label,
  value,
  policy,
  valueContent,
}: CatalogConfigurationRowProps) => {
  const { t } = useTranslation();
  const behavior = catalogFieldPolicyBehavior(policy as FieldPolicyLike);

  if (!behavior) {
    return null;
  }

  return (
    <DescriptionListGroup>
      <DescriptionListTerm>{label}</DescriptionListTerm>
      <DescriptionListDescription>
        <Flex flexWrap={{ default: 'nowrap' }} gap={{ default: 'gapXs' }}>
          <FlexItem>{valueContent ?? value ?? t('Not set')}</FlexItem>
          <FlexItem>
            <CatalogFieldEditabilityLabel behavior={behavior} />
          </FlexItem>
        </Flex>
      </DescriptionListDescription>
    </DescriptionListGroup>
  );
};

export const hasConfiguredPolicy = (...policies: unknown[]): boolean =>
  policies.some((policy) => catalogFieldPolicyIsConfigured(policy as FieldPolicyLike));
