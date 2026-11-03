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
} from '../../catalog/catalogFieldPolicyDisplay';

interface CatalogConfigurationRowProps {
  label: string;
  value: ReactNode;
  policy: FieldPolicyLike;
}

export const CatalogConfigurationRow = ({ label, value, policy }: CatalogConfigurationRowProps) => {
  const { t } = useTranslation();
  const behavior = catalogFieldPolicyBehavior(policy);

  if (!behavior) {
    return null;
  }

  return (
    <DescriptionListGroup>
      <DescriptionListTerm>{label}</DescriptionListTerm>
      <DescriptionListDescription>
        <Flex flexWrap={{ default: 'nowrap' }} gap={{ default: 'gapXs' }}>
          <FlexItem>{value ?? t('Not set')}</FlexItem>
          <FlexItem>
            <CatalogFieldEditabilityLabel behavior={behavior} />
          </FlexItem>
        </Flex>
      </DescriptionListDescription>
    </DescriptionListGroup>
  );
};
