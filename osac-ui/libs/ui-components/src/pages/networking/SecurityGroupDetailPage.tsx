import { useState } from 'react';
import { Link, useNavigate, useParams } from 'react-router-dom';
import {
  Alert,
  Button,
  Card,
  CardBody,
  Content,
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
  Tooltip,
} from '@patternfly/react-core';

import { SecurityGroupState } from '@osac/types';

import {
  resourceDisplayName,
  useDeleteSecurityGroup,
  useSecurityGroup,
  useVirtualNetworks,
} from '../../api/v1/networking';
import { useSecurityGroupDeleteDisabledReason } from '../../components/networking/deleteDisabledReasons';
import NetworkingProjectName from '../../components/networking/NetworkingProjectName';
import {
  autoCreatedTooltip,
  isAutoCreatedResource,
} from '../../components/networking/resourceOrigin';
import ResourceOriginBadge from '../../components/networking/ResourceOriginBadge';
import { SecurityGroupRulesTable } from '../../components/networking/SecurityGroupRulesTable';
import {
  securityGroupRuleEditDisabledReason,
  securityGroupStatusMessage,
} from '../../components/networking/securityGroupRuleUtils';
import { SecurityGroupStatusLabel } from '../../components/networking/SecurityGroupStatusLabel';
import StatusWithMessage from '../../components/networking/StatusWithMessage';
import { useNetworkingTenants } from '../../components/networking/useNetworkingTenants';
import ListPageBody from '../../components/Page/ListPageBody';
import { Timestamp } from '../../components/Primitives/Timestamp';
import DeleteResourceModal from '../../components/Resource/DeleteResourceModal';
import { ResourceDetailHeader } from '../../components/Resource/ResourceDetailHeader';
import { useTranslation } from '../../hooks/useTranslation';

