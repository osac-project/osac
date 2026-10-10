import { useMemo } from 'react';
import {
  DescriptionList,
  DescriptionListDescription,
  DescriptionListGroup,
  DescriptionListTerm,
  Flex,
  FlexItem,
} from '@patternfly/react-core';

import { ClusterCatalogItem } from '@osac/types';
import { useTranslation } from '@osac/ui-components/hooks/useTranslation';

import CatalogFieldEditabilityLabel from './CatalogFieldEditabilityLabel';
import { catalogFieldPolicyBehavior } from './catalogFieldPolicyDisplay';
import type { CatalogItemResourceLookups } from './catalogItemResourceLookups';
import {
  clusterCatalogItemFields,
  clusterNodeSetItemsFromPolicy,
  clusterNodeSetMapPolicy,
  clusterVersionReference,
  findBareMetalInstanceTypeForReference,
  findClusterVersionForReference,
  formatClusterCatalogBareMetalInstanceTypeRow,
  formatClusterCatalogNodeSetRow,
  formatClusterCatalogVersionRow,
  primaryClusterCatalogNodeSet,
} from './clusterCatalogItemResourceDisplay';
import { GuestOsIcon } from '../shared/GuestOsIcon';

interface ClusterCatalogItemResourcesProps {
  catalogItem: ClusterCatalogItem;
  resourceLookups: CatalogItemResourceLookups;
}

const ClusterCatalogItemResources = ({
  catalogItem,
  resourceLookups,
}: ClusterCatalogItemResourcesProps) => {
  const { t } = useTranslation();
  const fields = clusterCatalogItemFields(catalogItem);
  const versionReference = clusterVersionReference(fields);
  const nodeSetsPolicy = clusterNodeSetMapPolicy(fields);
  const nodeSetItems = clusterNodeSetItemsFromPolicy(nodeSetsPolicy);
  const primaryNodeSet = primaryClusterCatalogNodeSet(nodeSetItems);
  const versionPolicyBehavior = catalogFieldPolicyBehavior(fields?.version);
  const nodeSetsPolicyBehavior = catalogFieldPolicyBehavior(nodeSetsPolicy);

  const { clusterVersions, bareMetalInstanceTypes } = resourceLookups;

  const clusterVersion = useMemo(
    () => findClusterVersionForReference(clusterVersions, versionReference),
    [clusterVersions, versionReference],
  );

  const instanceType = useMemo(
    () =>
      findBareMetalInstanceTypeForReference(
        bareMetalInstanceTypes,
        primaryNodeSet?.nodeSet.baremetalInstanceType,
      ),
    [bareMetalInstanceTypes, primaryNodeSet?.nodeSet.baremetalInstanceType],
  );

  const versionLabel = formatClusterCatalogVersionRow(fields, clusterVersion, versionReference);
  const showClusterVersionIcon = Boolean(versionReference || clusterVersion);
  const nodeSetLabel = formatClusterCatalogNodeSetRow(nodeSetsPolicy, primaryNodeSet);
  const instanceTypeLabel = formatClusterCatalogBareMetalInstanceTypeRow(
    nodeSetsPolicy,
    primaryNodeSet,
    instanceType,
  );

  return (
    <DescriptionList isHorizontal isCompact>
      <DescriptionListGroup>
        <DescriptionListTerm>{t('Cluster version')}</DescriptionListTerm>
        <DescriptionListDescription>
          <Flex
            flexWrap={{ default: 'nowrap' }}
            gap={{ default: 'gapXs' }}
            alignItems={{ default: 'alignItemsCenter' }}
          >
            {showClusterVersionIcon ? (
              <FlexItem>
                <GuestOsIcon os="rhel" size="sm" />
              </FlexItem>
            ) : null}
            <FlexItem>{versionLabel || '-'}</FlexItem>
            <FlexItem>
              <CatalogFieldEditabilityLabel behavior={versionPolicyBehavior} />
            </FlexItem>
          </Flex>
        </DescriptionListDescription>
      </DescriptionListGroup>
      <DescriptionListGroup>
        <DescriptionListTerm>{t('Node set')}</DescriptionListTerm>
        <DescriptionListDescription>
          <Flex flexWrap={{ default: 'nowrap' }} gap={{ default: 'gapXs' }}>
            <FlexItem>{nodeSetLabel || '-'}</FlexItem>
            <FlexItem>
              <CatalogFieldEditabilityLabel behavior={nodeSetsPolicyBehavior} />
            </FlexItem>
          </Flex>
        </DescriptionListDescription>
      </DescriptionListGroup>
      <DescriptionListGroup>
        <DescriptionListTerm>{t('Bare-metal instance type')}</DescriptionListTerm>
        <DescriptionListDescription>
          <Flex flexWrap={{ default: 'nowrap' }} gap={{ default: 'gapXs' }}>
            <FlexItem>{instanceTypeLabel || '-'}</FlexItem>
            <FlexItem>
              <CatalogFieldEditabilityLabel behavior={nodeSetsPolicyBehavior} />
            </FlexItem>
          </Flex>
        </DescriptionListDescription>
      </DescriptionListGroup>
    </DescriptionList>
  );
};

export default ClusterCatalogItemResources;
