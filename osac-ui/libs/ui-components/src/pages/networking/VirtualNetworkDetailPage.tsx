import { useState } from 'react';
import { useNavigate, useParams, useSearchParams } from 'react-router-dom';
import {
  Breadcrumb,
  BreadcrumbItem,
  Button,
  DescriptionList,
  DescriptionListDescription,
  DescriptionListGroup,
  DescriptionListTerm,
  Flex,
  FlexItem,
  Stack,
  StackItem,
  Tab,
  TabTitleText,
  Tabs,
  Timestamp,
  Tooltip,
} from '@patternfly/react-core';

import { type NATGateway, NATGatewayState, SecurityGroups, Subnets } from '@osac/types';

import { useListAllResources } from '../../api/use-resource';
import {
  useDeleteVirtualNetwork,
  useNatGateway,
  useVirtualNetwork,
  virtualNetworkScopeFilter,
} from '../../api/v1/networking';
import { virtualNetworkDeleteBlockedReason } from '../../components/networking/deleteDisabledReasons';
import { DetachNatGatewayModal } from '../../components/networking/DetachNatGatewayModal';
import { NatGatewayStatusLabel } from '../../components/networking/NatGatewayStatusLabel';
import NetworkingProjectName from '../../components/networking/NetworkingProjectName';
import {
  protectedResourceReason,
  resourceOrigin,
} from '../../components/networking/resourceOrigin';
import ResourceOriginBadge from '../../components/networking/ResourceOriginBadge';
import { SecurityGroupCreateModal } from '../../components/networking/SecurityGroupCreateModal';
import SecurityGroupsTable from '../../components/networking/SecurityGroupsTable';
import { SubnetCreateModal } from '../../components/networking/SubnetCreateModal';
import SubnetsTable from '../../components/networking/SubnetsTable';
import {
  isVirtualNetworkReady,
  virtualNetworkNotReadyReason,
} from '../../components/networking/virtualNetworkReadiness';
import { VirtualNetworkStatusLabel } from '../../components/networking/VirtualNetworkStatusLabel';
import ListPage from '../../components/Page/ListPage';
import ListPageBody from '../../components/Page/ListPageBody';
import CreateButton from '../../components/Primitives/CreateButton.tsx';
import DeleteResourceModal from '../../components/Resource/DeleteResourceModal';
import { SubtleContent } from '../../components/SubtleContent/SubtleContent';
import { useToast } from '../../components/Toast/useToast';
import { useTranslation } from '../../hooks/useTranslation';

const TAB_PARAM = 'tab';
const TAB_VALUES = ['overview', 'subnets', 'security-groups'] as const;
type VirtualNetworkTab = (typeof TAB_VALUES)[number];

const isVirtualNetworkTab = (value: string | null): value is VirtualNetworkTab =>
  value !== null && (TAB_VALUES as readonly string[]).includes(value);

