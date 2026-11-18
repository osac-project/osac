import {
  CardBody,
  Content,
  ContentVariants,
  DescriptionList,
  DescriptionListDescription,
  DescriptionListGroup,
  DescriptionListTerm,
} from '@patternfly/react-core';

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
  const tenantName = tenant.metadata?.name ?? tenant.id;

  return (
    <ListPageCard
      title={<ResourceNameField resource={tenant} title={tenantName} />}
      icon={<Content component={ContentVariants.h1}>{tenantName[0].toUpperCase()}</Content>}
      statusLabel={<TenantStatusLabel state={tenant.status?.state} />}
      actionMenu={<TenantActionsMenu tenant={tenant} />}
    >
      <CardBody>
        <DescriptionList isCompact isHorizontal>
          <DescriptionListGroup>
            <DescriptionListTerm>{t('Domain')}</DescriptionListTerm>
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
