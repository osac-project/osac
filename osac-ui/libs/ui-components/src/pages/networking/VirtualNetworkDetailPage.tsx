import { useState } from 'react';
import { useNavigate, useParams } from 'react-router-dom';
import {
  Alert,
  Button,
  Card,
  CardBody,
  CardTitle,
  DescriptionList,
  DescriptionListDescription,
  DescriptionListGroup,
  DescriptionListTerm,
  Flex,
  FlexItem,
  PageSection,
  Stack,
  StackItem,
  Tab,
  TabTitleText,
  Tabs,
  Toolbar,
  ToolbarContent,
  ToolbarItem,
  Tooltip,
} from '@patternfly/react-core';

import { type NATGateway, SecurityGroups, Subnets, VirtualNetworkState } from '@osac/types';

import {
  useNatGateway,
  useSecurityGroups,
  useSubnets,
  useVirtualNetwork,
  virtualNetworkScopeFilter,
} from '../../api/v1/networking';
import { useFabricDomainsOfVirtualNetwork } from '../../api/v1/networking-consumers';
import { DetachNatGatewayModal } from '../../components/networking/DetachNatGatewayModal';
import GatedCreateButton from '../../components/networking/GatedCreateButton';
import NatGatewayCard from '../../components/networking/NatGatewayCard';
import NetworkingListEmptyState from '../../components/networking/NetworkingListEmptyState';
import NetworkingProjectName from '../../components/networking/NetworkingProjectName';
import {
  isDefaultResource,
  protectedResourceReason,
} from '../../components/networking/resourceOrigin';
import ResourceOriginBadge from '../../components/networking/ResourceOriginBadge';
import { SecurityGroupCreateModal } from '../../components/networking/SecurityGroupCreateModal';
import SecurityGroupsTable from '../../components/networking/SecurityGroupsTable';
import StatusWithMessage from '../../components/networking/StatusWithMessage';
import { SubnetCreateModal } from '../../components/networking/SubnetCreateModal';
import SubnetsTable from '../../components/networking/SubnetsTable';
import { useNetworkingTenants } from '../../components/networking/useNetworkingTenants';
import VirtualNetworkDeleteModal from '../../components/networking/VirtualNetworkDeleteModal';
import VirtualNetworkNatGatewayCell from '../../components/networking/VirtualNetworkNatGatewayCell';
import {
  virtualNetworkDeleteBlockedReason,
  virtualNetworkNotReadyReason,
} from '../../components/networking/virtualNetworkReadiness';
import { VirtualNetworkStatusLabel } from '../../components/networking/VirtualNetworkStatusLabel';
import ListPageBody from '../../components/Page/ListPageBody';
import { Timestamp } from '../../components/Primitives/Timestamp';
import QueryErrorState from '../../components/Resource/QueryErrorState';
import { ResourceDetailHeader } from '../../components/Resource/ResourceDetailHeader';
import { SubtleContent } from '../../components/SubtleContent/SubtleContent';
import { usePageFilter } from '../../hooks/use-page-filter';
import { useTranslation } from '../../hooks/useTranslation';

/** URL parameter of the active tab, so a flow can return to a tab (`?tab=nat-gateway`). */
const VIRTUAL_NETWORK_TAB_PARAM = 'tab';
const VIRTUAL_NETWORK_TABS = ['overview', 'subnets', 'security-groups', 'nat-gateway'];