export const VirtualNetworkDetailPage = () => {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const { id = '' } = useParams<{ id: string }>();
  const [searchParams, setSearchParams] = useSearchParams();
  const [isSubnetModalOpen, setIsSubnetModalOpen] = useState(false);
  const [isSecurityGroupModalOpen, setIsSecurityGroupModalOpen] = useState(false);
  const [isDeleteOpen, setIsDeleteOpen] = useState(false);
  const [detachTarget, setDetachTarget] = useState<NATGateway>();

  const { addToast } = useToast();
  const remove = useDeleteVirtualNetwork();

  const { data: vn, isLoading, error } = useVirtualNetwork(id);
  const {
    data: subnetsReply,
    isLoading: isLoadingSubnets,
    error: subnetsError,
  } = useListAllResources(Subnets, virtualNetworkScopeFilter(id));
  const subnets = subnetsReply?.items ?? [];
  const {
    data: securityGroupsReply,
    isLoading: isLoadingSecurityGroups,
    error: securityGroupsError,
  } = useListAllResources(SecurityGroups, virtualNetworkScopeFilter(id));
  const securityGroups = securityGroupsReply?.items ?? [];
  const { natGateway, natAddress, isGatewayLoading, gatewayError } = useNatGateway(id);

  const tabParam = searchParams.get(TAB_PARAM);
  const activeTab: VirtualNetworkTab = isVirtualNetworkTab(tabParam) ? tabParam : 'overview';
  const setActiveTab = (tab: VirtualNetworkTab) => {
    setSearchParams(
      (prev) => {
        const next = new URLSearchParams(prev);
        if (tab === 'overview') {
          next.delete(TAB_PARAM);
        } else {
          next.set(TAB_PARAM, tab);
        }
        return next;
      },
      { replace: true },
    );
  };

  const vnName = vn?.metadata?.name ?? id;
  const isDefault = resourceOrigin(vn?.metadata?.labels) === 'default';
  const ready = isVirtualNetworkReady(vn);
  const notReadyReason = ready ? undefined : virtualNetworkNotReadyReason(t);
  const natGatewayName = natGateway ? natGateway.metadata?.name || natGateway.id : undefined;

  // The dependents the UI knows about. A loading or failed query leaves Delete
  // enabled, so the server's guard decides rather than a partial view.
  const dependentsKnown =
    !isLoadingSubnets &&
    !isLoadingSecurityGroups &&
    !isGatewayLoading &&
    !subnetsError &&
    !securityGroupsError &&
    !gatewayError;
  const deleteBlockedReason =
    protectedResourceReason(t, vn?.metadata?.labels) ??
    (dependentsKnown
      ? virtualNetworkDeleteBlockedReason(t, {
          subnetCount: subnets.length,
          securityGroupCount: securityGroups.length,
          natGatewayName,
        })
      : undefined);

  const natGatewayDeleting =
    natGateway?.status?.state === NATGatewayState.NAT_GATEWAY_STATE_DELETING;
  // Default and automatically created gateways cannot be deleted on their own.
  const natGatewayProtectedReason = protectedResourceReason(t, natGateway?.metadata?.labels);
  const natGatewayRow = natGateway ? (
    <Flex
      spaceItems={{ default: 'spaceItemsSm' }}
      alignItems={{ default: 'alignItemsCenter' }}
      flexWrap={{ default: 'wrap' }}
    >
      <FlexItem>
        <span className="pf-v6-u-text-nowrap">
          <strong>{natGatewayName}</strong>
          {natAddress ? <span>{` (${natAddress})`}</span> : null}
        </span>
      </FlexItem>
      <FlexItem>
        <NatGatewayStatusLabel
          state={natGateway.status?.state}
          message={natGateway.status?.message}
        />
      </FlexItem>
      <FlexItem>
        <Tooltip
          content={
            natGatewayProtectedReason
              ? natGatewayProtectedReason
              : natGatewayDeleting
                ? t('This NAT gateway is being deleted.')
                : undefined
          }
        >
          <Button
            variant="link"
            isInline
            isAriaDisabled={natGatewayDeleting || Boolean(natGatewayProtectedReason)}
            onClick={() => setDetachTarget(natGateway)}
          >
            {t('Delete')}
          </Button>
        </Tooltip>
      </FlexItem>
    </Flex>
  ) : isGatewayLoading ? (
    <SubtleContent component="p">{t('Loading NAT gateway...')}</SubtleContent>
  ) : gatewayError ? (
    <>—</>
  ) : (
    <Flex spaceItems={{ default: 'spaceItemsSm' }} alignItems={{ default: 'alignItemsCenter' }}>
      <FlexItem>—</FlexItem>
      {!isDefault && (
        <FlexItem>
          <Tooltip content={notReadyReason}>
            <span>
              <Button
                variant="link"
                isInline
                isAriaDisabled={!ready}
                onClick={() => navigate(`/networking/virtual-networks/${id}/nat-gateway/attach`)}
              >
                {t('Create NAT gateway')}
              </Button>
            </span>
          </Tooltip>
        </FlexItem>
      )}
    </Flex>
  );

  const overview = (
    <DescriptionList aria-label={t('Virtual network details')}>
      <DescriptionListGroup>
        <DescriptionListTerm>{t('Name')}</DescriptionListTerm>
        <DescriptionListDescription>
          <Flex
            spaceItems={{ default: 'spaceItemsSm' }}
            alignItems={{ default: 'alignItemsCenter' }}
          >
            <FlexItem>{vnName}</FlexItem>
            <FlexItem>
              <ResourceOriginBadge labels={vn?.metadata?.labels} />
            </FlexItem>
          </Flex>
        </DescriptionListDescription>
      </DescriptionListGroup>
      <DescriptionListGroup>
        <DescriptionListTerm>{t('Project')}</DescriptionListTerm>
        <DescriptionListDescription>
          <NetworkingProjectName project={vn?.metadata?.project} />
        </DescriptionListDescription>
      </DescriptionListGroup>
      <DescriptionListGroup>
        <DescriptionListTerm>{t('IPv4 CIDR')}</DescriptionListTerm>
        <DescriptionListDescription>{vn?.spec?.ipv4Cidr ?? '—'}</DescriptionListDescription>
      </DescriptionListGroup>
      <DescriptionListGroup>
        <DescriptionListTerm>{t('Subnets')}</DescriptionListTerm>
        <DescriptionListDescription>
          {isLoadingSubnets || subnetsError ? '—' : subnets.length}
        </DescriptionListDescription>
      </DescriptionListGroup>
      <DescriptionListGroup>
        <DescriptionListTerm>{t('Security groups')}</DescriptionListTerm>
        <DescriptionListDescription>
          {isLoadingSecurityGroups || securityGroupsError ? '—' : securityGroups.length}
        </DescriptionListDescription>
      </DescriptionListGroup>
      <DescriptionListGroup>
        <DescriptionListTerm>{t('NAT gateway')}</DescriptionListTerm>
        <DescriptionListDescription>{natGatewayRow}</DescriptionListDescription>
      </DescriptionListGroup>
      <DescriptionListGroup>
        <DescriptionListTerm>{t('Created')}</DescriptionListTerm>
        <DescriptionListDescription>
          {vn?.metadata?.creationTimestamp ? (
            <Timestamp date={new Date(Number(vn.metadata.creationTimestamp.seconds) * 1000)} />
          ) : (
            '—'
          )}
        </DescriptionListDescription>
      </DescriptionListGroup>
    </DescriptionList>
  );

  const createSubnetButton = (
    <Tooltip content={notReadyReason}>
      <span>
        <CreateButton
          service={Subnets}
          variant="secondary"
          isDisabled={!ready}
          onClick={() => setIsSubnetModalOpen(true)}
        >
          {t('Create subnet')}
        </CreateButton>
      </span>
    </Tooltip>
  );
  const createSecurityGroupButton = (
    <Tooltip content={notReadyReason}>
      <span>
        <CreateButton
          service={SecurityGroups}
          variant="secondary"
          isDisabled={!ready}
          onClick={() => setIsSecurityGroupModalOpen(true)}
        >
          {t('Create security group')}
        </CreateButton>
      </span>
    </Tooltip>
  );

  return (
    <>
      <ListPage
        title={vnName}
        description={t('View the virtual network configuration and related resources.')}
        breadcrumb={
          <Breadcrumb>
            <BreadcrumbItem>
              <Button
                variant="link"
                isInline
                onClick={() => navigate('/networking/virtual-networks')}
              >
                {t('Virtual networks')}
              </Button>
            </BreadcrumbItem>
            <BreadcrumbItem isActive>{vnName}</BreadcrumbItem>
          </Breadcrumb>
        }
        actions={
          <Tooltip content={deleteBlockedReason}>
            <span>
              <Button
                variant="danger"
                isAriaDisabled={!vn || Boolean(deleteBlockedReason)}
                onClick={() => setIsDeleteOpen(true)}
              >
                {t('Delete virtual network')}
              </Button>
            </span>
          </Tooltip>
        }
      >
        <ListPageBody isLoading={isLoading} error={error}>
          {vn ? (
            <div className="pf-v6-u-mb-md">
              <VirtualNetworkStatusLabel state={vn.status?.state} message={vn.status?.message} />
            </div>
          ) : null}

          <Tabs
            activeKey={activeTab}
            onSelect={(_event, key) => setActiveTab(key as VirtualNetworkTab)}
            aria-label={t('Virtual network details tabs')}
          >
            <Tab eventKey="overview" title={<TabTitleText>{t('Overview')}</TabTitleText>}>
              <div className="pf-v6-u-mt-md">{overview}</div>
            </Tab>
            <Tab eventKey="subnets" title={<TabTitleText>{t('Subnets')}</TabTitleText>}>
              <div className="pf-v6-u-mt-md">
                <Stack hasGutter>
                  <StackItem>
                    <Flex justifyContent={{ default: 'justifyContentFlexEnd' }}>
                      <FlexItem>{createSubnetButton}</FlexItem>
                    </Flex>
                  </StackItem>
                  <StackItem>
                    <SubnetsTable
                      subnets={subnets}
                      isLoading={isLoadingSubnets}
                      error={subnetsError}
                    />
                  </StackItem>
                </Stack>
              </div>
            </Tab>
            <Tab
              eventKey="security-groups"
              title={<TabTitleText>{t('Security groups')}</TabTitleText>}
            >
              <div className="pf-v6-u-mt-md">
                <Stack hasGutter>
                  <StackItem>
                    <Flex justifyContent={{ default: 'justifyContentFlexEnd' }}>
                      <FlexItem>{createSecurityGroupButton}</FlexItem>
                    </Flex>
                  </StackItem>
                  <StackItem>
                    <SecurityGroupsTable
                      securityGroups={securityGroups}
                      isLoading={isLoadingSecurityGroups}
                      error={securityGroupsError}
                    />
                  </StackItem>
                </Stack>
              </div>
            </Tab>
          </Tabs>
        </ListPageBody>
      </ListPage>

      {isDeleteOpen && (
        <DeleteResourceModal
          resourceName={vnName}
          label={t(
            'Remove its subnets, security groups and NAT gateway first. A network that still has dependents cannot be deleted.',
          )}
          errorLabel={t('Failed to delete virtual network')}
          mutation={remove}
          variables={id}
          disabledReason={deleteBlockedReason}
          onClose={() => setIsDeleteOpen(false)}
          onSuccess={() => {
            setIsDeleteOpen(false);
            addToast({
              variant: 'success',
              title: t('Virtual network {{name}} deleted', { name: vnName }),
            });
            navigate('/networking/virtual-networks');
          }}
        />
      )}

      {isSecurityGroupModalOpen && (
        <SecurityGroupCreateModal
          onClose={() => setIsSecurityGroupModalOpen(false)}
          virtualNetworkId={id}
        />
      )}
      {isSubnetModalOpen && vn && (
        <SubnetCreateModal
          onClose={() => setIsSubnetModalOpen(false)}
          parentVN={vn}
          existingSubnets={subnets}
        />
      )}
      {detachTarget && (
        <DetachNatGatewayModal
          natGateway={detachTarget}
          onClose={() => setDetachTarget(undefined)}
        />
      )}
    </>
  );
};
