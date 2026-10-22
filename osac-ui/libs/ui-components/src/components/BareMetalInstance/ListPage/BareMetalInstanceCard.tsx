import {
  DescriptionList,
  DescriptionListDescription,
  DescriptionListGroup,
  DescriptionListTerm,
  Flex,
  FlexItem,
} from '@patternfly/react-core';

import { type BareMetalInstance, BareMetalInstanceType } from '@osac/types';
import InstanceCard from '@osac/ui-components/components/InstanceCard/InstanceCard';
import { Timestamp } from '@osac/ui-components/components/Primitives/Timestamp';
import ResourceNameField from '@osac/ui-components/components/Resource/ResourceNameField';
import { useTranslation } from '@osac/ui-components/hooks/useTranslation';

import { BareMetalActionsMenu } from './BareMetalActionsMenu';
import { BareMetalStatusLabel } from '../BareMetalStatusLabel';

interface BareMetalInstanceCardProps {
  instance: BareMetalInstance;
  instanceTypes?: BareMetalInstanceType[];
}

const BareMetalInstanceCard = ({ instance }: BareMetalInstanceCardProps) => {
  const { t } = useTranslation();

  return (
    <>
      <InstanceCard
        kind="bm"
        statusLabel={<BareMetalStatusLabel state={instance.status?.state} />}
        actionMenu={<BareMetalActionsMenu instance={instance} />}
        title={
          <ResourceNameField
            resource={instance}
            detailsUrl={`/bare-metal/${instance.id}`}
            subTitle={instance.spec?.catalogItem?.name}
          />
        }
        body={
          <DescriptionList isHorizontal isCompact>
            <DescriptionListGroup>
              <DescriptionListTerm>{t('Instance type')}</DescriptionListTerm>
              <DescriptionListDescription>
                {instance.spec?.instanceType?.name || '-'}
              </DescriptionListDescription>
            </DescriptionListGroup>
            <DescriptionListGroup>
              <DescriptionListTerm>{t('Disk image')}</DescriptionListTerm>
              <DescriptionListDescription>
                {instance.spec?.diskImage?.name || '—'}
              </DescriptionListDescription>
            </DescriptionListGroup>
          </DescriptionList>
        }
        footer={
          <Flex direction={{ default: 'column' }} gap={{ default: 'gapSm' }}>
            <FlexItem>
              <Flex
                flexWrap={{ default: 'nowrap' }}
                gap={{ default: 'gapLg' }}
                justifyContent={{ default: 'justifyContentSpaceBetween' }}
              >
                <FlexItem>{t('Project')}</FlexItem>
                <FlexItem>{instance.metadata?.project || t('Default')}</FlexItem>
              </Flex>
            </FlexItem>
            <FlexItem>
              <Flex
                flexWrap={{ default: 'nowrap' }}
                gap={{ default: 'gapLg' }}
                justifyContent={{ default: 'justifyContentSpaceBetween' }}
              >
                <FlexItem>{t('Created')}</FlexItem>
                <FlexItem>
                  <Timestamp value={instance.metadata?.creationTimestamp} />
                </FlexItem>
              </Flex>
            </FlexItem>
          </Flex>
        }
      />
    </>
  );
};

export default BareMetalInstanceCard;
