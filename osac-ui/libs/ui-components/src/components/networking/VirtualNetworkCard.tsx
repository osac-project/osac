import {
  CardBody,
  CardFooter,
  DescriptionList,
  DescriptionListDescription,
  DescriptionListGroup,
  DescriptionListTerm,
  Divider,
} from '@patternfly/react-core';
import RhUiZoneIcon from '@patternfly/react-icons/dist/esm/icons/rh-ui-zone-icon';

import type { VirtualNetwork } from '@osac/types';
import type { NatGatewayWithAddress } from '@osac/ui-components/api/v1/networking';
import VirtualNetworkActionsMenu from '@osac/ui-components/components/networking/VirtualNetworkActionsMenu';
import VirtualNetworkLoadedField from '@osac/ui-components/components/networking/VirtualNetworkLoadedField';
import ListPageCard from '@osac/ui-components/components/Page/ListPageCard';

import { CidrDisplay } from './CidrDisplay';
import { getVirtualNetworkName } from './virtual-network-list-utils';
import VirtualNetworkNatGatewayCell from './VirtualNetworkNatGatewayCell';
import { VirtualNetworkStatusLabel } from './VirtualNetworkStatusLabel';
import { useTranslation } from '../../hooks/useTranslation';
import ResourceNameField from '../Resource/ResourceNameField';

interface VirtualNetworkCardProps {
  virtualNetwork: VirtualNetwork;
  subnetCount: number;
  subnetsLoading?: boolean;
  subnetsError?: unknown;
  securityGroupCount: number;
  securityGroupsLoading?: boolean;
  securityGroupsError?: unknown;
  natGatewayWithAddress?: NatGatewayWithAddress;
  natGatewaysLoading?: boolean;
  natGatewaysError?: unknown;
}

const VirtualNetworkCard = ({
  virtualNetwork,
  subnetCount,
  subnetsLoading,
  subnetsError,
  securityGroupCount,
  securityGroupsLoading,
  securityGroupsError,
  natGatewayWithAddress,
  natGatewaysLoading,
  natGatewaysError,
}: VirtualNetworkCardProps) => {
  const { t } = useTranslation();

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
              <VirtualNetworkLoadedField
                unavailableAriaLabel={t('Failed to load subnets')}
                isLoading={subnetsLoading}
                error={subnetsError}
              >
                {subnetCount}
              </VirtualNetworkLoadedField>
            </DescriptionListDescription>
          </DescriptionListGroup>
          <DescriptionListGroup>
            <DescriptionListTerm>{t('Security groups')}</DescriptionListTerm>
            <DescriptionListDescription>
              <VirtualNetworkLoadedField
                unavailableAriaLabel={t('Failed to load security groups')}
                isLoading={securityGroupsLoading}
                error={securityGroupsError}
              >
                {securityGroupCount}
              </VirtualNetworkLoadedField>
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
              <VirtualNetworkLoadedField
                unavailableAriaLabel={t('Failed to load NAT gateways')}
                isLoading={natGatewaysLoading}
                error={natGatewaysError}
              >
                <VirtualNetworkNatGatewayCell natGatewayWithAddress={natGatewayWithAddress} />
              </VirtualNetworkLoadedField>
            </DescriptionListDescription>
          </DescriptionListGroup>
        </DescriptionList>
      </CardFooter>
    </ListPageCard>
  );
};

export default VirtualNetworkCard;
