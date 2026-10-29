import type { ReactNode } from 'react';
import {
  Alert,
  Button,
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
  useWizardContext,
} from '@patternfly/react-core';
import InfoCircleIcon from '@patternfly/react-icons/dist/esm/icons/info-circle-icon';
import { useFormikContext } from 'formik';
import type { TFunction } from 'i18next';

import { GuestOSFamily } from '@osac/types';
import type { ComputeInstanceCatalogItem } from '@osac/types';

import type { ComputeCatalogItemValues } from './values';
import { useInstanceType } from '../../../../api/v1/instance-types';
import { useSession } from '../../../../hooks/use-session';
import { useTranslation } from '../../../../hooks/useTranslation';
import type { ProvisioningFieldValue } from '../../../ProvisioningWizard/fields/provisioningFieldValue';
import { formatReviewScalar } from '../../../ProvisioningWizard/reviewFormatters';
import { formatInstanceTypeReviewLabelFromType } from '../../../vm/utils';
import CatalogFieldEditabilityLabel from '../../CatalogFieldEditabilityLabel';

interface ComputeCatalogReviewStepProps {
  item?: ComputeInstanceCatalogItem;
}

interface ReviewSectionProps {
  children: ReactNode;
  stepId: string;
  title: string;
}

interface ReviewRowProps {
  label: string;
  value: ReactNode;
}

interface GovernedReviewValueProps {
  field: ProvisioningFieldValue<unknown>;
  value: ReactNode;
}

interface EditChange {
  after: string;
  before: string;
  label: string;
}

const ReviewRow = ({ label, value }: ReviewRowProps) => (
  <DescriptionListGroup>
    <DescriptionListTerm>{label}</DescriptionListTerm>
    <DescriptionListDescription>{value}</DescriptionListDescription>
  </DescriptionListGroup>
);

