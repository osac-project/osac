import type { ReactNode } from 'react';
import {
  DescriptionList,
  DescriptionListDescription,
  DescriptionListGroup,
  DescriptionListTerm,
  Divider,
  Flex,
  FlexItem,
  Grid,
  GridItem,
  Label,
  Stack,
  StackItem,
  Title,
} from '@patternfly/react-core';
import type { TFunction } from 'i18next';

import type { ComputeInstanceCatalogItem, StorageTierReference } from '@osac/types';

import { useTranslation } from '../../../hooks/useTranslation';
import { Timestamp } from '../../Primitives/Timestamp';
import CatalogFieldEditabilityLabel from '../CatalogFieldEditabilityLabel';
import {
  type CatalogReference,
  type FieldPolicyLike,
  catalogFieldPolicyBehavior,
  catalogFieldPolicyValue,
  catalogReferenceLabel,
} from '../catalogFieldPolicyDisplay';
import { GLOBAL_TENANT_VALUE } from '../catalogItemDisplay';
import {
  computeDiskImageReferenceFromPolicy,
  computeInstanceTypeReferenceFromPolicy,
  int32ValueFromPolicy,
} from '../computeCatalogItemResourceDisplay';
import CatalogStorageTable, { type CatalogStorageTableRow } from './CatalogStorageTable';
import CatalogUserDataDetailValue from './CatalogUserDataDetailValue';

interface VMCatalogItemDetailContentProps {
  item: ComputeInstanceCatalogItem;
}

interface DetailSectionProps {
  title: string;
  children: ReactNode;
}

interface DetailRowProps {
  label: ReactNode;
  children: ReactNode;
}

interface GovernedDetailValueProps {
  value: ReactNode;
  policy: unknown;
}

const DetailSection = ({ title, children }: DetailSectionProps) => (
  <Stack hasGutter>
    <StackItem>
      <Title headingLevel="h2" size="lg">
        {title}
      </Title>
    </StackItem>
    <StackItem>
      <DescriptionList isCompact>{children}</DescriptionList>
    </StackItem>
  </Stack>
);

const DetailRow = ({ label, children }: DetailRowProps) => (
  <DescriptionListGroup>
    <DescriptionListTerm>{label}</DescriptionListTerm>
    <DescriptionListDescription>{children}</DescriptionListDescription>
  </DescriptionListGroup>
);

const GovernedDetailValue = ({ value, policy }: GovernedDetailValueProps) => {
  const behavior = catalogFieldPolicyBehavior(policy as FieldPolicyLike);

  return (
    <Flex flexWrap={{ default: 'wrap' }} gap={{ default: 'gapXs' }}>
      <FlexItem>{value}</FlexItem>
      {behavior ? (
        <FlexItem>
          <CatalogFieldEditabilityLabel behavior={behavior} />
        </FlexItem>
      ) : null}
    </Flex>
  );
};

interface AdditionalDisk {
  sizeGib?: number;
  storageTier?: { id?: string; name?: string };
}

const additionalDisksFromPolicy = (value: unknown): AdditionalDisk[] => {
  if (!value || typeof value !== 'object') {
    return [];
  }
  const items = (value as { items?: AdditionalDisk[] }).items;
  return items ?? [];
};

const displayReference = (reference: CatalogReference | undefined, t: TFunction): string =>
  catalogReferenceLabel(reference) ?? t('Not set');

const VMCatalogItemDetailContent = ({ item }: VMCatalogItemDetailContentProps) => {
  const { t } = useTranslation();
  const fields = item.fields;
  const instanceTypeReference = computeInstanceTypeReferenceFromPolicy(fields?.instanceType);
  const diskImageReference = computeDiskImageReferenceFromPolicy(fields?.diskImage);
  const bootDiskSize = int32ValueFromPolicy(fields?.bootDisk?.sizeGib);
  const storageTier = catalogFieldPolicyValue<StorageTierReference>(fields?.bootDisk?.storageTier);
  const templateName = catalogReferenceLabel(item.template);
  const project = item.metadata?.project?.trim();
  const tenant = item.metadata?.tenant?.trim();
  const scopeLabel = project ? t('Project') : t('Visibility');
  const scopeValue = project
    ? project
    : tenant === GLOBAL_TENANT_VALUE
      ? t('Global public')
      : tenant || t('Not set');
  const diskImageValue = displayReference(diskImageReference, t);
  const instanceTypeValue = displayReference(instanceTypeReference, t);
  const bootDiskSizeValue = bootDiskSize === undefined ? t('Not set') : `${bootDiskSize} GiB`;
  const additionalDisks = additionalDisksFromPolicy(
    catalogFieldPolicyValue(fields?.additionalDisks),
  );
  const additionalDiskRows: CatalogStorageTableRow[] = additionalDisks.map((disk, index) => ({
    id: `additional-disk-${index}`,
    size: disk.sizeGib === undefined ? t('Not set') : `${disk.sizeGib} GiB`,
    storageTier: catalogReferenceLabel(disk.storageTier) ?? t('Not set'),
  }));

  return (
    <Grid hasGutter>
      <GridItem md={4}>
        <DetailSection title={t('Overview')}>
          <DetailRow label={t('Service')}>{t('Virtual machines')}</DetailRow>
          <DetailRow label={t('Status')}>
            <Label color={item.published ? 'green' : undefined} isCompact>
              {item.published ? t('Live') : t('Unpublished')}
            </Label>
          </DetailRow>
          <DetailRow label={t('Provisioning template')}>{templateName ?? t('Not set')}</DetailRow>
          <DetailRow label={scopeLabel}>{scopeValue}</DetailRow>
          <DetailRow label={t('Created')}>
            <Timestamp value={item.metadata?.creationTimestamp} fallback={t('Not set')} />
          </DetailRow>
        </DetailSection>
      </GridItem>

      <GridItem md={4}>
        <DetailSection title={t('Configuration')}>
          <DetailRow label={t('Disk image')}>
            <GovernedDetailValue value={diskImageValue} policy={fields?.diskImage} />
          </DetailRow>
          <DetailRow label={t('Instance type')}>
            <GovernedDetailValue value={instanceTypeValue} policy={fields?.instanceType} />
          </DetailRow>
        </DetailSection>
      </GridItem>

      <GridItem md={4}>
        <DetailSection title={t('Access & Initial run')}>
          <DetailRow label={t('Cloud-init')}>
            <GovernedDetailValue
              value={<CatalogUserDataDetailValue policy={fields?.userDataSource} />}
              policy={fields?.userDataSource}
            />
          </DetailRow>
        </DetailSection>
      </GridItem>

      <GridItem md={12}>
        <Divider />
      </GridItem>

      <GridItem md={12}>
        <DetailSection title={t('Storage')}>
          <DetailRow label={t('Boot disk size')}>
            <GovernedDetailValue value={bootDiskSizeValue} policy={fields?.bootDisk?.sizeGib} />
          </DetailRow>
          <DetailRow label={t('Boot storage tier')}>
            <GovernedDetailValue
              value={displayReference(storageTier, t)}
              policy={fields?.bootDisk?.storageTier}
            />
          </DetailRow>
          <DetailRow
            label={
              <GovernedDetailValue value={t('Additional disks')} policy={fields?.additionalDisks} />
            }
          >
            {additionalDiskRows.length ? (
              <CatalogStorageTable rows={additionalDiskRows} />
            ) : (
              t('Not set')
            )}
          </DetailRow>
        </DetailSection>
      </GridItem>
    </Grid>
  );
};

export default VMCatalogItemDetailContent;
