import {
  CardBody,
  CardFooter,
  DescriptionList,
  DescriptionListDescription,
  DescriptionListGroup,
  DescriptionListTerm,
  Divider,
} from '@patternfly/react-core';
import { RhUiConnectedIcon } from '@patternfly/react-icons/dist/esm/icons/rh-ui-connected-icon';

import type { ExternalIPPool } from '@osac/types/private';
import ListPageCard from '@osac/ui-components/components/Page/ListPageCard';
import OsacCardFooterRow from '@osac/ui-components/components/Primitives/OsacCardFooterRow';
import { Timestamp } from '@osac/ui-components/components/Primitives/Timestamp';

import ExternalIpPoolActionsMenu from './ExternalIpPoolActionsMenu';
import ExternalIpPoolStatusLabel from './ExternalIpPoolStatusLabel';
import { formatExternalIpPoolCidrs, getPoolCapacity, getPoolTenant } from './utils';
import { externalIpPoolDetailsPath } from './values';
import { useTranslation } from '../../hooks/useTranslation';
import ResourceNameField from '../Resource/ResourceNameField';

interface ExternalIpPoolCardProps {
  pool: ExternalIPPool;
}

const ExternalIpPoolCard = ({ pool }: ExternalIpPoolCardProps) => {
  const { t } = useTranslation();

  return (
    <ListPageCard
      title={<ResourceNameField resource={pool} detailsUrl={externalIpPoolDetailsPath(pool.id)} />}
      icon={<RhUiConnectedIcon />}
      statusLabel={<ExternalIpPoolStatusLabel state={pool.status?.state} />}
      actionMenu={<ExternalIpPoolActionsMenu pool={pool} />}
    >
      <CardBody>
        <DescriptionList isCompact isHorizontal>
          <DescriptionListGroup>
            <DescriptionListTerm>{t('Tenant')}</DescriptionListTerm>
            <DescriptionListDescription>{getPoolTenant(pool, t)}</DescriptionListDescription>
          </DescriptionListGroup>
          <DescriptionListGroup>
            <DescriptionListTerm>{t('Capacity')}</DescriptionListTerm>
            <DescriptionListDescription>{getPoolCapacity(pool, t)}</DescriptionListDescription>
          </DescriptionListGroup>
          <DescriptionListGroup>
            <DescriptionListTerm>{t('CIDRs')}</DescriptionListTerm>
            <DescriptionListDescription>
              {formatExternalIpPoolCidrs(t, pool.spec?.cidrs)}
            </DescriptionListDescription>
          </DescriptionListGroup>
        </DescriptionList>
      </CardBody>
      <Divider />
      <CardFooter>
        <OsacCardFooterRow
          term={t('Created')}
          description={<Timestamp value={pool.metadata?.creationTimestamp} />}
        />
      </CardFooter>
    </ListPageCard>
  );
};

export default ExternalIpPoolCard;
