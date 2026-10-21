import type { ReactNode } from 'react';
import {
  Content,
  DescriptionList,
  DescriptionListDescription,
  DescriptionListGroup,
  DescriptionListTerm,
  Flex,
  FlexItem,
} from '@patternfly/react-core';
import type { TFunction } from 'i18next';

import type {
  BareMetalInstanceCatalogItem,
  ClusterCatalogItem,
  ComputeInstanceCatalogItem,
} from '@osac/types';

import { useTranslation } from '../../../hooks/useTranslation';
import { formatReviewScalar } from '../../ProvisioningWizard/reviewFormatters';
import {
  bareMetalCatalogItemFields,
  bareMetalDiskImageReferenceFromPolicy,
  bareMetalInstanceTypeReferenceFromPolicy,
  findBareMetalInstanceTypeForReference,
  findDiskImageForReference,
  formatBareMetalCatalogDiskImage,
} from '../bareMetalCatalogItemResourceDisplay';
import CatalogFieldEditabilityLabel from '../CatalogFieldEditabilityLabel';
import {
  type FieldPolicyLike,
  catalogFieldPolicyBehavior,
  catalogFieldPolicyIsConfigured,
} from '../catalogFieldPolicyDisplay';
import type { CatalogItem } from '../catalogItemDisplay';
import type { CatalogItemResourceLookups } from '../catalogItemResourceLookups';
import {
  clusterCatalogItemFields,
  clusterNodeSetItemsFromPolicy,
  clusterNodeSetMapPolicy,
  clusterVersionReference,
  findClusterVersionForReference,
  formatClusterCatalogVersionLabel,
} from '../clusterCatalogItemResourceDisplay';
import {
  computeDiskImageReferenceFromPolicy,
  computeInstanceTypeReferenceFromPolicy,
  findComputeInstanceTypeForReference,
  formatComputeCatalogDiskImage,
} from '../computeCatalogItemResourceDisplay';

interface CatalogConfigurationRowProps {
  label: string;
  value: unknown;
  policy: unknown;
}

interface CatalogConfiguration {
  rows: ReactNode;
  hasConfiguredField: boolean;
}

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
  return value.name || value.id || undefined;
};

const policyDisplayValue = (value: unknown): string => {
  if (value && typeof value === 'object' && 'name' in value) {
    return referenceLabel(value) ?? '—';
  }
  return formatReviewScalar(value);
};

const CatalogConfigurationRow = ({ label, value, policy }: CatalogConfigurationRowProps) => {
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
          <FlexItem>{value === undefined ? t('Not set') : policyDisplayValue(value)}</FlexItem>
          <FlexItem>
            <CatalogFieldEditabilityLabel behavior={behavior} />
          </FlexItem>
        </Flex>
      </DescriptionListDescription>
    </DescriptionListGroup>
  );
};

const hasConfiguredPolicy = (...policies: unknown[]): boolean =>
  policies.some((policy) => catalogFieldPolicyIsConfigured(policy as FieldPolicyLike));

const formatDiskList = (value: unknown): string | undefined => {
  if (!value || typeof value !== 'object') {
    return undefined;
  }
  const items = (
    value as {
      items?: Array<{ sizeGib?: number; storageTier?: { id?: string; name?: string } }>;
    }
  ).items;
  if (!items?.length) {
    return undefined;
  }
  return items
    .map((item) => {
      const size = item.sizeGib === undefined ? '—' : `${item.sizeGib} GiB`;
      const tier = referenceLabel(item.storageTier);
      return tier ? `${size} (${tier})` : size;
    })
    .join(', ');
};

const formatNodeSets = (value: unknown): string | undefined => {
  if (!value || typeof value !== 'object') {
    return undefined;
  }
  const valueRecord = value as Record<string, unknown>;
  const items = (valueRecord.items ?? valueRecord) as Record<
    string,
    { size?: number; baremetalInstanceType?: unknown }
  >;
  if (!items) {
    return undefined;
  }
  const rows = Object.entries(items).map(([name, nodeSet]) => {
    const size = nodeSet.size === undefined ? '—' : nodeSet.size;
    const hostType = referenceLabel(nodeSet.baremetalInstanceType);
    return `${name}: ${size}${hostType ? ` (${hostType})` : ''}`;
  });
  return rows.length > 0 ? rows.join(', ') : undefined;
};

