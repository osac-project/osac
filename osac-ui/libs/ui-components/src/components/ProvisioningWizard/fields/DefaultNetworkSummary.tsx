import {
  Alert,
  Button,
  Content,
  DescriptionList,
  DescriptionListDescription,
  DescriptionListGroup,
  DescriptionListTerm,
  Spinner,
  Stack,
  StackItem,
} from '@patternfly/react-core';

import { SecurityGroupState, SubnetState } from '@osac/types';

import {
  defaultSecurityGroupListFilter,
  defaultSubnetListFilter,
  useSecurityGroups,
  useSubnets,
} from '../../../api/v1/networking';
import { useTranslation } from '../../../hooks/useTranslation';
import { getErrorMessage } from '../../../utils/error';

const newestFirst = (
  a: { metadata?: { creationTimestamp?: { seconds: bigint; nanos: number } } },
  b: { metadata?: { creationTimestamp?: { seconds: bigint; nanos: number } } },
) => {
  const aTime = a.metadata?.creationTimestamp;
  const bTime = b.metadata?.creationTimestamp;
  if ((aTime?.seconds ?? 0n) !== (bTime?.seconds ?? 0n)) {
    return (aTime?.seconds ?? 0n) > (bTime?.seconds ?? 0n) ? -1 : 1;
  }
  return (bTime?.nanos ?? 0) - (aTime?.nanos ?? 0);
};

const DefaultNetworkSummary = () => {
  const { t } = useTranslation();
  // Tenant defaults live in the tenant's default project and apply to every
  // project of the tenant. Only tenant users reach the provisioning wizards, and
  // the server scopes their lists to their tenant.
  const inDefaultProject = (metadata?: { project?: string }) => metadata?.project === '';
  const subnetsQuery = useSubnets({ filter: defaultSubnetListFilter() });
  const subnet = subnetsQuery.data
    ?.filter(
      (item) =>
        item.metadata?.labels?.['osac.openshift.io/default'] === 'true' &&
        inDefaultProject(item.metadata) &&
        !item.metadata.deletionTimestamp &&
        item.status?.state === SubnetState.READY &&
        Boolean(item.spec?.ipv4Cidr),
    )
    .sort(newestFirst)[0];
  const virtualNetworkId = subnet?.spec?.virtualNetwork?.id ?? '';
  const securityGroupsQuery = useSecurityGroups(
    {
      filter: defaultSecurityGroupListFilter(virtualNetworkId),
    },
    { enabled: Boolean(virtualNetworkId) },
  );
  const securityGroup = securityGroupsQuery.data
    ?.filter(
      (group) =>
        group.metadata?.labels?.['osac.openshift.io/default'] === 'true' &&
        inDefaultProject(group.metadata) &&
        !group.metadata.deletionTimestamp &&
        group.spec?.virtualNetwork?.id === virtualNetworkId &&
        group.status?.state === SecurityGroupState.READY,
    )
    .sort(newestFirst)[0];

  if (subnetsQuery.isLoading || securityGroupsQuery.isLoading) {
    return <Spinner size="md" aria-label={t('Loading tenant default network')} />;
  }

  const error = subnetsQuery.error ?? securityGroupsQuery.error;
  if (error) {
    return (
      <Alert variant="danger" isInline title={t('Could not load tenant default network')}>
        {getErrorMessage(error)}
        <Button
          variant="link"
          isInline
          onClick={() => {
            if (subnetsQuery.error) {
              void subnetsQuery.refetch();
            } else {
              void securityGroupsQuery.refetch();
            }
          }}
        >
          {t('Retry')}
        </Button>
      </Alert>
    );
  }

  if (!subnet) {
    return (
      <Alert variant="warning" isInline title={t('Tenant default network is unavailable')}>
        {t('Choose a custom network or try again when tenant networking is ready.')}
      </Alert>
    );
  }

  const virtualNetworkName =
    subnet.spec?.virtualNetwork?.name || subnet.spec?.virtualNetwork?.id || '—';
  const subnetName = subnet.metadata?.name || subnet.id;

  return (
    <Stack hasGutter>
      {!securityGroup && (
        <StackItem>
          <Alert
            variant="warning"
            isInline
            title={t('Tenant default security group is unavailable')}
          >
            {t('Choose a custom network or try again when tenant networking is ready.')}
          </Alert>
        </StackItem>
      )}
      <StackItem>
        <DescriptionList isHorizontal isCompact aria-label={t('Tenant default network details')}>
          <DescriptionListGroup>
            <DescriptionListTerm>{t('Virtual network')}</DescriptionListTerm>
            <DescriptionListDescription>
              <strong>{virtualNetworkName}</strong>
            </DescriptionListDescription>
          </DescriptionListGroup>
          <DescriptionListGroup>
            <DescriptionListTerm>{t('Subnet')}</DescriptionListTerm>
            <DescriptionListDescription>
              <strong>{subnetName}</strong>
              {subnet.spec?.ipv4Cidr && <Content component="p">{subnet.spec.ipv4Cidr}</Content>}
            </DescriptionListDescription>
          </DescriptionListGroup>
          <DescriptionListGroup>
            <DescriptionListTerm>{t('Security group')}</DescriptionListTerm>
            <DescriptionListDescription>
              <strong>{securityGroup?.metadata?.name || securityGroup?.id || '—'}</strong>
            </DescriptionListDescription>
          </DescriptionListGroup>
        </DescriptionList>
      </StackItem>
    </Stack>
  );
};

export default DefaultNetworkSummary;
