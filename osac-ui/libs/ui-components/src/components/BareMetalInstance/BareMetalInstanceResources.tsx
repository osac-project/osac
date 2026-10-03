import { FC } from 'react';
import {
  DescriptionList,
  DescriptionListDescription,
  DescriptionListGroup,
  DescriptionListTerm,
  Flex,
  FlexItem,
} from '@patternfly/react-core';

import { type BareMetalInstance, BareMetalInstanceType } from '@osac/types';
import { useTranslation } from '@osac/ui-components/hooks/useTranslation';

import {
  formatBareMetalCpu,
  formatBareMetalGpu,
  formatBareMetalMemory,
  inferDiskImageOs,
} from './bareMetalInstanceDisplay';
import { GuestOsIcon } from '../shared/GuestOsIcon';

import './BareMetalInstanceCard.css';

interface BareMetalInstanceCardProps {
  instance: BareMetalInstance;
  instanceType?: BareMetalInstanceType;
}

const BareMetalInstanceCard: FC<BareMetalInstanceCardProps> = ({ instance, instanceType }) => {
  const { t } = useTranslation();

  const hardware = instanceType?.spec?.hardware;
  const diskImageName = instance.spec?.diskImage?.name;

  return (
    <DescriptionList isHorizontal isCompact className="bare-metal-instance-card__specs">
      <DescriptionListGroup>
        <DescriptionListTerm>{t('CPU')}</DescriptionListTerm>
        <DescriptionListDescription>{formatBareMetalCpu(hardware)}</DescriptionListDescription>
      </DescriptionListGroup>
      <DescriptionListGroup>
        <DescriptionListTerm>{t('RAM')}</DescriptionListTerm>
        <DescriptionListDescription>{formatBareMetalMemory(hardware)}</DescriptionListDescription>
      </DescriptionListGroup>
      <DescriptionListGroup>
        <DescriptionListTerm>{t('GPU')}</DescriptionListTerm>
        <DescriptionListDescription>{formatBareMetalGpu(hardware)}</DescriptionListDescription>
      </DescriptionListGroup>
      <DescriptionListGroup>
        <DescriptionListTerm>{t('OS image')}</DescriptionListTerm>
        <DescriptionListDescription>
          <Flex
            flexWrap={{ default: 'nowrap' }}
            gap={{ default: 'gapXs' }}
            alignItems={{ default: 'alignItemsCenter' }}
          >
            <FlexItem className="bare-metal-instance-card__disk-image">
              <GuestOsIcon os={inferDiskImageOs(diskImageName)} size="sm" />
            </FlexItem>
            <FlexItem>{diskImageName ?? '—'}</FlexItem>
          </Flex>
        </DescriptionListDescription>
      </DescriptionListGroup>
    </DescriptionList>
  );
};

export default BareMetalInstanceCard;
