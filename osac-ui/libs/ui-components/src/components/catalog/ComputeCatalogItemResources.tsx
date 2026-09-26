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
  formatComputeCatalogMemory,
  formatComputeCatalogStorage,
  formatComputeCatalogVCpu,
  int32ValueFromPolicy,
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
  const bootDiskSizePolicyBehavior = catalogFieldPolicyBehavior(fields?.bootDisk?.sizeGib);
  const diskImagePolicyBehavior = catalogFieldPolicyBehavior(fields?.diskImage);
  const bootDiskSizeGib = int32ValueFromPolicy(fields?.bootDisk?.sizeGib);

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

  const vCpuLabel = formatComputeCatalogVCpu(fields, instanceType, t);
  const memoryLabel = formatComputeCatalogMemory(fields, instanceType, t);
  const storageLabel = formatComputeCatalogStorage(fields, bootDiskSizeGib, t);
  const diskImageLabel = formatComputeCatalogDiskImage(fields, diskImage, diskImageReference);

  return (
    <DescriptionList isHorizontal isCompact>
      <DescriptionListGroup>
        <DescriptionListTerm>{t('vCPU')}</DescriptionListTerm>
        <DescriptionListDescription>
          <Flex flexWrap={{ default: 'nowrap' }} gap={{ default: 'gapXs' }}>
            <FlexItem>{vCpuLabel || '-'}</FlexItem>
            <FlexItem>
              <CatalogFieldEditabilityLabel behavior={instanceTypePolicyBehavior} />
            </FlexItem>
          </Flex>
        </DescriptionListDescription>
      </DescriptionListGroup>
      <DescriptionListGroup>
        <DescriptionListTerm>{t('Memory')}</DescriptionListTerm>
        <DescriptionListDescription>
          <Flex flexWrap={{ default: 'nowrap' }} gap={{ default: 'gapXs' }}>
            <FlexItem>{memoryLabel || '-'}</FlexItem>
            <FlexItem>
              <CatalogFieldEditabilityLabel behavior={instanceTypePolicyBehavior} />
            </FlexItem>
          </Flex>
        </DescriptionListDescription>
      </DescriptionListGroup>
      <DescriptionListGroup>
        <DescriptionListTerm>{t('Storage')}</DescriptionListTerm>
        <DescriptionListDescription>
          <Flex flexWrap={{ default: 'nowrap' }} gap={{ default: 'gapXs' }}>
            <FlexItem>{storageLabel || '-'}</FlexItem>
            <FlexItem>
              <CatalogFieldEditabilityLabel behavior={bootDiskSizePolicyBehavior} />
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
