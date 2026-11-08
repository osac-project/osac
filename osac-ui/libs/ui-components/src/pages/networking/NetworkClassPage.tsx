import {
  Alert,
  Card,
  CardBody,
  CardTitle,
  Content,
  DescriptionList,
  DescriptionListDescription,
  DescriptionListGroup,
  DescriptionListTerm,
  Grid,
  GridItem,
  Stack,
  StackItem,
  Title,
} from '@patternfly/react-core';

import { NetworkClassState, NetworkClasses, type NetworkManager } from '@osac/types/private';

import { useListResource } from '../../api/use-resource';
import { NetworkClassStatusLabel } from '../../components/networking/NetworkClassStatusLabel';
import { SecurityGroupRulesTable } from '../../components/networking/SecurityGroupRulesTable';
import StatusWithMessage from '../../components/networking/StatusWithMessage';
import ListPage from '../../components/Page/ListPage';
import ListPageBody from '../../components/Page/ListPageBody';
import { SubtleContent } from '../../components/SubtleContent/SubtleContent';
import { useTranslation } from '../../hooks/useTranslation';

interface ManagerValueProps {
  /** The manager the installer asked for (spec). */
  requested?: string;
  /** The manager OSAC resolved (status), with its Ansible role. */
  resolved?: NetworkManager;
  /** Shown when no manager is requested. */
  noneLabel?: string;
}

/** One manager: the resolved name and role, or the requested name until it is resolved. */
const ManagerValue = ({ requested, resolved, noneLabel }: ManagerValueProps) => {
  const { t } = useTranslation();
  if (resolved?.name) {
    return (
      <Stack>
        <StackItem>{resolved.name}</StackItem>
        {resolved.ansibleRole ? (
          <StackItem>
            <Content component="small">{resolved.ansibleRole}</Content>
          </StackItem>
        ) : null}
      </Stack>
    );
  }
  if (requested) {
    return <>{t('{{name}} (not resolved yet)', { name: requested })}</>;
  }
  return <>{noneLabel ?? '—'}</>;
};

const formatShare = (share = 0): string =>
  new Intl.NumberFormat(undefined, { style: 'percent', maximumFractionDigits: 2 }).format(share);

/**
 * Read-only view of the deployment's NetworkClass: its status, managers, capabilities,
 * tenant defaults and subnet address plan. The installer fixes the defaults and the plan
 * when it creates the NetworkClass, so they are what every tenant and subnet got.
 * The installer creates it; the UI has no create, edit or delete. A deployment has one
 * NetworkClass until sites are introduced, when this page becomes a list.
 */
