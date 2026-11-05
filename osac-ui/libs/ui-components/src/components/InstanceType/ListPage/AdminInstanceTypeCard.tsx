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
import { RhUiVirtualServerIcon } from '@patternfly/react-icons/dist/esm/icons/rh-ui-virtual-server-icon';

import type { InstanceType as PrivateInstanceType } from '@osac/types/private';
import ListPageCard from '@osac/ui-components/components/Page/ListPageCard';
import { Timestamp } from '@osac/ui-components/components/Primitives/Timestamp';
import ResourceNameField from '@osac/ui-components/components/Resource/ResourceNameField.tsx';
import { useTranslation } from '@osac/ui-components/hooks/useTranslation';

import AdminInstanceTypeActionsMenu from '../AdminInstanceTypeActionsMenu';
import InstanceTypeLifecycleLabel from '../InstanceTypeLifecycleLabel';

const INSTANCE_TYPE_NAME_PREVIEW_LENGTH = 32;

interface AdminInstanceTypeCardProps {
  instanceType: PrivateInstanceType;
}

const AdminInstanceTypeCard = ({ instanceType }: AdminInstanceTypeCardProps) => {
  const { t } = useTranslation();
  const gpu = instanceType.spec?.gpu;
  const detailsUrl = `/admin/infrastructure/instance-types/${instanceType.id}`;

  return (
    <ListPageCard
      title={
        <ResourceNameField
          resource={instanceType}
          detailsUrl={detailsUrl}
          maxCharsDisplayed={INSTANCE_TYPE_NAME_PREVIEW_LENGTH}
          subTitle={instanceType.spec?.description}
        />
      }
      icon={<RhUiVirtualServerIcon />}
      statusLabel={<InstanceTypeLifecycleLabel state={instanceType.spec?.state} />}
      actionMenu={<AdminInstanceTypeActionsMenu instanceType={instanceType} />}
    >
      <CardBody>
        <DescriptionList isCompact isHorizontal>
          <DescriptionListGroup>
            <DescriptionListTerm>{t('vCPUs')}</DescriptionListTerm>
            <DescriptionListDescription>
              {instanceType.spec?.vcpus ?? '—'}
            </DescriptionListDescription>
          </DescriptionListGroup>
          <DescriptionListGroup>
            <DescriptionListTerm>{t('Memory (GiB)')}</DescriptionListTerm>
            <DescriptionListDescription>
              {instanceType.spec?.memoryGib ?? '—'}
            </DescriptionListDescription>
          </DescriptionListGroup>
          <DescriptionListGroup>
            <DescriptionListTerm>{t('GPUs')}</DescriptionListTerm>
            <DescriptionListDescription>{gpu?.count ?? '—'}</DescriptionListDescription>
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
            <Timestamp value={instanceType.metadata?.creationTimestamp} />
          </FlexItem>
        </Flex>
      </CardFooter>
    </ListPageCard>
  );
};

export default AdminInstanceTypeCard;
