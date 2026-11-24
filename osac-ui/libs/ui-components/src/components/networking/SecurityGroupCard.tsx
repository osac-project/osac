import { Link } from 'react-router-dom';
import {
  CardBody,
  CardFooter,
  DescriptionList,
  DescriptionListDescription,
  DescriptionListGroup,
  DescriptionListTerm,
  Divider,
} from '@patternfly/react-core';
import { RhUiLockFillIcon } from '@patternfly/react-icons/dist/esm/icons/rh-ui-lock-fill-icon';

import type { SecurityGroup, VirtualNetwork } from '@osac/types';
import { resourceDisplayName } from '@osac/ui-components/api/v1/networking';
import ListPageCard from '@osac/ui-components/components/Page/ListPageCard';
import OsacCardFooterRow from '@osac/ui-components/components/Primitives/OsacCardFooterRow';
import { Timestamp } from '@osac/ui-components/components/Primitives/Timestamp';
import ResourceNameField from '@osac/ui-components/components/Resource/ResourceNameField.tsx';

import { securityGroupDetailsPath } from './security-group-list-utils';
import SecurityGroupActionsMenu from './SecurityGroupActionsMenu';
import { SecurityGroupStatusLabel } from './SecurityGroupStatusLabel';
import { useTranslation } from '../../hooks/useTranslation';

interface SecurityGroupCardProps {
  securityGroup: SecurityGroup;
  virtualNetworksById: ReadonlyMap<string, VirtualNetwork>;
}

const SecurityGroupCard = ({ securityGroup, virtualNetworksById }: SecurityGroupCardProps) => {
  const { t } = useTranslation();

  const virtualNetworkId = securityGroup.spec?.virtualNetwork?.id ?? '';
  const virtualNetwork = virtualNetworkId ? virtualNetworksById.get(virtualNetworkId) : undefined;
  const virtualNetworkName = resourceDisplayName(virtualNetwork?.metadata, virtualNetworkId);
  const ingressCount = securityGroup.spec?.ingress?.length ?? 0;
  const egressCount = securityGroup.spec?.egress?.length ?? 0;

  return (
    <ListPageCard
      title={
        <ResourceNameField
          resource={securityGroup}
          detailsUrl={securityGroupDetailsPath(securityGroup.id)}
        />
      }
      icon={<RhUiLockFillIcon />}
      statusLabel={<SecurityGroupStatusLabel state={securityGroup.status?.state} />}
      actionMenu={<SecurityGroupActionsMenu securityGroup={securityGroup} />}
    >
      <CardBody>
        <DescriptionList isCompact isHorizontal>
          <DescriptionListGroup>
            <DescriptionListTerm>{t('Project')}</DescriptionListTerm>
            <DescriptionListDescription>
              {securityGroup.metadata?.project || t('Default')}
            </DescriptionListDescription>
          </DescriptionListGroup>
          <DescriptionListGroup>
            <DescriptionListTerm>{t('Virtual Network')}</DescriptionListTerm>
            <DescriptionListDescription>
              {virtualNetworkId ? (
                <Link to={`/networking/virtual-networks/${virtualNetworkId}`}>
                  {virtualNetworkName}
                </Link>
              ) : (
                virtualNetworkName
              )}
            </DescriptionListDescription>
          </DescriptionListGroup>
          <DescriptionListGroup>
            <DescriptionListTerm>{t('Inbound Rules')}</DescriptionListTerm>
            <DescriptionListDescription>{ingressCount}</DescriptionListDescription>
          </DescriptionListGroup>
          <DescriptionListGroup>
            <DescriptionListTerm>{t('Outbound Rules')}</DescriptionListTerm>
            <DescriptionListDescription>{egressCount}</DescriptionListDescription>
          </DescriptionListGroup>
        </DescriptionList>
      </CardBody>
      <Divider />
      <CardFooter>
        <OsacCardFooterRow
          term={t('Created')}
          description={<Timestamp value={securityGroup.metadata?.creationTimestamp} />}
        />
      </CardFooter>
    </ListPageCard>
  );
};

export default SecurityGroupCard;
