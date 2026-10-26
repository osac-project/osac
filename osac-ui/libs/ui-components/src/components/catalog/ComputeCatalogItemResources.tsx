import { useMemo } from 'react';
import {
  DescriptionList,
  DescriptionListDescription,
  DescriptionListGroup,
  DescriptionListTerm,
  Flex,
  FlexItem,
} from '@patternfly/react-core';

import { ComputeInstanceCatalogItem } from '@osac/types';
import { useTranslation } from '@osac/ui-components/hooks/useTranslation';

import {
  diskImageGuestOsIconType,
  findDiskImageForReference,
} from './bareMetalCatalogItemResourceDisplay';
import CatalogFieldEditabilityLabel from './CatalogFieldEditabilityLabel';
import { catalogFieldPolicyBehavior } from './catalogFieldPolicyDisplay';
import type { CatalogItemResourceLookups } from './catalogItemResourceLookups';
import {
  computeCatalogItemFields,
  computeDiskImageReferenceFromPolicy,
  computeInstanceTypeReferenceFromPolicy,
  findComputeInstanceTypeForReference,
  formatComputeCatalogDiskImage,
} from './computeCatalogItemResourceDisplay';
import { GuestOsIcon } from '../shared/GuestOsIcon';

interface ComputeCatalogItemResourcesProps {
  catalogItem: ComputeInstanceCatalogItem;
  resourceLookups: CatalogItemResourceLookups;
}

const ComputeCatalogItemResources = ({
  catalogItem,
  resourceLookups,
}: ComputeCatalogItemResourcesProps) => {
  const { t } = useTranslation();
  const fields = computeCatalogItemFields(catalogItem);
  const instanceTypeReference = computeInstanceTypeReferenceFromPolicy(fields?.instanceType);
  const diskImageReference = computeDiskImageReferenceFromPolicy(fields?.diskImage);
  const instanceTypePolicyBehavior = catalogFieldPolicyBehavior(fields?.instanceType);
  const diskImagePolicyBehavior = catalogFieldPolicyBehavior(fields?.diskImage);

  const { computeInstanceTypes, diskImages } = resourceLookups;

  const instanceType = useMemo(
    () => findComputeInstanceTypeForReference(computeInstanceTypes, instanceTypeReference),
    [instanceTypeReference, computeInstanceTypes],
  );

  const diskImage = useMemo(
    () => findDiskImageForReference(diskImages, diskImageReference),
    [diskImageReference, diskImages],
  );

  const diskImageOsIcon = diskImageGuestOsIconType(diskImage);

  const instanceTypeLabel =
    instanceType?.metadata?.name ?? instanceTypeReference?.name ?? instanceTypeReference?.id;
  const diskImageLabel = formatComputeCatalogDiskImage(fields, diskImage, diskImageReference);

  return (
    <DescriptionList isHorizontal isCompact>
      <DescriptionListGroup>
        <DescriptionListTerm>{t('Instance type')}</DescriptionListTerm>
        <DescriptionListDescription>
          <Flex flexWrap={{ default: 'nowrap' }} gap={{ default: 'gapXs' }}>
            <FlexItem>{instanceTypeLabel || '-'}</FlexItem>
            <FlexItem>
              <CatalogFieldEditabilityLabel behavior={instanceTypePolicyBehavior} />
            </FlexItem>
          </Flex>
        </DescriptionListDescription>
      </DescriptionListGroup>
      <DescriptionListGroup>
        <DescriptionListTerm>{t('Disk image')}</DescriptionListTerm>
        <DescriptionListDescription>
          <Flex
            flexWrap={{ default: 'nowrap' }}
            gap={{ default: 'gapXs' }}
            alignItems={{ default: 'alignItemsCenter' }}
          >
            {diskImageOsIcon ? (
              <FlexItem>
                <GuestOsIcon os={diskImageOsIcon} size="sm" />
              </FlexItem>
            ) : null}
            <FlexItem>{diskImageLabel || '-'}</FlexItem>
            <FlexItem>
              <CatalogFieldEditabilityLabel behavior={diskImagePolicyBehavior} />
            </FlexItem>
          </Flex>
        </DescriptionListDescription>
      </DescriptionListGroup>
    </DescriptionList>
  );
};

export default ComputeCatalogItemResources;
