import {
  CardBody,
  DescriptionList,
  DescriptionListDescription,
  DescriptionListGroup,
  DescriptionListTerm,
} from '@patternfly/react-core';
import UsersIcon from '@patternfly/react-icons/dist/esm/icons/users-icon';

import type { Tenant } from '@osac/types/private';
import ListPageCard from '@osac/ui-components/components/Page/ListPageCard';

import TenantActionsMenu from './TenantActionsMenu';
import TenantStatusLabel from './TenantStatusLabel';
import { useTranslation } from '../../hooks/useTranslation';
import { Timestamp } from '../Primitives/Timestamp';
import ResourceNameField from '../Resource/ResourceNameField';

interface TenantCardProps {
  tenant: Tenant;
}

const TenantCard = ({ tenant }: TenantCardProps) => {
  const { t } = useTranslation();

  return (
    <ListPageCard
      title={<ResourceNameField resource={tenant} title={tenant.metadata?.name ?? tenant.id} />}
      icon={<UsersIcon />}
      statusLabel={<TenantStatusLabel state={tenant.status?.state} />}
      actionMenu={<TenantActionsMenu tenant={tenant} />}
    >
      <CardBody>
        <DescriptionList isCompact isHorizontal>
          <DescriptionListGroup>
            <DescriptionListTerm>{t('Primary domain')}</DescriptionListTerm>
            <DescriptionListDescription>
              {tenant.spec?.domains?.[0] ?? '—'}
            </DescriptionListDescription>
          </DescriptionListGroup>
          <DescriptionListGroup>
            <DescriptionListTerm>{t('Registered')}</DescriptionListTerm>
            <DescriptionListDescription>
              <Timestamp value={tenant.metadata?.creationTimestamp} />
            </DescriptionListDescription>
          </DescriptionListGroup>
        </DescriptionList>
      </CardBody>
    </ListPageCard>
  );
};

export default TenantCard;