const GovernedReviewValue = ({ field, value }: GovernedReviewValueProps) => (
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

const ReviewSection = ({ children, stepId, title }: ReviewSectionProps) => {
  const { t } = useTranslation();
  const { goToStepByIndex, steps } = useWizardContext();

  const handleEdit = () => {
    const step = steps.find(({ id }) => id === stepId);
    if (step) {
      goToStepByIndex(step.index);
    }
  };

  return (
    <StackItem>
      <Stack hasGutter>
        <StackItem>
          <Title headingLevel="h3">
            <Button
              variant="link"
              isInline
              aria-label={t('Edit {{section}}', { section: title })}
              onClick={handleEdit}
            >
              {title}
            </Button>
          </Title>
        </StackItem>
        <StackItem>
          <DescriptionList isCompact aria-label={title}>
            {children}
          </DescriptionList>
        </StackItem>
      </Stack>
    </StackItem>
  );
};

const resourceValueKey = (value: { id?: string; name?: string }) =>
  `${value.id?.trim() ?? ''}|${value.name?.trim() ?? ''}`;

const resourceValueLabel = (value: { id?: string; name?: string }, notSet: string): string =>
  value.name?.trim() || value.id?.trim() || notSet;

const guestOsFamilyLabel = (t: TFunction, value: GuestOSFamily): string => {
  switch (value) {
    case GuestOSFamily.GUEST_OS_FAMILY_LINUX:
      return t('Linux');
    case GuestOSFamily.GUEST_OS_FAMILY_WINDOWS:
      return t('Microsoft Windows');
    case GuestOSFamily.GUEST_OS_FAMILY_UNSPECIFIED:
    default:
      return t('Unspecified');
  }
};

const bootDiskSizeLabel = (t: TFunction, value: string): string =>
  value.trim() ? `${value.trim()} GiB` : t('Not set');

const additionalDisksKey = (values: ComputeCatalogItemValues['additionalDisks']['value']) =>
  JSON.stringify(
    values.map((disk) => ({
      sizeGib: disk.sizeGib.trim(),
      storageTier: resourceValueKey(disk.storageTier),
    })),
  );

const additionalDisksLabel = (
  t: TFunction,
  values: ComputeCatalogItemValues['additionalDisks']['value'],
): string =>
  values.length
    ? values
        .map((disk, index) => {
          const size = disk.sizeGib.trim() ? `${disk.sizeGib.trim()} GiB` : t('Not set');
          const tier = resourceValueLabel(disk.storageTier, t('Not set'));
          return `${t('Disk set {{number}}', { number: index + 1 })}: ${size} · ${tier}`;
        })
        .join('; ')
    : t('Not set');

const userDataKey = (values: ComputeCatalogItemValues) =>
  JSON.stringify({
    locked: values.userData.locked,
    source: values.userDataSource.value,
    inline: values.userData.value,
    secret: values.userDataSecret.value,
  });

const userDataLabel = (t: TFunction, values: ComputeCatalogItemValues): string => {
  if (values.userDataSource.value === 'secret') {
    return values.userDataSecret.value.trim() ? values.userDataSecret.value : t('Not set');
  }
  return values.userData.value.trim() ? t('Configured') : t('Not set');
};

const policyLabel = (t: TFunction, field: ProvisioningFieldValue<unknown>): string =>
  field.locked ? t('Locked') : t('Editable');

const catalogEditChanges = (
  t: TFunction,
  initialValues: ComputeCatalogItemValues,
  values: ComputeCatalogItemValues,
): EditChange[] => {
  const fields = [
    {
      label: t('Guest operating system'),
      before: guestOsFamilyLabel(t, initialValues.guestOsFamily),
      after: guestOsFamilyLabel(t, values.guestOsFamily),
      beforeKey: initialValues.guestOsFamily,
      afterKey: values.guestOsFamily,
    },
    {
      label: t('Disk image'),
      before: resourceValueLabel(initialValues.diskImage.value, t('Not set')),
      after: resourceValueLabel(values.diskImage.value, t('Not set')),
      beforeKey: resourceValueKey(initialValues.diskImage.value),
      afterKey: resourceValueKey(values.diskImage.value),
    },
    {
      label: t('Disk image access'),
      before: policyLabel(t, initialValues.diskImage),
      after: policyLabel(t, values.diskImage),
      beforeKey: initialValues.diskImage.locked,
      afterKey: values.diskImage.locked,
    },
    {
      label: t('Instance type'),
      before: resourceValueLabel(initialValues.instanceType.value, t('Not set')),
      after: resourceValueLabel(values.instanceType.value, t('Not set')),
      beforeKey: resourceValueKey(initialValues.instanceType.value),
      afterKey: resourceValueKey(values.instanceType.value),
    },
    {
      label: t('Instance type access'),
      before: policyLabel(t, initialValues.instanceType),
      after: policyLabel(t, values.instanceType),
      beforeKey: initialValues.instanceType.locked,
      afterKey: values.instanceType.locked,
    },
    {
      label: t('Boot disk size'),
      before: bootDiskSizeLabel(t, initialValues.bootDiskSizeGib.value),
      after: bootDiskSizeLabel(t, values.bootDiskSizeGib.value),
      beforeKey: initialValues.bootDiskSizeGib.value.trim(),
      afterKey: values.bootDiskSizeGib.value.trim(),
    },
    {
      label: t('Boot disk size access'),
      before: policyLabel(t, initialValues.bootDiskSizeGib),
      after: policyLabel(t, values.bootDiskSizeGib),
      beforeKey: initialValues.bootDiskSizeGib.locked,
      afterKey: values.bootDiskSizeGib.locked,
    },
    {
      label: t('Boot storage tier'),
      before: resourceValueLabel(initialValues.bootDiskStorageTier.value, t('Not set')),
      after: resourceValueLabel(values.bootDiskStorageTier.value, t('Not set')),
      beforeKey: resourceValueKey(initialValues.bootDiskStorageTier.value),
      afterKey: resourceValueKey(values.bootDiskStorageTier.value),
    },
    {
      label: t('Boot storage tier access'),
      before: policyLabel(t, initialValues.bootDiskStorageTier),
      after: policyLabel(t, values.bootDiskStorageTier),
      beforeKey: initialValues.bootDiskStorageTier.locked,
      afterKey: values.bootDiskStorageTier.locked,
    },
    {
      label: t('Additional disks'),
      before: additionalDisksLabel(t, initialValues.additionalDisks.value),
      after: additionalDisksLabel(t, values.additionalDisks.value),
      beforeKey: additionalDisksKey(initialValues.additionalDisks.value),
      afterKey: additionalDisksKey(values.additionalDisks.value),
    },
    {
      label: t('Additional disks access'),
      before: policyLabel(t, initialValues.additionalDisks),
      after: policyLabel(t, values.additionalDisks),
      beforeKey: initialValues.additionalDisks.locked,
      afterKey: values.additionalDisks.locked,
    },
    {
      label: t('Cloud-init'),
      before: userDataLabel(t, initialValues),
      after: userDataLabel(t, values),
      beforeKey: userDataKey(initialValues),
      afterKey: userDataKey(values),
    },
    {
      label: t('Cloud-init access'),
      before: policyLabel(t, initialValues.userData),
      after: policyLabel(t, values.userData),
      beforeKey: initialValues.userData.locked,
      afterKey: values.userData.locked,
    },
  ];

  return fields
    .filter(({ beforeKey, afterKey }) => beforeKey !== afterKey)
    .map(({ label, before, after }) => ({ label, before, after }));
};

const ComputeCatalogReviewStep = ({ item }: ComputeCatalogReviewStepProps) => {
  const { t } = useTranslation();
  const { role } = useSession();
  const { initialValues, values } = useFormikContext<ComputeCatalogItemValues>();
  const { data: instanceType } = useInstanceType(values.instanceType.value.id);
  const isEdit = Boolean(item);
  const editChanges = isEdit ? catalogEditChanges(t, initialValues, values) : [];
  const notSet = t('Not set');
  const diskImage = resourceValueLabel(values.diskImage.value, notSet);
  const instanceTypeId = values.instanceType.value.id.trim();
  const instanceTypeLabel = instanceTypeId
    ? formatInstanceTypeReviewLabelFromType(instanceType, undefined, instanceTypeId)
    : notSet;
  const userDataReview = userDataLabel(t, values);

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
            'Confirm your settings before creating the catalog item. It will be saved as unpublished until you publish it. Click a section title to edit.',
          )}
        </Content>
      </StackItem>

      <ReviewSection title={t('Details')} stepId="details">
        {role === 'admin' && (
          <ReviewRow
            label={t('Scope')}
            value={
              values.scope === 'global'
                ? t('Global')
                : formatReviewScalar(values.metadata.tenant.name || values.metadata.tenant.id)
            }
          />
        )}
        <ReviewRow label={t('Name')} value={formatReviewScalar(values.metadata.name)} />
        <ReviewRow
          label={t('Provisioning template')}
          value={formatReviewScalar(values.template.name || values.template.id)}
        />
        <ReviewRow label={t('Status')} value={t('Unpublished')} />
      </ReviewSection>

      <ReviewSection title={t('Compute configuration')} stepId="configuration">
        <ReviewRow
          label={t('Guest operating system')}
          value={guestOsFamilyLabel(t, values.guestOsFamily)}
        />
        <ReviewRow
          label={t('Disk image')}
          value={<GovernedReviewValue field={values.diskImage} value={diskImage} />}
        />
        <ReviewRow
          label={t('Instance type')}
          value={<GovernedReviewValue field={values.instanceType} value={instanceTypeLabel} />}
        />
      </ReviewSection>

      <ReviewSection title={t('Storage')} stepId="storage">
        <ReviewRow
          label={t('Boot disk size')}
          value={
            <GovernedReviewValue
              field={values.bootDiskSizeGib}
              value={bootDiskSizeLabel(t, values.bootDiskSizeGib.value)}
            />
          }
        />
        <ReviewRow
          label={t('Boot storage tier')}
          value={
            <GovernedReviewValue
              field={values.bootDiskStorageTier}
              value={resourceValueLabel(values.bootDiskStorageTier.value, notSet)}
            />
          }
        />
        <ReviewRow
          label={t('Additional disks')}
          value={
            values.additionalDisks.value.length ? (
              <GovernedReviewValue
                field={values.additionalDisks}
                value={
                  <Stack hasGutter={false}>
                    {values.additionalDisks.value.map((disk, index) => (
                      <StackItem key={`${disk.sizeGib}-${index}`}>
                        {`${t('Disk set {{number}}', { number: index + 1 })}: ${
                          disk.sizeGib.trim() ? `${disk.sizeGib.trim()} GiB` : notSet
                        } · ${resourceValueLabel(disk.storageTier, notSet)}`}
                      </StackItem>
                    ))}
                  </Stack>
                }
              />
            ) : (
              notSet
            )
          }
        />
      </ReviewSection>

      <ReviewSection title={t('Access & Initial run')} stepId="access-init">
        <ReviewRow
          label={t('Cloud-init')}
          value={<GovernedReviewValue field={values.userData} value={userDataReview} />}
        />
      </ReviewSection>
    </Stack>
  );
};

export default ComputeCatalogReviewStep;
