import type { ReactNode } from 'react';
import {
  Alert,
  Content,
  DescriptionList,
  DescriptionListDescription,
  DescriptionListGroup,
  DescriptionListTerm,
  Flex,
  FlexItem,
  Stack,
  StackItem,
  Title,
} from '@patternfly/react-core';
import InfoCircleIcon from '@patternfly/react-icons/dist/esm/icons/info-circle-icon';
import type { TFunction } from 'i18next';

import { useTranslation } from '../../hooks/useTranslation';
import CatalogFieldEditabilityLabel from '../catalog/CatalogFieldEditabilityLabel';
import type { ProvisioningFieldValue } from '../ProvisioningWizard/fields/provisioningFieldValue';

export interface CatalogReviewChange {
  after: string;
  before: string;
  label: string;
}

export interface CatalogReviewField {
  after: string;
  afterKey: unknown;
  before: string;
  beforeKey: unknown;
  label: string;
}

interface CatalogReviewStepProps {
  children: ReactNode;
  editChanges: CatalogReviewChange[];
  isEdit: boolean;
}

interface CatalogReviewSectionProps {
  children: ReactNode;
  title: string;
}

interface CatalogReviewRowProps {
  label: string;
  value: ReactNode;
}

interface CatalogReviewValueProps {
  field: ProvisioningFieldValue<unknown>;
  value: ReactNode;
}

interface CatalogResourceReference {
  id?: string;
  name?: string;
}

export const CatalogReviewRow = ({ label, value }: CatalogReviewRowProps) => (
  <DescriptionListGroup>
    <DescriptionListTerm>{label}</DescriptionListTerm>
    <DescriptionListDescription>{value}</DescriptionListDescription>
  </DescriptionListGroup>
);

export const CatalogReviewValue = ({ field, value }: CatalogReviewValueProps) => (
  <Flex
    alignItems={{ default: 'alignItemsCenter' }}
    flexWrap={{ default: 'wrap' }}
    gap={{ default: 'gapSm' }}
  >
    <FlexItem>{value}</FlexItem>
    <FlexItem>
      <CatalogFieldEditabilityLabel behavior={field.locked ? 'locked' : 'editable'} />
    </FlexItem>
  </Flex>
);

export const CatalogReviewSection = ({ children, title }: CatalogReviewSectionProps) => (
  <StackItem>
    <Stack hasGutter>
      <StackItem>
        <Title headingLevel="h3">{title}</Title>
      </StackItem>
      <StackItem>
        <DescriptionList isCompact aria-label={title}>
          {children}
        </DescriptionList>
      </StackItem>
    </Stack>
  </StackItem>
);

export const catalogReviewChanges = (fields: CatalogReviewField[]): CatalogReviewChange[] =>
  fields
    .filter(({ beforeKey, afterKey }) => beforeKey !== afterKey)
    .map(({ label, before, after }) => ({ label, before, after }));

export const catalogReviewPolicyLabel = (
  t: TFunction,
  field: ProvisioningFieldValue<unknown>,
): string => (field.locked ? t('Locked') : t('Editable'));

export const catalogReviewValue = (t: TFunction, value?: string): string =>
  value?.trim() || t('Not set');

export const catalogReviewResourceKey = (value: CatalogResourceReference): string =>
  `${value.id?.trim() ?? ''}|${value.name?.trim() ?? ''}`;

const CatalogReview = ({ children, editChanges, isEdit }: CatalogReviewStepProps) => {
  const { t } = useTranslation();

  if (isEdit) {
    return (
      <Stack hasGutter>
        <StackItem>
          <Title headingLevel="h2" size="lg">
            {t('Review your changes before saving')}
          </Title>
        </StackItem>
        <StackItem>
          {editChanges.length ? (
            <Stack hasGutter aria-label={t('Changed fields')}>
              {editChanges.map((change) => (
                <StackItem key={change.label}>
                  <Title headingLevel="h4" size="md">
                    {change.label}
                  </Title>
                  <Flex gap={{ default: 'gapSm' }} flexWrap={{ default: 'wrap' }}>
                    <FlexItem>{change.before}</FlexItem>
                    <FlexItem aria-hidden="true">→</FlexItem>
                    <FlexItem>{change.after}</FlexItem>
                  </Flex>
                </StackItem>
              ))}
            </Stack>
          ) : (
            <Content component="p">
              {t('No changes yet. Update a step to see a before-and-after summary here.')}
            </Content>
          )}
        </StackItem>
        <StackItem>
          <Alert
            variant="info"
            customIcon={<InfoCircleIcon />}
            isInline
            title={t('Changes apply immediately')}
          >
            {t('Saving updates this catalog item right away.')}
          </Alert>
        </StackItem>
      </Stack>
    );
  }

  return (
    <Stack hasGutter>
      <StackItem>
        <Title headingLevel="h2" size="lg">
          {t('Review and create')}
        </Title>
      </StackItem>
      <StackItem>
        <Content component="p">
          {t(
            'Confirm your settings before creating the catalog item. It will be saved as unpublished until you publish it.',
          )}
        </Content>
      </StackItem>
      {children}
    </Stack>
  );
};

export default CatalogReview;
