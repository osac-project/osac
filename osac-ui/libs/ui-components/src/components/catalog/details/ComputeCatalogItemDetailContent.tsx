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

import type { ComputeInstanceCatalogItem } from '@osac/types';

import { useTranslation } from '../../../hooks/useTranslation';
import { guestOsFamilyLabels } from '../../DiskImage/DiskImageTable';
import { Timestamp } from '../../Primitives/Timestamp';
import { findDiskImageForReference } from '../bareMetalCatalogItemResourceDisplay';
import CatalogFieldEditabilityLabel from '../CatalogFieldEditabilityLabel';
import { type FieldPolicyLike, catalogFieldPolicyBehavior } from '../catalogFieldPolicyDisplay';
import { GLOBAL_TENANT_VALUE } from '../catalogItemDisplay';
import { useCatalogItemResourceLookups } from '../catalogItemResourceLookups';
import {
  computeDiskImageReferenceFromPolicy,
  computeInstanceTypeReferenceFromPolicy,
  findComputeInstanceTypeForReference,
  formatComputeCatalogDiskImage,
  formatComputeCatalogMemory,
  formatComputeCatalogStorage,
  formatComputeCatalogVCpu,
  int32ValueFromPolicy,
} from '../computeCatalogItemResourceDisplay';
import CatalogStorageTable, { type CatalogStorageTableRow } from './CatalogStorageTable';
import CatalogUserDataDetailValue from './CatalogUserDataDetailValue';

interface VmCatalogItemDetailContentProps {
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

const policyValue = (policy: unknown): unknown => {
  const behavior = (policy as { behavior?: { case?: string; value?: unknown } } | undefined)
    ?.behavior;
  if (behavior?.case === 'locked') {
    return behavior.value;
  }
  if (behavior?.case === 'editable' && behavior.value && typeof behavior.value === 'object') {
    return (behavior.value as { defaultValue?: unknown }).defaultValue;
  }
  return undefined;
};

const referenceLabel = (reference: unknown): string | undefined => {
  if (!reference || typeof reference !== 'object') {
    return undefined;
  }
  const value = reference as { id?: string; name?: string };
  return value.name?.trim() || value.id?.trim() || undefined;
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

const displayReference = (reference: unknown, t: TFunction): string =>
  referenceLabel(reference) ?? t('Not set');

const VmCatalogItemDetailContent = ({ item }: VmCatalogItemDetailContentProps) => {
  const { t } = useTranslation();
  const resourceLookups = useCatalogItemResourceLookups();
  const fields = item.fields;
  const instanceTypeReference = computeInstanceTypeReferenceFromPolicy(fields?.instanceType);
  const diskImageReference = computeDiskImageReferenceFromPolicy(fields?.diskImage);
  const instanceType = findComputeInstanceTypeForReference(
    resourceLookups.computeInstanceTypes,
    instanceTypeReference,
  );
  const diskImage = findDiskImageForReference(resourceLookups.diskImages, diskImageReference);
  const bootDiskSize = int32ValueFromPolicy(fields?.bootDisk?.sizeGib);
  const storageTier = policyValue(fields?.bootDisk?.storageTier);
  const templateName = referenceLabel(item.template);
  const project = item.metadata?.project?.trim();
  const tenant = item.metadata?.tenant?.trim();
  const scopeLabel = project ? t('Project') : t('Visibility');
  const scopeValue = project
    ? project
    : tenant === GLOBAL_TENANT_VALUE
      ? t('Global public')
      : tenant || t('Not set');
  const guestOsFamily = diskImage?.spec?.guestOsFamily;
  const guestOsValue =
    guestOsFamily === undefined
      ? t('Not set')
      : (guestOsFamilyLabels(t)[guestOsFamily] ?? t('Not set'));
  const diskImageValue =
    formatComputeCatalogDiskImage(fields, diskImage, diskImageReference) ??
    displayReference(diskImageReference, t);
  const instanceTypeValue = instanceType?.metadata?.name
    ? instanceType.metadata.name
    : displayReference(instanceTypeReference, t);
  const vcpuValue = formatComputeCatalogVCpu(fields, instanceType, t) ?? t('Not set');
  const memoryValue = formatComputeCatalogMemory(fields, instanceType, t) ?? t('Not set');
  const gpu = instanceType?.spec?.gpu;
  const bootDiskSizeValue = formatComputeCatalogStorage(fields, bootDiskSize, t) ?? t('Not set');
  const additionalDisks = additionalDisksFromPolicy(policyValue(fields?.additionalDisks));
  const additionalDiskRows: CatalogStorageTableRow[] = additionalDisks.map((disk, index) => ({
    id: `additional-disk-${index}`,
    size: disk.sizeGib === undefined ? t('Not set') : `${disk.sizeGib} GiB`,
    storageTier: referenceLabel(disk.storageTier) ?? t('Not set'),
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
          <DetailRow label={t('Guest OS family')}>{guestOsValue}</DetailRow>
          <DetailRow label={t('Disk image')}>
            <GovernedDetailValue value={diskImageValue} policy={fields?.diskImage} />
          </DetailRow>
          <DetailRow label={t('Instance type')}>
            <GovernedDetailValue value={instanceTypeValue} policy={fields?.instanceType} />
          </DetailRow>
          <DetailRow label={t('vCPUs')}>{vcpuValue}</DetailRow>
          <DetailRow label={t('Memory')}>{memoryValue}</DetailRow>
          {gpu ? (
            <>
              <DetailRow label={t('GPU count')}>{gpu.count}</DetailRow>
              <DetailRow label={t('Resource name')}>{gpu.resourceName || t('Not set')}</DetailRow>
            </>
          ) : null}
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

export default VmCatalogItemDetailContent;
