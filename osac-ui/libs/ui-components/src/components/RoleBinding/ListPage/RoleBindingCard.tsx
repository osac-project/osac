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
import { RhUiUsersIcon } from '@patternfly/react-icons/dist/esm/icons/rh-ui-users-icon';

import type { RoleBinding } from '@osac/types';
import ListPageCard from '@osac/ui-components/components/Page/ListPageCard';
import { Timestamp } from '@osac/ui-components/components/Primitives/Timestamp';
import ResourceNameField from '@osac/ui-components/components/Resource/ResourceNameField.tsx';
import { useTranslation } from '@osac/ui-components/hooks/useTranslation';

import RoleBindingActionsMenu from '../RoleBindingActionsMenu';
import RoleBindingStatusLabel from '../RoleBindingStatusLabel';

const ROLE_BINDING_NAME_PREVIEW_LENGTH = 32;

interface RoleBindingCardProps {
  roleBinding: RoleBinding;
  roleTitle: string;
}

const RoleBindingCard = ({ roleBinding, roleTitle }: RoleBindingCardProps) => {
  const { t } = useTranslation();
  const userCount = roleBinding.spec?.users.length ?? 0;

  return (
    <ListPageCard
      title={
        <ResourceNameField
          resource={roleBinding}
          maxCharsDisplayed={ROLE_BINDING_NAME_PREVIEW_LENGTH}
        />
      }
      icon={<RhUiUsersIcon />}
      statusLabel={<RoleBindingStatusLabel rb={roleBinding} />}
      actionMenu={<RoleBindingActionsMenu roleBinding={roleBinding} />}
    >
      <CardBody>
        <DescriptionList isCompact isHorizontal>
          <DescriptionListGroup>
            <DescriptionListTerm>{t('Role')}</DescriptionListTerm>
            <DescriptionListDescription>{roleTitle}</DescriptionListDescription>
          </DescriptionListGroup>
          <DescriptionListGroup>
            <DescriptionListTerm>{t('Users')}</DescriptionListTerm>
            <DescriptionListDescription>
              {t('{{count}} user', { count: userCount })}
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
            <Timestamp value={roleBinding.metadata?.creationTimestamp} />
          </FlexItem>
        </Flex>
      </CardFooter>{' '}
    </ListPageCard>
  );
};

export default RoleBindingCard;
