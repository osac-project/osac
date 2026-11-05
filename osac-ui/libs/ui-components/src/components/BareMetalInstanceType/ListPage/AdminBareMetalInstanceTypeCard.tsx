import {
  CardBody,
  CardFooter,
  DescriptionList,
  DescriptionListDescription,
  DescriptionListGroup,
  DescriptionListTerm,
  Divider,
  Flex,
  FlexItem,
} from '@patternfly/react-core';
import { RhUiServerStackIcon } from '@patternfly/react-icons/dist/esm/icons/rh-ui-server-stack-icon';

import type { BareMetalInstanceType as PrivateBareMetalInstanceType } from '@osac/types/private';
import ListPageCard from '@osac/ui-components/components/Page/ListPageCard';
import { Timestamp } from '@osac/ui-components/components/Primitives/Timestamp';
import ResourceNameField from '@osac/ui-components/components/Resource/ResourceNameField.tsx';
import { useTranslation } from '@osac/ui-components/hooks/useTranslation';

import AdminBareMetalInstanceTypeActionsMenu from '../AdminBareMetalInstanceTypeActionsMenu';

const NAME_PREVIEW_LENGTH = 32;

interface AdminBareMetalInstanceTypeCardProps {
  bareMetalInstanceType: PrivateBareMetalInstanceType;
}

const AdminBareMetalInstanceTypeCard = ({
  bareMetalInstanceType,
}: AdminBareMetalInstanceTypeCardProps) => {
  const { t } = useTranslation();
  const cpu = bareMetalInstanceType.spec?.hardware?.cpu;
  const memory = bareMetalInstanceType.spec?.hardware?.memory;
  const acceleratorCount = bareMetalInstanceType.spec?.hardware?.accelerators.length ?? 0;
  const detailsUrl = `/admin/infrastructure/baremetal-instance-types/${bareMetalInstanceType.id}`;

  return (
    <ListPageCard
      title={
        <ResourceNameField
          resource={bareMetalInstanceType}
          detailsUrl={detailsUrl}
          maxCharsDisplayed={NAME_PREVIEW_LENGTH}
          subTitle={bareMetalInstanceType.spec?.description}
        />
      }
      icon={<RhUiServerStackIcon />}
      actionMenu={
        <AdminBareMetalInstanceTypeActionsMenu bareMetalInstanceType={bareMetalInstanceType} />
      }
    >
      <CardBody>
        <DescriptionList isCompact isHorizontal>
          <DescriptionListGroup>
            <DescriptionListTerm>{t('CPU')}</DescriptionListTerm>
            <DescriptionListDescription>
              {cpu ? `${cpu.cores} (${cpu.architecture})` : '—'}
            </DescriptionListDescription>
          </DescriptionListGroup>
          <DescriptionListGroup>
            <DescriptionListTerm>{t('Memory (GiB)')}</DescriptionListTerm>
            <DescriptionListDescription>
              {memory ? memory.totalGb.toString() : '—'}
            </DescriptionListDescription>
          </DescriptionListGroup>
          <DescriptionListGroup>
            <DescriptionListTerm>{t('Accelerators')}</DescriptionListTerm>
            <DescriptionListDescription>
              {acceleratorCount > 0 ? acceleratorCount : '—'}
            </DescriptionListDescription>
          </DescriptionListGroup>
        </DescriptionList>
      </CardBody>
      <Divider />
      <CardFooter>
        <Flex
          flexWrap={{ default: 'nowrap' }}
          gap={{ default: 'gapLg' }}
          justifyContent={{ default: 'justifyContentSpaceBetween' }}
        >
          <FlexItem>{t('Created')}</FlexItem>
          <FlexItem>
            <Timestamp value={bareMetalInstanceType.metadata?.creationTimestamp} />
          </FlexItem>
        </Flex>
      </CardFooter>
    </ListPageCard>
  );
};

export default AdminBareMetalInstanceTypeCard;
