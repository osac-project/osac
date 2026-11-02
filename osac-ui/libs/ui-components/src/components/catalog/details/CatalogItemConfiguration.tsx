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

import type { BareMetalInstanceCatalogItem, ClusterCatalogItem } from '@osac/types';

import { useTranslation } from '../../../hooks/useTranslation';
import { formatReviewScalar } from '../../ProvisioningWizard/reviewFormatters';
import {
  bareMetalCatalogItemFields,
  bareMetalDiskImageReferenceFromPolicy,
  bareMetalInstanceTypeReferenceFromPolicy,
  formatBareMetalCatalogDiskImage,
} from '../bareMetalCatalogItemResourceDisplay';
import CatalogFieldEditabilityLabel from '../CatalogFieldEditabilityLabel';
import {
  type FieldPolicyLike,
  catalogFieldPolicyBehavior,
  catalogFieldPolicyIsConfigured,
  catalogFieldPolicyValue,
} from '../catalogFieldPolicyDisplay';
import type { CatalogItem } from '../catalogItemDisplay';
import {
  clusterCatalogItemFields,
  clusterNodeSetMapPolicy,
  clusterVersionReference,
  formatClusterCatalogVersionLabel,
} from '../clusterCatalogItemResourceDisplay';
import CatalogNodeSets from './CatalogNodeSets';
import CatalogUserDataDetailValue from './CatalogUserDataDetailValue';

interface CatalogConfigurationRowProps {
  label: string;
  value?: string;
  policy: unknown;
  valueContent?: ReactNode;
}

interface CatalogConfiguration {
  rows: ReactNode;
  hasConfiguredField: boolean;
}

const CatalogConfigurationRow = ({
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

const hasConfiguredPolicy = (...policies: unknown[]): boolean =>
  policies.some((policy) => catalogFieldPolicyIsConfigured(policy as FieldPolicyLike));

const clusterConfiguration = (item: ClusterCatalogItem, t: TFunction): CatalogConfiguration => {
  const fields = clusterCatalogItemFields(item);
  const versionPolicy = fields?.version;
  const versionReference = clusterVersionReference(fields);
  const nodeSetsPolicy = clusterNodeSetMapPolicy(fields);
  const network = fields?.network;

  return {
    rows: (
      <>
        <CatalogConfigurationRow
          label={t('Cluster version')}
          value={
            versionReference
              ? formatReviewScalar(formatClusterCatalogVersionLabel(undefined, versionReference))
              : undefined
          }
          policy={versionPolicy}
        />
        <CatalogNodeSets policy={nodeSetsPolicy} />
        <CatalogConfigurationRow
          label={t('Pod CIDR')}
          value={formatReviewScalar(catalogFieldPolicyValue(network?.podCidr))}
          policy={network?.podCidr}
        />
        <CatalogConfigurationRow
          label={t('Service CIDR')}
          value={formatReviewScalar(catalogFieldPolicyValue(network?.serviceCidr))}
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
  t: TFunction,
): CatalogConfiguration => {
  const fields = bareMetalCatalogItemFields(item);
  const instanceTypeReference = bareMetalInstanceTypeReferenceFromPolicy(fields?.instanceType);
  const diskImageReference = bareMetalDiskImageReferenceFromPolicy(fields?.diskImage);

  return {
    rows: (
      <>
        <CatalogConfigurationRow
          label={t('Instance type')}
          value={instanceTypeReference?.name || undefined}
          policy={fields?.instanceType}
        />
        <CatalogConfigurationRow
          label={t('Disk image')}
          value={formatBareMetalCatalogDiskImage(fields, undefined, diskImageReference)}
          policy={fields?.diskImage}
        />
        <CatalogConfigurationRow
          label={t('User data')}
          valueContent={<CatalogUserDataDetailValue policy={fields?.userDataSource} />}
          policy={fields?.userDataSource}
        />
      </>
    ),
    hasConfiguredField: hasConfiguredPolicy(
      fields?.instanceType,
      fields?.diskImage,
      fields?.userDataSource,
    ),
  };
};

const CatalogItemConfiguration = ({ item }: { item: CatalogItem }) => {
  const { t } = useTranslation();
  let configuration: CatalogConfiguration | undefined;

  switch (item.$typeName) {
    case 'osac.public.v1.ClusterCatalogItem':
      configuration = clusterConfiguration(item, t);
      break;
    case 'osac.public.v1.BareMetalInstanceCatalogItem':
      configuration = bareMetalConfiguration(item, t);
      break;
    default:
      configuration = undefined;
  }

  return configuration?.hasConfiguredField ? (
    <DescriptionList isCompact>{configuration.rows}</DescriptionList>
  ) : (
    <Content component="p">{t('No governed configuration')}</Content>
  );
};

export default CatalogItemConfiguration;
