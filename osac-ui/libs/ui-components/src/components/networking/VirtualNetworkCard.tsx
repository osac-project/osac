import {
  CardBody,
  CardFooter,
  DescriptionList,
  DescriptionListDescription,
  DescriptionListGroup,
  DescriptionListTerm,
  Divider,
  Skeleton,
} from '@patternfly/react-core';
import RhUiZoneIcon from '@patternfly/react-icons/dist/esm/icons/rh-ui-zone-icon';

import { SecurityGroups, Subnets, VirtualNetwork } from '@osac/types';
import { useListResource } from '@osac/ui-components/api/use-resource';
import {
  NatGatewayWithAddress,
  virtualNetworkScopeFilter,
} from '@osac/ui-components/api/v1/networking';
import VirtualNetworkActionsMenu from '@osac/ui-components/components/networking/VirtualNetworkActionsMenu';
import ListPageCard from '@osac/ui-components/components/Page/ListPageCard';

import { CidrDisplay } from './CidrDisplay';
import { getVirtualNetworkName } from './virtual-network-list-utils';
import VirtualNetworkNatGatewayCell from './VirtualNetworkNatGatewayCell';
import { VirtualNetworkStatusLabel } from './VirtualNetworkStatusLabel';
import { useTranslation } from '../../hooks/useTranslation';
import ResourceNameField from '../Resource/ResourceNameField';

interface VirtualNetworkCardProps {
  virtualNetwork: VirtualNetwork;
  natGatewayWithAddress?: NatGatewayWithAddress;
}

const VirtualNetworkCard = ({ virtualNetwork, natGatewayWithAddress }: VirtualNetworkCardProps) => {
  const { t } = useTranslation();

  const { data: subnetTotals, isLoading: subnetsLoading } = useListResource(Subnets, {
    filter: virtualNetworkScopeFilter(virtualNetwork.id),
    limit: 0,
  });
  const subnetCount = subnetTotals?.total ?? 0;

  const { data: securityGroupTotals, isLoading: securityGroupsLoading } = useListResource(
    SecurityGroups,
    {
      filter: virtualNetworkScopeFilter(virtualNetwork.id),
      limit: 0,
    },
  );
  const securityGroupCount = securityGroupTotals?.total ?? 0;

  return (
    <ListPageCard
      title={
        <ResourceNameField
          resource={virtualNetwork}
          title={getVirtualNetworkName(virtualNetwork)}
          detailsUrl={`/networking/virtual-networks/${virtualNetwork.id}`}
        />
      }
      icon={<RhUiZoneIcon />}
      statusLabel={<VirtualNetworkStatusLabel state={virtualNetwork.status?.state} />}
      actionMenu={<VirtualNetworkActionsMenu virtualNetwork={virtualNetwork} />}
    >
      <CardBody>
        <DescriptionList isCompact isHorizontal>
          <DescriptionListGroup>
            <DescriptionListTerm>{t('IPv4')}</DescriptionListTerm>
            <DescriptionListDescription>
              <CidrDisplay ipv4Cidr={virtualNetwork.spec?.ipv4Cidr} />
            </DescriptionListDescription>
          </DescriptionListGroup>
          <Divider />
          <DescriptionListGroup>
            <DescriptionListTerm>{t('Subnets')}</DescriptionListTerm>
            <DescriptionListDescription>
              {subnetsLoading ? <Skeleton width="25px" /> : subnetCount}
            </DescriptionListDescription>
          </DescriptionListGroup>
          <DescriptionListGroup>
            <DescriptionListTerm>{t('Security groups')}</DescriptionListTerm>
            <DescriptionListDescription>
              {securityGroupsLoading ? <Skeleton width="25px" /> : securityGroupCount}
            </DescriptionListDescription>
          </DescriptionListGroup>
        </DescriptionList>
      </CardBody>
      <Divider />
      <CardFooter>
        <DescriptionList isCompact isHorizontal>
          <DescriptionListGroup>
            <DescriptionListTerm>{t('NAT gateway')}</DescriptionListTerm>
            <DescriptionListDescription>
              <VirtualNetworkNatGatewayCell natGatewayWithAddress={natGatewayWithAddress} />
            </DescriptionListDescription>
          </DescriptionListGroup>
        </DescriptionList>
      </CardFooter>
    </ListPageCard>
  );
};

export default VirtualNetworkCard;
