import {
  Content,
  DescriptionList,
  DescriptionListDescription,
  DescriptionListGroup,
  DescriptionListTerm,
  Stack,
  StackItem,
  Title,
} from '@patternfly/react-core';
import { useFormikContext } from 'formik';

import type { Tenant } from '@osac/types/private';

import { useTranslation } from '../../../../hooks/useTranslation';
import { TenantValues, isBillingStepModified } from '../values';

interface TenantReviewStepProps {
  tenant?: Tenant;
  initialValues: TenantValues;
}

const TenantReviewStep = ({ tenant, initialValues }: TenantReviewStepProps) => {
  const { t } = useTranslation();
  const { values } = useFormikContext<TenantValues>();
  const externalBillingId = values.externalBillingId;
  const additionalDomains = values.additionalDomains.filter(Boolean);

  if (tenant) {
    const changes: Array<{ label: string; before: string; after: string }> = [];

    if (values.primaryDomain !== initialValues.primaryDomain) {
      changes.push({
        label: t('Primary email domain'),
        before: initialValues.primaryDomain,
        after: values.primaryDomain,
      });
    }

    const initialAdditionalDomains = initialValues.additionalDomains.filter(Boolean).join(', ');
    const nextAdditionalDomains = additionalDomains.join(', ');
    if (nextAdditionalDomains !== initialAdditionalDomains) {
      changes.push({
        label: t('Additional email domains'),
        before: initialAdditionalDomains,
        after: nextAdditionalDomains,
      });
    }

    if (isBillingStepModified(values, initialValues)) {
      changes.push({
        label: t('External billing ID'),
        before: initialValues.externalBillingId,
        after: externalBillingId,
      });
    }

    return (
      <Stack hasGutter>
        <StackItem>
          <Content component="p">{t('Review your changes before saving.')}</Content>
        </StackItem>
        <StackItem>
          <Title headingLevel="h3" size="md">
            {t('Changes')}
          </Title>
        </StackItem>
        <StackItem>
          {changes.length === 0 ? (
            <Content component="p">
              {t('No changes yet. Update a step to see a before-and-after summary here.')}
            </Content>
          ) : (
            <DescriptionList isHorizontal isCompact aria-label={t('Tenant changes')}>
              {changes.map((change) => (
                <DescriptionListGroup key={change.label}>
                  <DescriptionListTerm>{change.label}</DescriptionListTerm>
                  <DescriptionListDescription>
                    <span
                      className="pf-v6-u-text-color-subtle"
                      style={{ textDecoration: 'line-through' }}
                    >
                      {change.before || '—'}
                    </span>
                    {' → '}
                    <strong>{change.after || '—'}</strong>
                  </DescriptionListDescription>
                </DescriptionListGroup>
              ))}
            </DescriptionList>
          )}
        </StackItem>
      </Stack>
    );
  }

  return (
    <Stack hasGutter>
      <StackItem>
        <Content component="p">
          {t('Review your tenant and billing settings before registering.')}
        </Content>
      </StackItem>
      <StackItem>
        <DescriptionList isCompact aria-label={t('Tenant summary')}>
          <DescriptionListGroup>
            <DescriptionListTerm>{t('Tenant name')}</DescriptionListTerm>
            <DescriptionListDescription>{values.name || '—'}</DescriptionListDescription>
          </DescriptionListGroup>
          <DescriptionListGroup>
            <DescriptionListTerm>{t('Primary email domain')}</DescriptionListTerm>
            <DescriptionListDescription>{values.primaryDomain || '—'}</DescriptionListDescription>
          </DescriptionListGroup>
          {additionalDomains.length > 0 && (
            <DescriptionListGroup>
              <DescriptionListTerm>{t('Additional email domains')}</DescriptionListTerm>
              <DescriptionListDescription>
                {additionalDomains.join(', ') || '—'}
              </DescriptionListDescription>
            </DescriptionListGroup>
          )}
          <DescriptionListGroup>
            <DescriptionListTerm>{t('External billing ID')}</DescriptionListTerm>
            <DescriptionListDescription>
              {externalBillingId || t('Not set — can add later')}
            </DescriptionListDescription>
          </DescriptionListGroup>
        </DescriptionList>
      </StackItem>
    </Stack>
  );
};

export default TenantReviewStep;