export const NetworkClassPage = () => {
  const { t } = useTranslation();
  const { data, isLoading, error } = useListResource(NetworkClasses);
  const networkClass = data?.items?.[0];
  const status = networkClass?.status;
  const icmp = status?.securityGroupIcmp;
  const defaults = networkClass?.spec?.defaults;
  const addressPlan = networkClass?.spec?.addressPlan;

  return (
    <ListPage
      title={t('Network class')}
      description={t('How this deployment provides tenant networking. Created by the installer.')}
    >
      <ListPageBody isLoading={isLoading} error={error}>
        {status?.state === NetworkClassState.FAILED && status.message ? (
          <Alert
            variant="danger"
            isInline
            title={t('The network class is not ready')}
            className="pf-v6-u-mb-md"
          >
            {status.message}
          </Alert>
        ) : null}
        {!networkClass ? (
          <SubtleContent component="p">
            {t('No network class is configured. The installer creates it when it deploys OSAC.')}
          </SubtleContent>
        ) : (
          <Grid hasGutter>
            <GridItem md={6}>
              <Card isFullHeight>
                <CardTitle>
                  {networkClass.title || networkClass.metadata?.name || networkClass.id}
                </CardTitle>
                <CardBody>
                  <DescriptionList isHorizontal aria-label={t('Network class')}>
                    <DescriptionListGroup>
                      <DescriptionListTerm>{t('Description')}</DescriptionListTerm>
                      <DescriptionListDescription>
                        {networkClass.description || '—'}
                      </DescriptionListDescription>
                    </DescriptionListGroup>
                    <DescriptionListGroup>
                      <DescriptionListTerm>{t('Status')}</DescriptionListTerm>
                      <DescriptionListDescription>
                        {/* A FAILED class shows its message once, in the alert above. */}
                        <StatusWithMessage
                          message={
                            status?.state === NetworkClassState.FAILED ? undefined : status?.message
                          }
                        >
                          <NetworkClassStatusLabel state={status?.state} />
                        </StatusWithMessage>
                      </DescriptionListDescription>
                    </DescriptionListGroup>
                    <DescriptionListGroup>
                      <DescriptionListTerm>{t('Fabric manager')}</DescriptionListTerm>
                      <DescriptionListDescription>
                        <ManagerValue
                          requested={networkClass.fabricManager}
                          resolved={status?.networkManagers?.fabric}
                        />
                      </DescriptionListDescription>
                    </DescriptionListGroup>
                    <DescriptionListGroup>
                      <DescriptionListTerm>{t('Kubernetes manager')}</DescriptionListTerm>
                      <DescriptionListDescription>
                        <ManagerValue
                          requested={networkClass.k8sManager}
                          resolved={status?.networkManagers?.kubernetes}
                          noneLabel={t('None (virtual machines are disabled)')}
                        />
                      </DescriptionListDescription>
                    </DescriptionListGroup>
                    <DescriptionListGroup>
                      <DescriptionListTerm>{t('Network type')}</DescriptionListTerm>
                      <DescriptionListDescription>
                        {status?.networkType || '—'}
                      </DescriptionListDescription>
                    </DescriptionListGroup>
                  </DescriptionList>
                </CardBody>
              </Card>
            </GridItem>
            <GridItem md={6}>
              <Card isFullHeight>
                <CardTitle>{t('Capabilities')}</CardTitle>
                <CardBody>
                  <DescriptionList isHorizontal aria-label={t('Capabilities')}>
                    <DescriptionListGroup>
                      <DescriptionListTerm>{t('East-west Ethernet')}</DescriptionListTerm>
                      <DescriptionListDescription>
                        {networkClass.spec?.eastWestCapabilities?.supportsEastWestEthernet
                          ? t('Supported')
                          : t('Not supported')}
                      </DescriptionListDescription>
                    </DescriptionListGroup>
                    <DescriptionListGroup>
                      <DescriptionListTerm>{t('ICMP in security groups')}</DescriptionListTerm>
                      <DescriptionListDescription>
                        <Stack>
                          <StackItem>
                            {icmp === true
                              ? t('Supported')
                              : icmp === false
                                ? t('Not supported')
                                : t('Unknown')}
                          </StackItem>
                          <StackItem>
                            <Content component="small">
                              {icmp === true
                                ? t('Every network manager supports ICMP security group rules.')
                                : icmp === false
                                  ? t(
                                      'A network manager does not support ICMP rules, so security groups reject them. The All protocol still includes ICMP.',
                                    )
                                  : t(
                                      'Shown once the network managers are resolved. ICMP rules may be rejected until then.',
                                    )}
                            </Content>
                          </StackItem>
                        </Stack>
                      </DescriptionListDescription>
                    </DescriptionListGroup>
                  </DescriptionList>
                </CardBody>
              </Card>
            </GridItem>
            <GridItem md={6}>
              <Card isFullHeight>
                <CardTitle>{t('Tenant defaults')}</CardTitle>
                <CardBody>
                  <Stack hasGutter>
                    <StackItem>
                      <SubtleContent component="p">
                        {t(
                          "Every tenant's default virtual network, subnet and security group are created with these values. They are set at installation.",
                        )}
                      </SubtleContent>
                    </StackItem>
                    <StackItem>
                      <DescriptionList isHorizontal aria-label={t('Tenant defaults')}>
                        <DescriptionListGroup>
                          <DescriptionListTerm>{t('Virtual network CIDR')}</DescriptionListTerm>
                          <DescriptionListDescription>
                            {defaults?.virtualNetworkIpv4Cidr || '—'}
                          </DescriptionListDescription>
                        </DescriptionListGroup>
                        <DescriptionListGroup>
                          <DescriptionListTerm>{t('Subnet CIDR')}</DescriptionListTerm>
                          <DescriptionListDescription>
                            {defaults?.subnetIpv4Cidr || '—'}
                          </DescriptionListDescription>
                        </DescriptionListGroup>
                      </DescriptionList>
                    </StackItem>
                    <StackItem>
                      <Title headingLevel="h3" size="md" className="pf-v6-u-mb-sm">
                        {t('Default security group inbound rules')}
                      </Title>
                      <SecurityGroupRulesTable
                        rules={defaults?.ingressRules ?? []}
                        direction="ingress"
                        emptyText={t('No inbound rules.')}
                      />
                    </StackItem>
                    <StackItem>
                      <Title headingLevel="h3" size="md" className="pf-v6-u-mb-sm">
                        {t('Default security group outbound rules')}
                      </Title>
                      <SecurityGroupRulesTable
                        rules={defaults?.egressRules ?? []}
                        direction="egress"
                        emptyText={t('No outbound rules.')}
                      />
                    </StackItem>
                  </Stack>
                </CardBody>
              </Card>
            </GridItem>
            <GridItem md={6}>
              <Card isFullHeight>
                <CardTitle>{t('Subnet address plan')}</CardTitle>
                <CardBody>
                  <Stack hasGutter>
                    <StackItem>
                      <SubtleContent component="p">
                        {t(
                          "Each subnet's addresses are split into these ranges when it is created. The plan is set at installation.",
                        )}
                      </SubtleContent>
                    </StackItem>
                    <StackItem>
                      {addressPlan ? (
                        <DescriptionList isHorizontal aria-label={t('Subnet address plan')}>
                          {[
                            {
                              term: t('Virtual machines'),
                              share: addressPlan.overlay,
                              usedFor: t('Addresses from the Kubernetes manager.'),
                            },
                            {
                              term: t('Fabric'),
                              share: addressPlan.fabric,
                              usedFor: t(
                                'Fabric DHCP for bare-metal servers and cluster workers, and provider devices.',
                              ),
                            },
                            {
                              term: t('Cluster ingress'),
                              share: addressPlan.lbVip,
                              usedFor: t('Addresses reserved for cluster ingress.'),
                            },
                          ].map(({ term, share, usedFor }) => (
                            <DescriptionListGroup key={term}>
                              <DescriptionListTerm>{term}</DescriptionListTerm>
                              <DescriptionListDescription>
                                <Stack>
                                  <StackItem>{formatShare(share)}</StackItem>
                                  <StackItem>
                                    <Content component="small">{usedFor}</Content>
                                  </StackItem>
                                </Stack>
                              </DescriptionListDescription>
                            </DescriptionListGroup>
                          ))}
                        </DescriptionList>
                      ) : (
                        '—'
                      )}
                    </StackItem>
                  </Stack>
                </CardBody>
              </Card>
            </GridItem>
          </Grid>
        )}
      </ListPageBody>
    </ListPage>
  );
};
