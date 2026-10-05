import {
  DescriptionList,
  DescriptionListDescription,
  DescriptionListGroup,
  DescriptionListTerm,
  Stack,
  StackItem,
  Title,
} from '@patternfly/react-core';
import { useFormikContext } from 'formik';

import { useTranslation } from '@osac/ui-components/hooks/useTranslation';
import { displayValue } from '@osac/ui-components/utils/detailFormatters';

import type { ExternalIpPoolFormValues } from './values';

const ReviewStep = () => {
  const { t } = useTranslation();
  const { values } = useFormikContext<ExternalIpPoolFormValues>();

  return (
    <Stack hasGutter>
      <StackItem>
        <Title headingLevel="h2" size="lg">
          {t('Review')}
        </Title>
      </StackItem>
      <StackItem>
        <DescriptionList isHorizontal isCompact aria-label={t('Review')}>
          <DescriptionListGroup>
            <DescriptionListTerm>{t('Name')}</DescriptionListTerm>
            <DescriptionListDescription>
              {displayValue(values.metadata.name)}
            </DescriptionListDescription>
          </DescriptionListGroup>
          <DescriptionListGroup>
            <DescriptionListTerm>{t('CIDRs')}</DescriptionListTerm>
            <DescriptionListDescription>
              {values.cidrs.filter((cidr) => cidr.trim()).join(', ') || displayValue()}
            </DescriptionListDescription>
          </DescriptionListGroup>
          <DescriptionListGroup>
            <DescriptionListTerm>{t('Tenant')}</DescriptionListTerm>
            <DescriptionListDescription>
              {displayValue(values.metadata.tenant.name || values.metadata.tenant.id)}
            </DescriptionListDescription>
          </DescriptionListGroup>
        </DescriptionList>
      </StackItem>
    </Stack>
  );
};

export default ReviewStep;
