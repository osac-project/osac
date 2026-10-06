import { useMemo } from 'react';
import {
  DescriptionList,
  DescriptionListDescription,
  DescriptionListGroup,
  DescriptionListTerm,
  Flex,
  FlexItem,
} from '@patternfly/react-core';

import { BareMetalInstanceCatalogItem } from '@osac/types';
import { useTranslation } from '@osac/ui-components/hooks/useTranslation';

import {
  bareMetalCatalogItemFields,
  bareMetalDiskImageReferenceFromPolicy,
  bareMetalInstanceTypeReferenceFromPolicy,
  diskImageGuestOsIconType,
  findBareMetalInstanceTypeForReference,
  findDiskImageForReference,
  formatBareMetalCatalogCpu,
  formatBareMetalCatalogDiskImage,
  formatBareMetalCatalogGpu,
  formatBareMetalCatalogRam,
} from './bareMetalCatalogItemResourceDisplay';
import CatalogFieldEditabilityLabel from './CatalogFieldEditabilityLabel';
import { catalogFieldPolicyBehavior } from './catalogFieldPolicyDisplay';
import type { CatalogItemResourceLookups } from './catalogItemResourceLookups';
import { GuestOsIcon } from '../shared/GuestOsIcon';

interface BareMetalCatalogItemResourcesProps {
  catalogItem: BareMetalInstanceCatalogItem;
  resourceLookups: CatalogItemResourceLookups;
}

const BareMetalCatalogItemResources = ({
  catalogItem,
  resourceLookups,
}: BareMetalCatalogItemResourcesProps) => {
  const { t } = useTranslation();
  const fields = bareMetalCatalogItemFields(catalogItem);
  const instanceTypeReference = bareMetalInstanceTypeReferenceFromPolicy(fields?.instanceType);
  const diskImageReference = bareMetalDiskImageReferenceFromPolicy(fields?.diskImage);
  const diskImagePolicyBehavior = catalogFieldPolicyBehavior(fields?.diskImage);

  const { bareMetalInstanceTypes, diskImages } = resourceLookups;

  const instanceType = useMemo(
    () => findBareMetalInstanceTypeForReference(bareMetalInstanceTypes, instanceTypeReference),
    [instanceTypeReference, bareMetalInstanceTypes],
  );

  const diskImage = useMemo(
    () => findDiskImageForReference(diskImages, diskImageReference),
    [diskImageReference, diskImages],
  );

  const cpuLabel = formatBareMetalCatalogCpu(fields, instanceType, t);
  const ramLabel = formatBareMetalCatalogRam(fields, instanceType, t);
  const gpuLabel = formatBareMetalCatalogGpu(fields, instanceType, t);
  const diskImageLabel = formatBareMetalCatalogDiskImage(fields, diskImage, diskImageReference);
  const diskImageOsIcon = diskImageGuestOsIconType(diskImage);

  return (
    <DescriptionList isHorizontal isCompact>
      <DescriptionListGroup>
        <DescriptionListTerm>{t('CPU')}</DescriptionListTerm>
        <DescriptionListDescription>{cpuLabel || '-'}</DescriptionListDescription>
      </DescriptionListGroup>
      <DescriptionListGroup>
        <DescriptionListTerm>{t('RAM')}</DescriptionListTerm>
        <DescriptionListDescription>{ramLabel || '-'}</DescriptionListDescription>
      </DescriptionListGroup>
      <DescriptionListGroup>
        <DescriptionListTerm>{t('GPU')}</DescriptionListTerm>
        <DescriptionListDescription>{gpuLabel || '-'}</DescriptionListDescription>
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

export default BareMetalCatalogItemResources;