const computeConfiguration = (
  item: ComputeInstanceCatalogItem,
  resourceLookups: CatalogItemResourceLookups,
  t: TFunction,
): CatalogConfiguration => {
  const fields = item.fields;
  const instanceTypeReference = computeInstanceTypeReferenceFromPolicy(fields?.instanceType);
  const diskImageReference = computeDiskImageReferenceFromPolicy(fields?.diskImage);
  const instanceType = findComputeInstanceTypeForReference(
    resourceLookups.computeInstanceTypes,
    instanceTypeReference,
  );
  const diskImage = findDiskImageForReference(resourceLookups.diskImages, diskImageReference);
  const bootDisk = fields?.bootDisk;
  const bootDiskSize = policyValue(bootDisk?.sizeGib);
  const storageTier = policyValue(bootDisk?.storageTier);
  const additionalDisks = policyValue(fields?.additionalDisks);

  return {
    rows: (
      <>
        <CatalogConfigurationRow
          label={t('Instance type')}
          value={instanceType?.metadata?.name ?? referenceLabel(instanceTypeReference)}
          policy={fields?.instanceType}
        />
        <CatalogConfigurationRow
          label={t('Disk image')}
          value={formatComputeCatalogDiskImage(fields, diskImage, diskImageReference)}
          policy={fields?.diskImage}
        />
        <CatalogConfigurationRow
          label={t('User data')}
          value={policyValue(fields?.userData)}
          policy={fields?.userData}
        />
        <CatalogConfigurationRow
          label={t('Boot disk size')}
          value={typeof bootDiskSize === 'number' ? `${bootDiskSize} GiB` : undefined}
          policy={bootDisk?.sizeGib}
        />
        <CatalogConfigurationRow
          label={t('Boot disk storage tier')}
          value={storageTier}
          policy={bootDisk?.storageTier}
        />
        <CatalogConfigurationRow
          label={t('Additional disks')}
          value={formatDiskList(additionalDisks)}
          policy={fields?.additionalDisks}
        />
      </>
    ),
    hasConfiguredField: hasConfiguredPolicy(
      fields?.instanceType,
      fields?.diskImage,
      fields?.userData,
      bootDisk?.sizeGib,
      bootDisk?.storageTier,
      fields?.additionalDisks,
    ),
  };
};

const clusterConfiguration = (
  item: ClusterCatalogItem,
  resourceLookups: CatalogItemResourceLookups,
  t: TFunction,
): CatalogConfiguration => {
  const fields = clusterCatalogItemFields(item);
  const versionPolicy = fields?.version;
  const versionReference = clusterVersionReference(fields);
  const clusterVersion = findClusterVersionForReference(
    resourceLookups.clusterVersions,
    versionReference,
  );
  const nodeSetsPolicy = clusterNodeSetMapPolicy(fields);
  const nodeSets = formatNodeSets(clusterNodeSetItemsFromPolicy(nodeSetsPolicy));
  const network = fields?.network;

  return {
    rows: (
      <>
        <CatalogConfigurationRow
          label={t('Cluster version')}
          value={
            versionReference || clusterVersion
              ? formatClusterCatalogVersionLabel(clusterVersion, versionReference)
              : undefined
          }
          policy={versionPolicy}
        />
        <CatalogConfigurationRow label={t('Node sets')} value={nodeSets} policy={nodeSetsPolicy} />
        <CatalogConfigurationRow
          label={t('Pod CIDR')}
          value={policyValue(network?.podCidr)}
          policy={network?.podCidr}
        />
        <CatalogConfigurationRow
          label={t('Service CIDR')}
          value={policyValue(network?.serviceCidr)}
          policy={network?.serviceCidr}
        />
      </>
    ),
    hasConfiguredField: hasConfiguredPolicy(
      versionPolicy,
      nodeSetsPolicy,
      network?.podCidr,
      network?.serviceCidr,
    ),
  };
};

const bareMetalConfiguration = (
  item: BareMetalInstanceCatalogItem,
  resourceLookups: CatalogItemResourceLookups,
  t: TFunction,
): CatalogConfiguration => {
  const fields = bareMetalCatalogItemFields(item);
  const instanceTypeReference = bareMetalInstanceTypeReferenceFromPolicy(fields?.instanceType);
  const diskImageReference = bareMetalDiskImageReferenceFromPolicy(fields?.diskImage);
  const instanceType = findBareMetalInstanceTypeForReference(
    resourceLookups.bareMetalInstanceTypes,
    instanceTypeReference,
  );
  const diskImage = findDiskImageForReference(resourceLookups.diskImages, diskImageReference);

  return {
    rows: (
      <>
        <CatalogConfigurationRow
          label={t('Instance type')}
          value={instanceType?.metadata?.name ?? referenceLabel(instanceTypeReference)}
          policy={fields?.instanceType}
        />
        <CatalogConfigurationRow
          label={t('Disk image')}
          value={formatBareMetalCatalogDiskImage(fields, diskImage, diskImageReference)}
          policy={fields?.diskImage}
        />
        <CatalogConfigurationRow
          label={t('User data')}
          value={policyValue(fields?.userData)}
          policy={fields?.userData}
        />
      </>
    ),
    hasConfiguredField: hasConfiguredPolicy(
      fields?.instanceType,
      fields?.diskImage,
      fields?.userData,
    ),
  };
};

const CatalogItemConfiguration = ({
  item,
  resourceLookups,
}: {
  item: CatalogItem;
  resourceLookups: CatalogItemResourceLookups;
}) => {
  const { t } = useTranslation();
  let configuration: CatalogConfiguration | undefined;

  switch (item.$typeName) {
    case 'osac.public.v1.ComputeInstanceCatalogItem':
      configuration = computeConfiguration(item, resourceLookups, t);
      break;
    case 'osac.public.v1.ClusterCatalogItem':
      configuration = clusterConfiguration(item, resourceLookups, t);
      break;
    case 'osac.public.v1.BareMetalInstanceCatalogItem':
      configuration = bareMetalConfiguration(item, resourceLookups, t);
      break;
    default:
      configuration = undefined;
  }

  return configuration?.hasConfiguredField ? (
    <DescriptionList isHorizontal isCompact>
      {configuration.rows}
    </DescriptionList>
  ) : (
    <Content component="p">{t('No governed configuration')}</Content>
  );
};

export default CatalogItemConfiguration;
