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
import OsacCardFooterRow from '@osac/ui-components/components/Primitives/OsacCardFooterRow';

import { CidrDisplay } from './CidrDisplay';
import NetworkingProjectName from './NetworkingProjectName';
import NetworkingResourceName from './NetworkingResourceName';
import StatusWithMessage from './StatusWithMessage';
import { getVirtualNetworkName } from './virtual-network-list-utils';
import VirtualNetworkNatGatewayCell from './VirtualNetworkNatGatewayCell';
import { VirtualNetworkStatusLabel } from './VirtualNetworkStatusLabel';
import { useTranslation } from '../../hooks/useTranslation';

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
  /** Adds the Tenant, for cloud provider admins who see every tenant's networks. */
  tenantName?: (tenantId?: string) => string;
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
  tenantName,
}: VirtualNetworkCardProps) => {
  const { t } = useTranslation();

  return (
    <ListPageCard
      title={
        <NetworkingResourceName
          resource={virtualNetwork}
          title={getVirtualNetworkName(virtualNetwork)}
          detailsUrl={`/networking/virtual-networks/${virtualNetwork.id}`}
        />
      }
      icon={<RhUiZoneIcon />}
      statusLabel={
        <StatusWithMessage message={virtualNetwork.status?.message}>
          <VirtualNetworkStatusLabel state={virtualNetwork.status?.state} />
        </StatusWithMessage>
      }
      actionMenu={<VirtualNetworkActionsMenu virtualNetwork={virtualNetwork} />}
    >
      <CardBody>
        <DescriptionList isCompact isHorizontal>
          {tenantName ? (
            <DescriptionListGroup>
              <DescriptionListTerm>{t('Tenant')}</DescriptionListTerm>
              <DescriptionListDescription>
                {tenantName(virtualNetwork.metadata?.tenant)}
              </DescriptionListDescription>
            </DescriptionListGroup>
          ) : null}
          <DescriptionListGroup>
            <DescriptionListTerm>{t('Project')}</DescriptionListTerm>
            <DescriptionListDescription>
              <NetworkingProjectName
                project={virtualNetwork.metadata?.project}
                tenant={virtualNetwork.metadata?.tenant}
              />
            </DescriptionListDescription>
          </DescriptionListGroup>
          <DescriptionListGroup>
            <DescriptionListTerm>{t('IPv4 CIDR')}</DescriptionListTerm>
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
        <OsacCardFooterRow
          term={t('NAT gateway')}
          description={
            <VirtualNetworkLoadedField
              unavailableAriaLabel={t('Failed to load NAT gateways')}
              isLoading={natGatewaysLoading}
              error={natGatewaysError}
            >
              <VirtualNetworkNatGatewayCell natGatewayWithAddress={natGatewayWithAddress} />
            </VirtualNetworkLoadedField>
          }
        />
      </CardFooter>
    </ListPageCard>
  );
};

export default VirtualNetworkCard;