export const VirtualNetworkDetailPage = () => {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const { id = '' } = useParams<{ id: string }>();
  const { isAdmin, tenantName } = useNetworkingTenants();
  const [tabParam, setTabParam] = usePageFilter(VIRTUAL_NETWORK_TAB_PARAM);
  const activeTabKey = VIRTUAL_NETWORK_TABS.includes(tabParam) ? tabParam : 'overview';
  const [isSubnetModalOpen, setIsSubnetModalOpen] = useState(false);
  const [isSecurityGroupModalOpen, setIsSecurityGroupModalOpen] = useState(false);
  const [detachTarget, setDetachTarget] = useState<NATGateway>();
  const [isDeleteOpen, setIsDeleteOpen] = useState(false);

  const { data: vn, isLoading, error } = useVirtualNetwork(id);
  const {
    data: subnets = [],
    isLoading: isLoadingSubnets,
    error: subnetsError,
  } = useSubnets({
    filter: virtualNetworkScopeFilter(id),
  });
  const {
    data: securityGroups = [],
    isLoading: isLoadingSecurityGroups,
    error: securityGroupsError,
  } = useSecurityGroups({
    filter: virtualNetworkScopeFilter(id),
  });
  const {
    natGateway,
    natAddress,
    isLoading: isLoadingNatGateway,
    error: natGatewayError,
  } = useNatGateway(id);
  const { data: fabricDomainsReply, isLoading: isLoadingFabricDomains } =
    useFabricDomainsOfVirtualNetwork(id);
  const fabricDomains = fabricDomainsReply?.items ?? [];

  const vnName = vn?.metadata?.name ?? id;
  const isFailed = vn?.status?.state === VirtualNetworkState.FAILED;
  // Children of a virtual network can only be created once it is READY.
  const createDisabledReason = vn ? virtualNetworkNotReadyReason(t, vn) : undefined;
  // A default network is deleted with its tenant. Otherwise Delete is disabled while the UI
  // knows of dependents, and the server's guard decides.
  const deleteBlockedReason =
    protectedResourceReason(t, vn) ??
    (isLoadingSubnets || isLoadingSecurityGroups || isLoadingNatGateway || isLoadingFabricDomains
      ? undefined
      : virtualNetworkDeleteBlockedReason(t, {
          subnetCount: subnets.length,
          securityGroupCount: securityGroups.length,
          natGatewayName: natGateway ? natGateway.metadata?.name || natGateway.id : undefined,
          fabricDomainCount: fabricDomains.length,
        }));
  const deleteButton = (
    <Button
      variant="secondary"
      isAriaDisabled={Boolean(deleteBlockedReason)}
      onClick={deleteBlockedReason ? undefined : () => setIsDeleteOpen(true)}
    >
      {t('Delete virtual network')}
    </Button>
  );

  const createSubnetButton = (
    <GatedCreateButton
      service={Subnets}
      onClick={() => setIsSubnetModalOpen(true)}
      disabledReason={createDisabledReason}
    >
      {t('Create subnet')}
    </GatedCreateButton>
  );
  const createSecurityGroupButton = (
    <GatedCreateButton
      service={SecurityGroups}
      onClick={() => setIsSecurityGroupModalOpen(true)}
      disabledReason={createDisabledReason}
    >
      {t('Create security group')}
    </GatedCreateButton>
  );

  return (
    <>
      <PageSection hasBodyWrapper={false}>
        <Stack hasGutter>
          <StackItem>
            <Flex
              justifyContent={{ default: 'justifyContentSpaceBetween' }}
              alignItems={{ default: 'alignItemsFlexStart' }}
              flexWrap={{ default: 'wrap' }}
              spaceItems={{ default: 'spaceItemsMd' }}
            >
              <FlexItem>
                <ResourceDetailHeader
                  parents={[{ to: '/networking/virtual-networks', label: t('Virtual networks') }]}
                  resourceName={vnName}
                  titleAddon={
                    vn ? (
                      <Flex
                        spaceItems={{ default: 'spaceItemsSm' }}
                        alignItems={{ default: 'alignItemsCenter' }}
                      >
                        <FlexItem>
                          {/* A FAILED network shows its message once, in the alert below. */}
                          <StatusWithMessage message={isFailed ? undefined : vn.status?.message}>
                            <VirtualNetworkStatusLabel state={vn.status?.state} />
                          </StatusWithMessage>
                        </FlexItem>
                        <FlexItem>
                          <ResourceOriginBadge resource={vn} />
                        </FlexItem>
                      </Flex>
                    ) : undefined
                  }
                />
              </FlexItem>
              {vn ? (
                <FlexItem>
                  {deleteBlockedReason ? (
                    <Tooltip content={deleteBlockedReason}>{deleteButton}</Tooltip>
                  ) : (
                    deleteButton
                  )}
                </FlexItem>
              ) : null}
            </Flex>
          </StackItem>
          <StackItem>
            <ListPageBody isLoading={isLoading} error={error}>
              <Stack hasGutter>
                {isFailed && vn?.status?.message ? (
                  <StackItem>
                    <Alert variant="danger" title={t('Provisioning failed')} isInline>
                      {vn.status.message}
                    </Alert>
                  </StackItem>
                ) : null}
                <StackItem>
                  <Tabs
                    activeKey={activeTabKey}
                    onSelect={(_event, key) => setTabParam(key === 'overview' ? '' : String(key))}
                    aria-label={t('Virtual network tabs')}
                    mountOnEnter
                    unmountOnExit
                  >
                    <Tab eventKey="overview" title={<TabTitleText>{t('Overview')}</TabTitleText>}>
                      <Card className="pf-v6-u-mt-md">
                        <CardTitle>{t('Details')}</CardTitle>
                        <CardBody>
                          <DescriptionList isHorizontal>
                            <DescriptionListGroup>
                              <DescriptionListTerm>{t('IPv4 CIDR')}</DescriptionListTerm>
                              <DescriptionListDescription>
                                {vn?.spec?.ipv4Cidr ?? '—'}
                              </DescriptionListDescription>
                            </DescriptionListGroup>
                            <DescriptionListGroup>
                              <DescriptionListTerm>{t('Project')}</DescriptionListTerm>
                              <DescriptionListDescription>
                                <NetworkingProjectName
                                  project={vn?.metadata?.project}
                                  tenant={vn?.metadata?.tenant}
                                />
                              </DescriptionListDescription>
                            </DescriptionListGroup>
                            {isAdmin ? (
                              <DescriptionListGroup>
                                <DescriptionListTerm>{t('Tenant')}</DescriptionListTerm>
                                <DescriptionListDescription>
                                  {tenantName(vn?.metadata?.tenant)}
                                </DescriptionListDescription>
                              </DescriptionListGroup>
                            ) : null}
                            <DescriptionListGroup>
                              <DescriptionListTerm>{t('NAT gateway')}</DescriptionListTerm>
                              <DescriptionListDescription>
                                <VirtualNetworkNatGatewayCell
                                  natGatewayWithAddress={
                                    natGateway ? { natGateway, address: natAddress } : undefined
                                  }
                                />
                              </DescriptionListDescription>
                            </DescriptionListGroup>
                            <DescriptionListGroup>
                              <DescriptionListTerm>{t('Created')}</DescriptionListTerm>
                              <DescriptionListDescription>
                                <Timestamp value={vn?.metadata?.creationTimestamp} />
                              </DescriptionListDescription>
                            </DescriptionListGroup>
                          </DescriptionList>
                        </CardBody>
                      </Card>
                    </Tab>

                    <Tab eventKey="subnets" title={<TabTitleText>{t('Subnets')}</TabTitleText>}>
                      {isLoadingSubnets ? (
                        <SubtleContent component="p" className="pf-v6-u-mt-md">
                          {t('Loading subnets...')}
                        </SubtleContent>
                      ) : subnetsError ? (
                        <QueryErrorState error={subnetsError} title={t('Failed to load subnets')} />
                      ) : subnets.length === 0 ? (
                        <NetworkingListEmptyState
                          emptyTitle={t('No subnets')}
                          emptyBody={
                            createDisabledReason ??
                            t('Subnets are address ranges for the workloads of this network.')
                          }
                          emptyAction={createSubnetButton}
                        />
                      ) : (
                        <>
                          <Toolbar>
                            <ToolbarContent>
                              <ToolbarItem>{createSubnetButton}</ToolbarItem>
                            </ToolbarContent>
                          </Toolbar>
                          <SubnetsTable subnets={subnets} />
                        </>
                      )}
                    </Tab>

                    <Tab
                      eventKey="security-groups"
                      title={<TabTitleText>{t('Security groups')}</TabTitleText>}
                    >
                      {isLoadingSecurityGroups ? (
                        <SubtleContent component="p" className="pf-v6-u-mt-md">
                          {t('Loading security groups...')}
                        </SubtleContent>
                      ) : securityGroupsError ? (
                        <QueryErrorState
                          error={securityGroupsError}
                          title={t('Failed to load security groups')}
                        />
                      ) : securityGroups.length === 0 ? (
                        <NetworkingListEmptyState
                          emptyTitle={t('No security groups')}
                          emptyBody={
                            createDisabledReason ??
                            t('Security groups hold the firewall rules of workloads.')
                          }
                          emptyAction={createSecurityGroupButton}
                        />
                      ) : (
                        <>
                          <Toolbar>
                            <ToolbarContent>
                              <ToolbarItem>{createSecurityGroupButton}</ToolbarItem>
                            </ToolbarContent>
                          </Toolbar>
                          <SecurityGroupsTable securityGroups={securityGroups} />
                        </>
                      )}
                    </Tab>

                    <Tab
                      eventKey="nat-gateway"
                      title={<TabTitleText>{t('NAT gateway')}</TabTitleText>}
                    >
                      <div className="pf-v6-u-mt-md">
                        <NatGatewayCard
                          natAddress={natAddress}
                          natGateway={natGateway}
                          isLoading={isLoadingNatGateway}
                          error={natGatewayError}
                          onAttach={() =>
                            navigate(`/networking/virtual-networks/${id}/nat-gateway/attach`)
                          }
                          onDetach={setDetachTarget}
                          isAttachHidden={isDefaultResource(vn)}
                          attachDisabledReason={createDisabledReason}
                        />
                      </div>
                    </Tab>
                  </Tabs>
                </StackItem>
              </Stack>
            </ListPageBody>
          </StackItem>
        </Stack>
      </PageSection>

      {isSecurityGroupModalOpen && (
        <SecurityGroupCreateModal
          onClose={() => setIsSecurityGroupModalOpen(false)}
          virtualNetworkId={id}
        />
      )}

      {isSubnetModalOpen && vn && (
        <SubnetCreateModal
          onClose={() => setIsSubnetModalOpen(false)}
          virtualNetworkId={vn.id}
          lockVirtualNetwork
        />
      )}
      {isDeleteOpen && vn && (
        <VirtualNetworkDeleteModal
          virtualNetwork={vn}
          onClose={() => setIsDeleteOpen(false)}
          onSuccess={() => navigate('/networking/virtual-networks')}
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