export const SecurityGroupDetailPage = () => {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const { id } = useParams() as { id: string };
  const { isAdmin, tenantName } = useNetworkingTenants();
  const [activeTabKey, setActiveTabKey] = useState<string | number>(0);
  const [showDeleteSgModal, setShowDeleteSgModal] = useState(false);

  const { data: sg, isLoading, error } = useSecurityGroup(id);
  const { data: virtualNetworks = [] } = useVirtualNetworks();
  const deleteSecurityGroup = useDeleteSecurityGroup();

  const sgName = sg?.metadata?.name ?? id;
  const isFailed = sg?.status?.state === SecurityGroupState.FAILED;
  // OSAC manages the rules of an automatic group; the default group's rules stay editable.
  const ruleEditDisabledReason =
    sg && isAutoCreatedResource(sg)
      ? autoCreatedTooltip(t, sg)
      : securityGroupRuleEditDisabledReason(t, sg);
  const deleteDisabledReason = useSecurityGroupDeleteDisabledReason(sg);

  const vnId = sg?.spec?.virtualNetwork?.id ?? '';
  const vn = virtualNetworks.find((v) => v.id === vnId);
  const vnName = resourceDisplayName(vn?.metadata, vnId);

  const editRulesButton = (
    <Button
      variant="primary"
      isAriaDisabled={Boolean(ruleEditDisabledReason)}
      onClick={
        ruleEditDisabledReason
          ? undefined
          : () => navigate(`/networking/security-groups/${id}/edit-rules`)
      }
    >
      {t('Edit rules')}
    </Button>
  );

  const deleteButton = (
    <Button
      variant="secondary"
      isAriaDisabled={Boolean(deleteDisabledReason)}
      onClick={deleteDisabledReason ? undefined : () => setShowDeleteSgModal(true)}
    >
      {t('Delete security group')}
    </Button>
  );

  return (
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
                parents={[{ to: '/networking/security-groups', label: t('Security groups') }]}
                resourceName={sgName}
                titleAddon={
                  sg ? (
                    <Flex
                      spaceItems={{ default: 'spaceItemsSm' }}
                      alignItems={{ default: 'alignItemsCenter' }}
                    >
                      <FlexItem>
                        {/* A FAILED group shows its message once, in the alert below. */}
                        <StatusWithMessage
                          message={isFailed ? undefined : securityGroupStatusMessage(sg)}
                        >
                          <SecurityGroupStatusLabel
                            state={sg.status?.state}
                            message={sg.status?.message}
                          />
                        </StatusWithMessage>
                      </FlexItem>
                      <FlexItem>
                        <ResourceOriginBadge resource={sg} />
                      </FlexItem>
                    </Flex>
                  ) : undefined
                }
              />
            </FlexItem>
            {sg ? (
              <FlexItem>
                <Flex spaceItems={{ default: 'spaceItemsSm' }}>
                  <FlexItem>
                    {ruleEditDisabledReason ? (
                      <Tooltip content={ruleEditDisabledReason}>{editRulesButton}</Tooltip>
                    ) : (
                      editRulesButton
                    )}
                  </FlexItem>
                  <FlexItem>
                    {deleteDisabledReason ? (
                      <Tooltip content={deleteDisabledReason}>{deleteButton}</Tooltip>
                    ) : (
                      deleteButton
                    )}
                  </FlexItem>
                </Flex>
              </FlexItem>
            ) : null}
          </Flex>
        </StackItem>
        <StackItem>
          <ListPageBody isLoading={isLoading} error={error}>
            <Stack hasGutter>
              {/* Shown even without a message: the enforcement note and the fix still apply. */}
              {isFailed && sg ? (
                <StackItem>
                  <Alert variant="danger" title={t('Security group failed')} isInline>
                    {sg.status?.message ? (
                      <Content component="p">{sg.status.message}</Content>
                    ) : null}
                    <Content component="p">
                      {t(
                        'The rules below are saved but may not be enforced: each part of the network keeps the rules of its last successful update until a save succeeds.',
                      )}
                    </Content>
                    {!isAutoCreatedResource(sg) ? (
                      <Content component="p">
                        {t('Fix the rules with Edit rules and save them again.')}
                      </Content>
                    ) : null}
                  </Alert>
                </StackItem>
              ) : null}
              <StackItem>
                <Tabs
                  activeKey={activeTabKey}
                  onSelect={(_event, tabIndex) => setActiveTabKey(tabIndex)}
                  aria-label={t('Security group tabs')}
                >
                  <Tab eventKey={0} title={<TabTitleText>{t('Details')}</TabTitleText>}>
                    <Card className="pf-v6-u-mt-md">
                      <CardBody>
                        <DescriptionList isHorizontal>
                          <DescriptionListGroup>
                            <DescriptionListTerm>{t('Virtual network')}</DescriptionListTerm>
                            <DescriptionListDescription>
                              {vnId ? (
                                <Link to={`/networking/virtual-networks/${vnId}`}>{vnName}</Link>
                              ) : (
                                vnName
                              )}
                            </DescriptionListDescription>
                          </DescriptionListGroup>
                          <DescriptionListGroup>
                            <DescriptionListTerm>{t('Project')}</DescriptionListTerm>
                            <DescriptionListDescription>
                              <NetworkingProjectName
                                project={sg?.metadata?.project}
                                tenant={sg?.metadata?.tenant}
                              />
                            </DescriptionListDescription>
                          </DescriptionListGroup>
                          {isAdmin ? (
                            <DescriptionListGroup>
                              <DescriptionListTerm>{t('Tenant')}</DescriptionListTerm>
                              <DescriptionListDescription>
                                {tenantName(sg?.metadata?.tenant)}
                              </DescriptionListDescription>
                            </DescriptionListGroup>
                          ) : null}
                          <DescriptionListGroup>
                            <DescriptionListTerm>{t('Created')}</DescriptionListTerm>
                            <DescriptionListDescription>
                              <Timestamp value={sg?.metadata?.creationTimestamp} />
                            </DescriptionListDescription>
                          </DescriptionListGroup>
                        </DescriptionList>
                      </CardBody>
                    </Card>
                  </Tab>
                  <Tab eventKey={1} title={<TabTitleText>{t('Inbound rules')}</TabTitleText>}>
                    <div className="pf-v6-u-mt-md">
                      <SecurityGroupRulesTable
                        rules={sg?.spec?.ingress ?? []}
                        direction="ingress"
                      />
                    </div>
                  </Tab>
                  <Tab eventKey={2} title={<TabTitleText>{t('Outbound rules')}</TabTitleText>}>
                    <div className="pf-v6-u-mt-md">
                      <SecurityGroupRulesTable rules={sg?.spec?.egress ?? []} direction="egress" />
                    </div>
                  </Tab>
                </Tabs>
              </StackItem>
            </Stack>
          </ListPageBody>
        </StackItem>
      </Stack>

      {showDeleteSgModal && (
        <DeleteResourceModal
          resourceName={sgName}
          label={t(
            'This will permanently delete the security group and all its rules. This action cannot be undone.',
          )}
          errorLabel={t('Failed to delete security group')}
          onClose={() => setShowDeleteSgModal(false)}
          onSuccess={() => navigate('/networking/security-groups')}
          mutation={deleteSecurityGroup}
          variables={id}
        />
      )}
    </PageSection>
  );
};
