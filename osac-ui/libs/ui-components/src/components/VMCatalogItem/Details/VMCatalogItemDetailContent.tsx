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

import type { ComputeInstanceCatalogItem, StorageTierReference, UserDataSource } from '@osac/types';

import { useTranslation } from '../../../hooks/useTranslation';
import CatalogFieldEditabilityLabel from '../../catalog/CatalogFieldEditabilityLabel';
import {
  type FieldPolicyLike,
  catalogFieldPolicyBehavior,
  catalogFieldPolicyValue,
} from '../../catalog/catalogFieldPolicyDisplay';
import { GLOBAL_TENANT_VALUE } from '../../catalog/catalogItemDisplay';
import {
  computeDiskImageReferenceFromPolicy,
  computeInstanceTypeReferenceFromPolicy,
  int32ValueFromPolicy,
} from '../../catalog/computeCatalogItemResourceDisplay';
import { Timestamp } from '../../Primitives/Timestamp';
import AdditionalDisksTable from '../../shared/AdditionalDisksTable';
import UserDataDetailValue from '../../UserData/UserDataDetailValue';

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

const VMCatalogItemDetailContent = ({ item }: VMCatalogItemDetailContentProps) => {
  const { t } = useTranslation();
  const fields = item.fields;
  const instanceTypeReference = computeInstanceTypeReferenceFromPolicy(fields?.instanceType);
  const diskImageReference = computeDiskImageReferenceFromPolicy(fields?.diskImage);
  const bootDiskSize = int32ValueFromPolicy(fields?.bootDisk?.sizeGib);
  const storageTier = catalogFieldPolicyValue<StorageTierReference>(fields?.bootDisk?.storageTier);
  const templateName = item.template?.name;
  const project = item.metadata?.project;
  const tenant = item.metadata?.tenant;
  const visibilityValue =
    tenant === GLOBAL_TENANT_VALUE ? t('Global public') : tenant || t('Not set');
  const diskImageValue = diskImageReference?.name || t('Not set');
  const instanceTypeValue = instanceTypeReference?.name || t('Not set');
  const bootDiskSizeValue = bootDiskSize === undefined ? t('Not set') : `${bootDiskSize} GiB`;
  const additionalDisks = catalogFieldPolicyValue(fields?.additionalDisks)?.items || [];

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
          <DetailRow label={t('Provisioning template')}>{templateName || t('Not set')}</DetailRow>
          <DetailRow label={t('Visibility')}>{visibilityValue}</DetailRow>
          {project ? <DetailRow label={t('Project')}>{project}</DetailRow> : null}
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
              value={
                <UserDataDetailValue
                  userData={catalogFieldPolicyValue<UserDataSource>(fields?.userDataSource)}
                  emptyValue={t('Not set')}
                />
              }
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
              value={storageTier?.name || t('Not set')}
              policy={fields?.bootDisk?.storageTier}
            />
          </DetailRow>
          <DetailRow
            label={
              <GovernedDetailValue value={t('Additional disks')} policy={fields?.additionalDisks} />
            }
          >
            {additionalDisks.length ? (
              <AdditionalDisksTable disks={additionalDisks} />
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
