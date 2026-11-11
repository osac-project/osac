import { useMemo } from 'react';
import type { TFunction } from 'i18next';

import {
  type NATGateway,
  NATGatewayState,
  type SecurityGroup,
  SecurityGroupState,
  type Subnet,
  SubnetState,
} from '@osac/types';

import { protectedResourceReason, workloadKindLabel } from './resourceOrigin';
import {
  type NetworkConsumer,
  useClustersOnVirtualNetwork,
  useNetworkConsumers,
} from '../../api/v1/networking-consumers';
import { useTranslation } from '../../hooks/useTranslation';

/**
 * "Used by virtual machine web-1. Delete it first." for the workloads the UI sees, or
 * undefined. The server's deletion guard still decides; it also counts workloads in
 * projects the caller cannot see.
 */
export const usedByWorkloadsReason = (
  t: TFunction,
  consumers: readonly NetworkConsumer[],
): string | undefined => {
  const [first] = consumers;
  if (!first) {
    return undefined;
  }
  const kind = workloadKindLabel(t, first.kind) ?? '';
  return consumers.length === 1
    ? t('Used by {{kind}} {{name}}. Delete it first.', { kind, name: first.name })
    : t('Used by {{kind}} {{name}} and {{more}} more. Delete them first.', {
        kind,
        name: first.name,
        more: consumers.length - 1,
      });
};

/** Why a NAT gateway cannot be deleted while clusters on its network use it. */
export const clustersUseNatGatewayReason = (
  t: TFunction,
  clusters: readonly NetworkConsumer[],
): string | undefined => {
  const [first] = clusters;
  if (!first) {
    return undefined;
  }
  return clusters.length === 1
    ? t(
        'Cluster {{name}} reaches its management endpoints through this gateway. Delete it first.',
        {
          name: first.name,
        },
      )
    : t(
        'Cluster {{name}} and {{more}} more reach their management endpoints through this gateway. Delete them first.',
        { name: first.name, more: clusters.length - 1 },
      );
};

/** Why the subnet's Delete is disabled, or undefined. */
export const useSubnetDeleteDisabledReason = (subnet: Subnet): string | undefined => {
  const { t } = useTranslation();
  const { consumers } = useNetworkConsumers();
  const users = useMemo(
    () => consumers.filter((c) => c.subnetIds.includes(subnet.id)),
    [consumers, subnet.id],
  );
  return (
    protectedResourceReason(t, subnet) ??
    (subnet.status?.state === SubnetState.DELETING
      ? t('This subnet is being deleted.')
      : usedByWorkloadsReason(t, users))
  );
};

/** Why the security group's Delete is disabled, or undefined. */
export const useSecurityGroupDeleteDisabledReason = (
  securityGroup?: SecurityGroup,
): string | undefined => {
  const { t } = useTranslation();
  const { consumers } = useNetworkConsumers();
  const id = securityGroup?.id;
  const users = useMemo(
    () => (id ? consumers.filter((c) => c.securityGroupIds.includes(id)) : []),
    [consumers, id],
  );
  if (!securityGroup) {
    return undefined;
  }
  return (
    protectedResourceReason(t, securityGroup) ??
    (securityGroup.status?.state === SecurityGroupState.DELETING
      ? t('This security group is being deleted.')
      : usedByWorkloadsReason(t, users))
  );
};

/** Why the NAT gateway's Delete is disabled, or undefined. */
export const useNatGatewayDeleteDisabledReason = (natGateway?: NATGateway): string | undefined => {
  const { t } = useTranslation();
  const { clusters } = useClustersOnVirtualNetwork(natGateway?.spec?.virtualNetwork?.id);
  if (!natGateway) {
    return undefined;
  }
  return (
    protectedResourceReason(t, natGateway) ??
    (natGateway.status?.state === NATGatewayState.NAT_GATEWAY_STATE_DELETING
      ? t('This NAT gateway is being deleted.')
      : clustersUseNatGatewayReason(t, clusters))
  );
};
